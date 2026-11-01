package main_test

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/safewriter"
	"github.com/yberkut/cleanpass/internal/testkit"
)

const (
	richMailEntry = `/Пошта <&> "quoted" 😀`
	richWorkEntry = "/Робота/Друге"
	richFileBytes = "synthetic attachment bytes \x00\x01\x02 end"
	// secretMarker stands in every secret of the fixtures.
	secretMarker = "SECRET-MARKER"
)

// mergeableXML is the rich fixture with one change.
//
// In the fixture the older version of Пошта moved later than its main version, and no save of
// KeePassXC gives that. The engine takes the place of the newest move for the main version, so
// the export would differ there. Here the versions lie in the order KeePassXC leaves them.
func mergeableXML() string {
	return strings.Replace(testkit.RichXML(), "<LocationChanged>2026-02-01T10:00:03Z<", "<LocationChanged>2026-02-01T09:00:03Z<", 1)
}

// replaced changes one text of the fixture and fails when that text is not there.
func replaced(t *testing.T, xml string, pairs ...string) string {
	t.Helper()
	for i := 0; i < len(pairs); i += 2 {
		if !strings.Contains(xml, pairs[i]) {
			t.Fatalf("no %q in the fixture", pairs[i])
		}
		xml = strings.ReplaceAll(xml, pairs[i], pairs[i+1])
	}
	return xml
}

// A newer main version of Друге in one copy, and a new entry in the other. In the result the
// older main version of Друге must go to the history.
const (
	divergedSecret = secretMarker + "-друге-нове"
	addedSecret    = secretMarker + "-додане"
	addedEntry     = `<Entry><UUID>AAAAAAAAAAAAAAAAAAABCQ==</UUID><IconID>0</IconID><ForegroundColor/><BackgroundColor/><OverrideURL/><Tags/><Times>
  <LastModificationTime>2026-03-20T10:00:00Z</LastModificationTime><CreationTime>2026-03-20T10:00:01Z</CreationTime>
  <LastAccessTime>2026-03-20T10:00:02Z</LastAccessTime><ExpiryTime>2030-01-01T10:00:08Z</ExpiryTime>
  <Expires>False</Expires><UsageCount>0</UsageCount><LocationChanged>2026-03-20T10:00:03Z</LocationChanged>
</Times><String><Key>Title</Key><Value>Додане</Value></String><String><Key>Password</Key><Value ProtectInMemory="True">` + addedSecret + `</Value></String>
<AutoType><Enabled>True</Enabled><DataTransferObfuscation>0</DataTransferObfuscation></AutoType><History/></Entry>`
)

func divergedPair(t *testing.T, dir string) (a, b testkit.DB) {
	t.Helper()
	xmlA := replaced(t, mergeableXML(),
		"SECRET-MARKER-друге", divergedSecret,
		"<LastModificationTime>2026-03-01T10:00:03Z<", "<LastModificationTime>2026-05-01T10:00:00Z<")
	xmlB := replaced(t, mergeableXML(), "<Group><UUID>AAAAAAAAAAAAAAAAAAAAIA==</UUID>", addedEntry+"<Group><UUID>AAAAAAAAAAAAAAAAAAAAIA==</UUID>")
	a = testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, xmlA)
	b = testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, xmlB)
	return a, b
}

// wantMerged checks the exit code of a merge that must work.
func wantMerged(t *testing.T, got testkit.Result) {
	t.Helper()
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
	}
}

// wantStopped checks a merge that must give no result and leave no temporary file.
func wantStopped(t *testing.T, got testkit.Result, output string) {
	t.Helper()
	if got.Code != 3 {
		t.Errorf("exit code %d, want 3\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Errorf("a file lies at %s", output)
	}
	wantNoTempFiles(t, filepath.Dir(output))
}

func wantNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	if found := tempFiles(t, dir); len(found) > 0 {
		t.Errorf("temporary files stayed: %q", found)
	}
}

func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, "cleanpass-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func digestOf(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(content)
}

// keptAllLines checks that the real KeePassXC sees in the result each line it sees in a copy.
// The limits of the history may rise in a result, and an entry with no history may get one, so
// those lines do not count.
func keptAllLines(t *testing.T, result testkit.DB, copies ...testkit.DB) {
	t.Helper()
	have := map[string]int{}
	for _, line := range result.ExportLines(t) {
		have[line]++
	}
	for _, c := range copies {
		seen := map[string]int{}
		for _, line := range c.ExportLines(t) {
			if strings.HasPrefix(line, "<HistoryMax") || line == "<History/>" {
				continue
			}
			if seen[line]++; seen[line] > have[line] {
				t.Errorf("KeePassXC sees %q in %s, but not in the result", line, c.Path)
			}
		}
	}
}

func TestMergeGivesAFileKeePassXCReadsWithoutLoss(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	beforeA, beforeB := digestOf(t, a.Path), digestOf(t, b.Path)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

	got := testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", result.Path)

	wantMerged(t, got)
	wantUkrainian(t, got.Stdout, got.Stderr, a.Path, b.Path, result.Path)
	if !strings.Contains(got.Stdout, result.Path) {
		t.Errorf("the output does not name the result:\n%s", got.Stdout)
	}
	keptAllLines(t, result, a, b)
	if digestOf(t, a.Path) != beforeA || digestOf(t, b.Path) != beforeB {
		t.Error("an input changed")
	}
	wantNoTempFiles(t, dir)
}

func passwordLines(n int) string { return strings.Repeat(testkit.Password+"\n", n) }

func TestMergeKeepsEveryKindOfData(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}
	wantMerged(t, testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", result.Path))

	export := strings.Join(result.ExportLines(t), "\n")
	for name, want := range map[string]string{
		"main password":    "SECRET-MARKER-поточний пароль",
		"older password":   "SECRET-MARKER-старий пароль",
		"both conflicting": divergedSecret,
		"the other main":   ">SECRET-MARKER-друге<",
		"the new entry":    addedSecret,
		"TOTP":             "otpauth://totp/Example:user?secret=JBSWY3DPEHPK3PXP",
		"passkey key":      "<Key>KPEX_PASSKEY_PRIVATE_KEY_PEM</Key>",
		"passkey value":    "SECRET-MARKER-passkey",
		"custom field":     "<Key>Власне поле</Key>",
		"its value":        "власне значення",
		"empty field":      "<Key>EmptyField</Key>",
		"Auto-Type":        "{USERNAME}{TAB}{PASSWORD}",
		"Auto-Type window": "*Firefox*",
		"tag":              "бета",
		"foreground":       "#112233",
		"background":       "#445566",
		"custom icon":      "<CustomIconUUID>AAAAAAAAAAAAAAAAAAAJAQ==</CustomIconUUID>",
		"browser data":     "<Key>KPXC_BROWSER_SETTINGS</Key>",
		"attachment name":  "<Key>same-content.bin</Key>",
		"older attachment": "<Key>old.bin</Key>",
		"database name":    "Синтетична база",
		"database data":    "meta-custom-value",
		"group data":       "group-value",
	} {
		if !strings.Contains(export, want) {
			t.Errorf("KeePassXC does not see the %s: %q", name, want)
		}
	}
	if got := result.AttachmentBytes(t, richMailEntry, "file.bin"); string(got) != richFileBytes {
		t.Errorf("file.bin holds %q", got)
	}
	if got := result.AttachmentBytes(t, richWorkEntry, "same-content.bin"); string(got) != richFileBytes {
		t.Errorf("same-content.bin holds %q", got)
	}
}

// Both copies hold the same bytes under two names, and each stores them on its own.
func TestMergeStoresTheSameAttachmentOnce(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}
	wantMerged(t, testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", result.Path))

	// The bytes of file.bin and same-content.bin, and the bytes of old.bin.
	if stored := result.AttachmentsInFile(t); stored != 2 {
		t.Errorf("the file stores %d attachments, want 2", stored)
	}
}

func TestMergeKeepsAnAttachmentThatLooksLikeBase64(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	const content = "abcdQUJD"
	a.AttachFile(t, richWorkEntry, "note.txt", []byte(content))
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

	wantMerged(t, testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", result.Path))

	if got := result.AttachmentBytes(t, richWorkEntry, "note.txt"); string(got) != content {
		t.Errorf("KeePassXC reads %q, want %q", got, content)
	}
}

func TestMergeMakesNewRandomValues(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}
	wantMerged(t, testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", result.Path))

	out := result.Randoms(t)
	for _, in := range []testkit.Randoms{a.Randoms(t), b.Randoms(t)} {
		for name, pair := range map[string][2][]byte{
			"MasterSeed":       {out.MasterSeed, in.MasterSeed},
			"EncryptionIV":     {out.EncryptionIV, in.EncryptionIV},
			"KDF salt":         {out.KDFSalt, in.KDFSalt},
			"inner stream key": {out.InnerStreamKey, in.InnerStreamKey},
		} {
			if bytes.Equal(pair[0], pair[1]) {
				t.Errorf("%s of the result is the same as in a copy", name)
			}
		}
	}
}

func TestMergeKeepsEveryVersionOfAllCopiesAboveTheHistoryLimit(t *testing.T) {
	dir := t.TempDir()
	versions := func(prefix string, first int) string {
		var history strings.Builder
		for i := range 8 {
			day := first + i
			history.WriteString(`<Entry><UUID>AAAAAAAAAAAAAAAAAAABAQ==</UUID><Times><LastModificationTime>2026-02-` + twoDigits(day) + `T10:00:00Z</LastModificationTime></Times><String><Key>Title</Key><Value>Друге</Value></String><String><Key>Password</Key><Value ProtectInMemory="True">` + secretMarker + `-` + prefix + `-` + twoDigits(day) + `</Value></String></Entry>`)
		}
		return history.String()
	}
	xmlOf := func(prefix string, first int) string {
		return replaced(t, mergeableXML(), `<AutoType><Enabled>False</Enabled><DataTransferObfuscation>0</DataTransferObfuscation></AutoType><History/>`,
			`<AutoType><Enabled>False</Enabled><DataTransferObfuscation>0</DataTransferObfuscation></AutoType><History>`+versions(prefix, first)+`</History>`,
			"<HistoryMaxItems>25<", "<HistoryMaxItems>10<")
	}
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, xmlOf("a", 1))
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, xmlOf("b", 11))
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

	wantMerged(t, testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", result.Path))

	export := strings.Join(result.ExportLines(t), "\n")
	for _, version := range []struct {
		prefix string
		first  int
	}{{"a", 1}, {"b", 11}} {
		for i := range 8 {
			if want := secretMarker + "-" + version.prefix + "-" + twoDigits(version.first+i); !strings.Contains(export, want) {
				t.Errorf("KeePassXC does not see the version with %s", want)
			}
		}
	}
}

func twoDigits(n int) string {
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestMergeWithThreeCopiesAndPathsWithSpacesAndCyrillic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "папка з пробілом")
	a, b := divergedPair(t, dir+" а")
	c := testkit.DB{Path: filepath.Join(dir, "третя копія.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	result := testkit.DB{Path: filepath.Join(dir, "нова база.kdbx"), Password: testkit.Password}

	wantMerged(t, testkit.Run(t, passwordLines(3), "merge", a.Path, b.Path, c.Path, "--output", result.Path))

	keptAllLines(t, result, a, b, c)
	wantNoTempFiles(t, dir)
}

func TestMergeWithASingleCopyMakesTheSameDatabase(t *testing.T) {
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

	wantMerged(t, testkit.Run(t, passwordLines(1), "merge", a.Path, "--output", result.Path))

	if got, want := result.ExportLines(t), a.ExportLines(t); !slices.Equal(got, want) {
		t.Errorf("KeePassXC sees %d lines in the result and %d in the copy", len(got), len(want))
	}
}

func TestMergeTakesTheOutputInEveryPlace(t *testing.T) {
	for name, args := range map[string][]string{
		"at the end":      {"a.kdbx", "b.kdbx", "--output", "new.kdbx"},
		"at the start":    {"--output", "new.kdbx", "a.kdbx", "b.kdbx"},
		"in the middle":   {"a.kdbx", "--output", "new.kdbx", "b.kdbx"},
		"with an equals":  {"a.kdbx", "b.kdbx", "--output=new.kdbx"},
		"before the mark": {"--output", "new.kdbx", "--", "a.kdbx", "b.kdbx"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			divergedPair(t, dir)
			got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Dir: dir}, append([]string{"merge"}, args...)...)
			wantMerged(t, got)
			if _, err := os.Stat(filepath.Join(dir, "new.kdbx")); err != nil {
				t.Error(err)
			}
		})
	}
}

// The word after --output is its value, and after -- every word is a file. Both can look like
// an option: status offers such a command for a copy with such a name.
func TestMergeTakesNamesThatLookLikeOptions(t *testing.T) {
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "--a.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Dir: dir}, "merge", "--output", "--a-merged.kdbx", "--", "--a.kdbx", "b.kdbx")

	wantMerged(t, got)
	result := testkit.DB{Path: filepath.Join(dir, "--a-merged.kdbx"), Password: testkit.Password}
	keptAllLines(t, result, a, b)
}

// printedMergeLine gives the line status offers as the next command, as it is.
func printedMergeLine(t *testing.T, stdout string) string {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(stdout, "\r", ""), "\n")
	at := slices.Index(lines, "Наступна команда:")
	if at < 0 || at+1 == len(lines) {
		t.Fatalf("no next command in:\n%s", stdout)
	}
	return strings.TrimSpace(lines[at+1])
}

func posixShell(t *testing.T) string {
	t.Helper()
	if shell, err := exec.LookPath("sh"); err == nil {
		return shell
	}
	shell := `C:\Program Files\Git\bin\sh.exe`
	if _, err := os.Stat(shell); err != nil {
		t.Skip("no sh on this system")
	}
	return shell
}

// status prints a command for the person to copy. A real shell runs that very line here, and
// it must make the result it promises.
func TestMergeRunsTheLineThatStatusPrints(t *testing.T) {
	const keyName = "мій ключ.key"
	for name, tc := range map[string]struct {
		first, second, result string
		withKey               bool
		// statusArgs follow the word status.
		statusArgs []string
		// wantInLine says that the line has the form this case is about.
		wantInLine string
	}{
		"plain names": {
			first: "a.kdbx", second: "b.kdbx", result: "a-merged.kdbx",
			statusArgs: []string{"a.kdbx", "b.kdbx"},
			wantInLine: "merge a.kdbx b.kdbx --output a-merged.kdbx",
		},
		"names with spaces and a key file": {
			first: "копія один.kdbx", second: "b.kdbx", result: "копія один-merged.kdbx", withKey: true,
			statusArgs: []string{"копія один.kdbx", "--key-file", keyName, "b.kdbx"},
			wantInLine: " --key-file ",
		},
		"files after the mark": {
			first: "--a.kdbx", second: "b.kdbx", result: "--a-merged.kdbx",
			statusArgs: []string{"--", "--a.kdbx", "b.kdbx"},
			wantInLine: "merge --output --a-merged.kdbx -- --a.kdbx b.kdbx",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			keyFile := ""
			if tc.withKey {
				keyFile = testkit.NewKeyFile(t, filepath.Join(dir, keyName), 1)
			}
			xmlOfFirst := mergeableXML()
			if tc.withKey {
				// The newest copy gives the keys. A date after the two edits below makes that
				// the copy with the key file, whichever second they fall in.
				xmlOfFirst = replaced(t, xmlOfFirst, "<LastModificationTime>2026-03-01T10:00:04Z<", "<LastModificationTime>2099-03-01T10:00:04Z<")
			}
			a := testkit.DB{Path: filepath.Join(dir, tc.first), Password: testkit.Password, KeyFile: keyFile}.Import(t, xmlOfFirst)
			b := testkit.DB{Path: filepath.Join(dir, tc.second), Password: testkit.Password}.Import(t, mergeableXML())
			a.CLI(t, "edit", "--notes", "зміна в першій копії", "/Кошик/У кошику")
			b.CLI(t, "edit", "--notes", "зміна в другій копії", richWorkEntry)

			status := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Dir: dir}, append([]string{"status"}, tc.statusArgs...)...)
			if status.Code != 2 {
				t.Fatalf("status gave exit code %d, want 2\nstdout:\n%s\nstderr:\n%s", status.Code, status.Stdout, status.Stderr)
			}
			line := printedMergeLine(t, status.Stdout)
			if !strings.Contains(line, tc.wantInLine) {
				t.Fatalf("the line %q does not hold %q", line, tc.wantInLine)
			}

			shell := exec.Command(posixShell(t), "-c", line)
			shell.Dir = dir
			shell.Stdin = strings.NewReader(passwordLines(2))
			var stdout, stderr bytes.Buffer
			shell.Stdout, shell.Stderr = &stdout, &stderr
			if err := shell.Run(); err != nil {
				t.Fatalf("%s: %v\nstdout:\n%s\nstderr:\n%s", line, err, stdout.String(), stderr.String())
			}

			result := testkit.DB{Path: filepath.Join(dir, tc.result), Password: testkit.Password, KeyFile: keyFile}
			if !strings.Contains(stdout.String(), tc.result) {
				t.Errorf("the output does not name %s:\n%s", tc.result, stdout.String())
			}
			export := strings.Join(result.ExportLines(t), "\n")
			for _, want := range []string{"зміна в першій копії", "зміна в другій копії"} {
				if !strings.Contains(export, want) {
					t.Errorf("KeePassXC does not see %q in the result", want)
				}
			}
		})
	}
}

// One copy holds an older version and has moved the entry to the recycle bin. With no answer
// the entry stays outside the bin, so that older version has no place of its own: the engine
// gives it no LocationChanged. The file must then have no such element, and the real
// KeePassXC must still read the version.
func TestMergeKeepsAVersionWithoutALocationTime(t *testing.T) {
	const onlyInTheBin = secretMarker + "-лише-в-кошику"
	dir := t.TempDir()
	withOlderVersion := replaced(t, mergeableXML(),
		`<AutoType><Enabled>False</Enabled><DataTransferObfuscation>0</DataTransferObfuscation></AutoType><History/>`,
		`<AutoType><Enabled>False</Enabled><DataTransferObfuscation>0</DataTransferObfuscation></AutoType><History><Entry><UUID>AAAAAAAAAAAAAAAAAAABAQ==</UUID><Times><LastModificationTime>2026-02-15T10:00:00Z</LastModificationTime><LocationChanged>2026-02-01T09:00:05Z</LocationChanged></Times><String><Key>Title</Key><Value>Друге</Value></String><String><Key>Password</Key><Value ProtectInMemory="True">`+onlyInTheBin+`</Value></String></Entry></History>`)
	inBin := testkit.DB{Path: filepath.Join(dir, "in-bin.kdbx"), Password: testkit.Password}.Import(t, withOlderVersion)
	inBin.CLI(t, "rm", richWorkEntry)
	outside := testkit.DB{Path: filepath.Join(dir, "outside.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

	wantMerged(t, testkit.Run(t, passwordLines(2), "merge", inBin.Path, outside.Path, "--output", result.Path))

	if count := result.VersionsWithoutLocationTime(t); count != 1 {
		t.Errorf("%d versions have no LocationChanged in the file, want this one", count)
	}
	if export := strings.Join(result.ExportLines(t), "\n"); !strings.Contains(export, onlyInTheBin) {
		t.Error("KeePassXC does not see the older version")
	}
	// A save by KeePassXC goes through its reader and writer. The version must get through.
	result.Resave(t)
	if export := strings.Join(result.ExportLines(t), "\n"); !strings.Contains(export, onlyInTheBin) {
		t.Error("the older version is gone after a save by KeePassXC")
	}
}

func TestMergeRefusesBadCommandLines(t *testing.T) {
	dir := t.TempDir()
	a, _ := divergedPair(t, dir)
	for name, args := range map[string][]string{
		"no output":        {a.Path},
		"an empty output":  {a.Path, "--output="},
		"no output value":  {a.Path, "--output"},
		"the output twice": {a.Path, "--output", "one.kdbx", "--output", "two.kdbx"},
		"no files":         {"--output", "new.kdbx"},
		"unknown option":   {a.Path, "--output", "new.kdbx", "--force"},
		"only after mark":  {"--", a.Path, "--output", "new.kdbx"},
	} {
		t.Run(name, func(t *testing.T) {
			got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Dir: dir}, append([]string{"merge"}, args...)...)
			if got.Code != 3 {
				t.Errorf("exit code %d\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
			}
			if strings.Contains(got.Stderr, "Пароль") {
				t.Errorf("it asked for a password first:\n%s", got.Stderr)
			}
			for _, path := range []string{"new.kdbx", "one.kdbx", "two.kdbx"} {
				if _, err := os.Stat(filepath.Join(dir, path)); err == nil {
					t.Errorf("a file lies at %s", path)
				}
			}
			wantUkrainian(t, got.Stdout, got.Stderr, a.Path, "--output", "--force")
		})
	}
}

func TestMergeRefusesToOverwriteAndAsksNoPassword(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	existing := filepath.Join(dir, "existing.kdbx")
	if err := os.WriteFile(existing, []byte("the file of the person"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string]string{"another file": existing, "an input": a.Path, "the other input": b.Path, "a directory": dir} {
		t.Run(name, func(t *testing.T) {
			before := digestOf(t, a.Path)
			got := testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", output)
			if got.Code != 3 {
				t.Errorf("exit code %d\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
			}
			if strings.Contains(got.Stderr, "Пароль") {
				t.Errorf("it asked for a password first:\n%s", got.Stderr)
			}
			if !strings.Contains(got.Stderr, output) {
				t.Errorf("the message does not name the path:\n%s", got.Stderr)
			}
			if digestOf(t, a.Path) != before {
				t.Error("an input changed")
			}
			wantUkrainian(t, got.Stdout, got.Stderr, output)
			wantNoTempFiles(t, dir)
		})
	}
	content, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "the file of the person" {
		t.Errorf("the existing file now holds %q", content)
	}
}

func TestMergeRefusesATargetInADirectoryThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	a, _ := divergedPair(t, dir)
	output := filepath.Join(dir, "no-such-directory", "new.kdbx")
	got := testkit.Run(t, passwordLines(1), "merge", a.Path, "--output", output)
	if got.Code != 3 || strings.Contains(got.Stderr, "Пароль") {
		t.Errorf("exit code %d\nstderr:\n%s", got.Code, got.Stderr)
	}
	wantUkrainian(t, got.Stdout, got.Stderr, output)
}

func TestMergeStopsAtAFieldItCannotKeep(t *testing.T) {
	dir := t.TempDir()
	good := testkit.DB{Path: filepath.Join(dir, "good.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	unknown := testkit.UnknownFields(t, filepath.Join(dir, "unknown.kdbx"))
	output := filepath.Join(dir, "new.kdbx")

	for name, order := range map[string][]string{
		"on its own": {unknown.Path},
		"as first":   {unknown.Path, good.Path},
		"as second":  {good.Path, unknown.Path},
	} {
		t.Run(name, func(t *testing.T) {
			args := append(append([]string{"merge"}, order...), "--output", output)
			got := testkit.Run(t, passwordLines(len(order)), args...)

			wantStopped(t, got, output)
			for _, path := range testkit.UnknownFieldPaths {
				if !strings.Contains(got.Stdout+got.Stderr, path) {
					t.Errorf("the output does not name %s:\n%s\n%s", path, got.Stdout, got.Stderr)
				}
			}
			if strings.Contains(got.Stdout+got.Stderr, testkit.UnknownFieldValue) {
				t.Errorf("the output holds a value of an unknown field:\n%s\n%s", got.Stdout, got.Stderr)
			}
			if !strings.Contains(got.Stderr, unknown.Path) {
				t.Errorf("the message does not name the copy:\n%s", got.Stderr)
			}
		})
	}
}

// The header of a file can also hold data. A result gets a header of its own.
func TestMergeStopsAtHeaderDataItCannotKeep(t *testing.T) {
	const comment = "keep-me-too"
	custom := []testkit.KDFParam{{Name: "plugin", Value: []byte("keep-me-too")}}
	for name, tc := range map[string]struct {
		file testkit.RawFile
		path string
	}{
		"comment in KDBX 4":                {testkit.RawFile{Major: 4, Minor: 1, XML: testkit.PlainXML, Comment: []byte(comment)}, "OuterHeader/Comment (у файлі: 1, лишиться: 0)"},
		"comment in KDBX 3":                {testkit.RawFile{Major: 3, Minor: 1, XML: testkit.PlainXML, Comment: []byte(comment)}, "OuterHeader/Comment (у файлі: 1, лишиться: 0)"},
		"public custom data":               {testkit.RawFile{Major: 4, Minor: 1, XML: testkit.PlainXML, PublicCustomData: custom}, "OuterHeader/PublicCustomData (у файлі: 1, лишиться: 0)"},
		"attachments in the XML of KDBX 4": {testkit.RawFile{Major: 4, Minor: 1, XML: strings.Replace(testkit.PlainXML, "</Meta>", `<Binaries><Binary ID="0" Compressed="False">Ynl0ZXM=</Binary></Binaries></Meta>`, 1)}, "KeePassFile/Meta/Binaries/Binary (у файлі: 1, лишиться: 0)"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db := tc.file.Write(t, filepath.Join(dir, "a.kdbx"), testkit.Password)
			if listing := db.CLI(t, "ls"); !strings.Contains(listing, "Запис") {
				t.Fatalf("keepassxc-cli does not see the entry:\n%s", listing)
			}
			output := filepath.Join(dir, "new.kdbx")

			got := testkit.Run(t, passwordLines(1), "merge", db.Path, "--output", output)

			wantStopped(t, got, output)
			if !strings.Contains(got.Stderr, tc.path) {
				t.Errorf("the message does not hold %q:\n%s", tc.path, got.Stderr)
			}
			if strings.Contains(got.Stdout+got.Stderr, comment) {
				t.Errorf("the output holds the value:\n%s\n%s", got.Stdout, got.Stderr)
			}

			compare := testkit.Run(t, passwordLines(1), "compare", db.Path)
			if !strings.Contains(compare.Stdout, tc.path) {
				t.Errorf("compare does not warn with %q:\n%s", tc.path, compare.Stdout)
			}
		})
	}
}

// The engine may yet lack something for these inputs. A result must then not appear, and when
// it appears, it must hold the data. Ticket #8 turns the stops into results, so the test keeps
// its worth before and after it.
func TestMergeNeverPublishesAResultThatLost(t *testing.T) {
	olderIcon := `<CustomIcons><Icon><UUID>AAAAAAAAAAAAAAAAAAAJAg==</UUID><Data>iVBORw0KGgo=</Data><Name>old icon</Name><LastModificationTime>2026-02-04T10:00:00Z</LastModificationTime></Icon></CustomIcons>`
	for name, tc := range map[string]struct {
		older, newer string
		// inExport is what KeePassXC must show in a result.
		inExport string
	}{
		"a custom icon only the older copy has": {
			replaced(t, testkit.RichXML(), "<CustomIcons>", olderIcon+"<CustomIcons>"),
			replaced(t, testkit.RichXML(), "<SettingsChanged>2026-02-06T10:00:00Z<", "<SettingsChanged>2026-02-07T10:00:00Z<"),
			"AAAAAAAAAAAAAAAAAAAJAg==",
		},
		"custom data only the older copy has": {
			replaced(t, testkit.RichXML(), "<Key>meta-custom-key</Key>", "<Key>older-only-key</Key>"),
			replaced(t, testkit.RichXML(), "<SettingsChanged>2026-02-06T10:00:00Z<", "<SettingsChanged>2026-02-07T10:00:00Z<"),
			"older-only-key",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			older := testkit.DB{Path: filepath.Join(dir, "older.kdbx"), Password: testkit.Password}.Import(t, tc.older)
			newer := testkit.DB{Path: filepath.Join(dir, "newer.kdbx"), Password: testkit.Password}.Import(t, tc.newer)
			result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

			got := testkit.Run(t, passwordLines(2), "merge", older.Path, newer.Path, "--output", result.Path)

			if got.Code == 0 {
				if export := strings.Join(result.ExportLines(t), "\n"); !strings.Contains(export, tc.inExport) {
					t.Errorf("the result is out and KeePassXC does not see %s", tc.inExport)
				}
				return
			}
			wantStopped(t, got, result.Path)
			if !strings.Contains(got.Stderr, "Перевірка на втрати") {
				t.Errorf("the stop does not come from the Loss Audit:\n%s", got.Stderr)
			}
			if strings.Contains(got.Stdout+got.Stderr, secretMarker) {
				t.Errorf("the output holds a secret:\n%s\n%s", got.Stdout, got.Stderr)
			}
		})
	}
}

func TestTheNewestCopyGivesTheKeysTheCipherAndTheKDF(t *testing.T) {
	const passwordOld, passwordNew = "пароль старої копії", "пароль нової копії"
	root := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(root, "key.bin"), 3)
	// A real save stamps a copy with the present moment. A date after that keeps this copy the
	// newest, whichever got its save last.
	newer := replaced(t, mergeableXML(), "<LastModificationTime>2026-03-01T10:00:03Z<", "<LastModificationTime>2099-05-01T10:00:00Z<")

	for name, order := range map[string][2]bool{"the newest given last": {false, true}, "the newest given first": {true, false}} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			oldBase := testkit.DB{Path: filepath.Join(dir, "old-base.kdbx"), Password: passwordOld}.Import(t, mergeableXML())
			newBase := testkit.DB{Path: filepath.Join(dir, "new-base.kdbx"), Password: passwordNew, KeyFile: key}.Import(t, newer)
			old := oldBase.WithEncryption(t, filepath.Join(dir, "old.kdbx"), testkit.ChaCha20, testkit.KDFArgon2d)
			fresh := newBase.WithEncryption(t, filepath.Join(dir, "fresh.kdbx"), testkit.Twofish, testkit.KDFArgon2id)
			result := testkit.DB{Path: filepath.Join(dir, "result.kdbx"), Password: passwordNew, KeyFile: key}

			args := []string{"merge", old.Path, fresh.Path, "--key-file", key, "--output", result.Path}
			stdin := passwordOld + "\n" + passwordNew + "\n"
			if order[0] {
				args = []string{"merge", fresh.Path, "--key-file", key, old.Path, "--output", result.Path}
				stdin = passwordNew + "\n" + passwordOld + "\n"
			}
			got := testkit.Run(t, stdin, args...)
			wantMerged(t, got)
			if !strings.Contains(got.Stdout, fresh.Path) {
				t.Errorf("the output does not say which copy gave the keys:\n%s", got.Stdout)
			}

			keptAllLines(t, result, old, fresh)
			block := passports(t, testkit.Run(t, passwordNew+"\n", "compare", result.Path, "--key-file", key).Stdout, result.Path)[result.Path]
			wantField(t, block, "Формат", "KDBX 4.1")
			wantField(t, block, "Шифр", "Twofish")
			if kdf := field(t, block, "KDF"); !strings.HasPrefix(kdf, "Argon2id") {
				t.Errorf("KDF = %q", kdf)
			}
		})
	}
}

func TestACopyWithoutAPasswordGivesAResultWithoutAPassword(t *testing.T) {
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "key.bin"), 5)
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), KeyFile: key}.Import(t, mergeableXML())
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), KeyFile: key}

	wantMerged(t, testkit.Run(t, "\n", "merge", a.Path, "--key-file", key, "--output", result.Path))

	keptAllLines(t, result, a)
}

// The keys of the result are the ones that fit in the end, not the first ones typed.
func TestTheResultGetsTheKeysThatFit(t *testing.T) {
	const password = "спільний пароль"
	for name, stdin := range map[string]string{
		"typed again after a wrong one": "неправильний\n" + password + "\n\n" + password + "\n",
		"Enter repeats the typed one":   password + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: password}.Import(t, mergeableXML())
			b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: password}.Import(t, mergeableXML())
			result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: password}

			wantMerged(t, testkit.Run(t, stdin, "merge", a.Path, b.Path, "--output", result.Path))

			keptAllLines(t, result, a, b)
		})
	}
}

// Enter tries the password typed before, then no password. The second one fits here, so the
// result must have no password either.
func TestTheResultHasNoPasswordWhenEnterFoundThatTheNewestCopyHasNone(t *testing.T) {
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "key.bin"), 9)
	older := testkit.DB{Path: filepath.Join(dir, "older.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	newest := testkit.DB{Path: filepath.Join(dir, "newest.kdbx"), KeyFile: key}.Import(t,
		replaced(t, mergeableXML(), "<LastModificationTime>2026-03-01T10:00:04Z<", "<LastModificationTime>2099-03-01T10:00:04Z<"))
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), KeyFile: key}

	wantMerged(t, testkit.Run(t, testkit.Password+"\n\n", "merge", older.Path, newest.Path, "--key-file", key, "--output", result.Path))

	// A group has no history, so the older time of the changed group is gone by the spec.
	keptAllLines(t, result, newest)
}

func TestAKDBX3CopyIsUpgraded(t *testing.T) {
	dir := t.TempDir()
	source := testkit.NewKDBX3(t, filepath.Join(dir, "k3.kdbx"), testkit.Password)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

	wantMerged(t, testkit.Run(t, passwordLines(1), "merge", source.Path, "--output", result.Path))

	block := passports(t, testkit.Run(t, passwordLines(1), "compare", result.Path).Stdout, result.Path)[result.Path]
	wantField(t, block, "Формат", "KDBX 4.1")
	wantField(t, block, "Шифр", "AES-256")
	if kdf := field(t, block, "KDF"); !strings.HasPrefix(kdf, "Argon2id") || !strings.Contains(kdf, "64") {
		t.Errorf("KDF = %q, want Argon2id with 64 MiB", kdf)
	}
	if got := result.AttachmentBytes(t, "/kdbx3", "kdbx3.bin"); string(got) != "synthetic kdbx3 attachment" {
		t.Errorf("the attachment holds %q", got)
	}
	export := strings.Join(result.ExportLines(t), "\n")
	for _, secret := range []string{"SECRET-MARKER-new", "SECRET-MARKER-old"} {
		if !strings.Contains(export, secret) {
			t.Errorf("KeePassXC does not see %s", secret)
		}
	}
}

// A copy of KDBX 3 may hold parts of a second, and KDBX 4 cannot.
func TestATimeWithPartsOfASecondDoesNotStopTheMerge(t *testing.T) {
	dir := t.TempDir()
	xml := strings.ReplaceAll(testkit.PlainXML, "2026-03-15T10:00:00Z", "2026-03-15T10:00:00.5Z")
	source := testkit.RawFile{Major: 3, Minor: 1, XML: xml}.Write(t, filepath.Join(dir, "a.kdbx"), testkit.Password)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

	wantMerged(t, testkit.Run(t, passwordLines(1), "merge", source.Path, "--output", result.Path))

	if listing := result.CLI(t, "ls"); !strings.Contains(listing, "Запис") {
		t.Errorf("keepassxc-cli does not see the entry:\n%s", listing)
	}
}

// The person takes their time at a prompt, and a copy that was already read can change in that
// time. The merge must not go on with what it read.
func TestMergeStopsWhenACopyChangesWhileThePersonTypes(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	output := filepath.Join(dir, "new.kdbx")

	process := testkit.Start(t, "merge", a.Path, b.Path, "--output", output)
	process.Send(t, testkit.Password)
	process.WaitForStderr(t, b.Path)
	a.CLI(t, "edit", "--notes", "зміна під час роботи", richWorkEntry)
	process.Send(t, testkit.Password)
	got := process.Wait(t)

	wantStopped(t, got, output)
	if !strings.Contains(got.Stderr, a.Path) {
		t.Errorf("the message does not name the copy:\n%s", got.Stderr)
	}
	wantUkrainian(t, "", got.Stderr, a.Path, b.Path)
}

// allCrashPoints are the points of a crash in the order of the protocol, with whether the
// result is in its place at that moment.
var allCrashPoints = []struct {
	point     string
	published bool
}{
	{safewriter.PointCheckTarget, false},
	{safewriter.PointHashInputs, false},
	{safewriter.PointWriteTemp, false},
	{safewriter.PointPartialWrite, false},
	{safewriter.PointBeforeSync, false},
	{safewriter.PointReadBack, false},
	{safewriter.PointAudit, false},
	{safewriter.PointJudge, false},
	{safewriter.PointPublish, false},
	{safewriter.PointPublished, true},
	{safewriter.PointDone, true},
}

func TestACrashAtAnyPointLeavesTheInputsAndNoHalfOfAResult(t *testing.T) {
	root := t.TempDir()
	a, b := divergedPair(t, root)
	beforeA, beforeB := digestOf(t, a.Path), digestOf(t, b.Path)

	complete := testkit.DB{Path: filepath.Join(root, "complete.kdbx"), Password: testkit.Password}
	wantMerged(t, testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", complete.Path))
	wantLines := complete.ExportLines(t)

	for _, mode := range []struct{ name, fault string }{
		{"with a hard link", ""},
		{"without hard links", safewriter.FaultNoHardLink + ","},
	} {
		for _, crash := range allCrashPoints {
			t.Run(mode.name+" at "+crash.point, func(t *testing.T) {
				dir := t.TempDir()
				result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}
				opts := testkit.Options{
					Stdin: passwordLines(2),
					Env:   []string{safewriter.FaultVariable + "=" + mode.fault + safewriter.FaultCrashPrefix + crash.point},
				}

				got := testkit.RunWith(t, opts, "merge", a.Path, b.Path, "--output", result.Path)

				if got.Code != safewriter.CrashExitCode {
					t.Fatalf("exit code %d, want the crash\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
				}
				if digestOf(t, a.Path) != beforeA || digestOf(t, b.Path) != beforeB {
					t.Error("an input changed")
				}
				wantNoSecretInFiles(t, dir)
				_, err := os.Lstat(result.Path)
				if !crash.published {
					if !os.IsNotExist(err) {
						t.Errorf("a file lies at the target: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if lines := result.ExportLines(t); !slices.Equal(lines, wantLines) {
					t.Errorf("the result is not whole: KeePassXC sees %d lines, want %d", len(lines), len(wantLines))
				}
			})
		}
	}
}

func TestAMergeWorksAfterACrashLeftATemporaryFile(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}
	opts := testkit.Options{
		Stdin: passwordLines(2),
		Env:   []string{safewriter.FaultVariable + "=" + safewriter.FaultCrashPrefix + safewriter.PointJudge},
	}
	if got := testkit.RunWith(t, opts, "merge", a.Path, b.Path, "--output", result.Path); got.Code != safewriter.CrashExitCode {
		t.Fatalf("exit code %d\nstderr:\n%s", got.Code, got.Stderr)
	}
	left := tempFiles(t, dir)
	if len(left) != 1 {
		t.Fatalf("the crash left %q, want one temporary file", left)
	}

	wantMerged(t, testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", result.Path))

	keptAllLines(t, result, a, b)
	if _, err := os.Stat(left[0]); err != nil {
		t.Errorf("the merge touched the temporary file of another run: %v", err)
	}
}

// Without the special variable a run has no crash points. All tests above that expect a result
// prove the rest: no other variable and no argument gets to them.
func TestCrashPointsNeedTheirOwnVariable(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	result := filepath.Join(dir, "new.kdbx")
	opts := testkit.Options{
		Stdin: passwordLines(2),
		Env: []string{
			safewriter.FaultVariable + "=",
			"CLEANPASS_CRASH=" + safewriter.FaultCrashPrefix + safewriter.PointJudge,
			"CLEANPASS_FAULT=" + safewriter.FaultCrashPrefix + safewriter.PointJudge,
		},
	}
	wantMerged(t, testkit.RunWith(t, opts, "merge", a.Path, b.Path, "--output", result))
}

func wantNoSecretInFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(content, []byte(secretMarker)) {
			t.Errorf("%s holds a secret in clear text", entry.Name())
		}
	}
}

func TestSecretsStayOutOfTheOutputAndTheDirectoryOfTheResult(t *testing.T) {
	root := t.TempDir()
	a, b := divergedPair(t, root)
	dir := t.TempDir()
	got := testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", filepath.Join(dir, "new.kdbx"))

	wantMerged(t, got)
	if strings.Contains(got.Stdout+got.Stderr, secretMarker) || strings.Contains(got.Stdout+got.Stderr, testkit.Password) {
		t.Errorf("the output holds a secret:\n%s\n%s", got.Stdout, got.Stderr)
	}
	wantNoSecretInFiles(t, dir)
}

func TestMergeSaysThatNoOutsideProgramCheckedTheResult(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	got := testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", filepath.Join(dir, "new.kdbx"))
	wantMerged(t, got)
	if !strings.Contains(got.Stdout, "KeePassXC") {
		t.Errorf("no word about the outside check:\n%s", got.Stdout)
	}
}

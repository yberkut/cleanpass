package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

// wantCode checks the exit code of status, the part of its output scripts rely on.
func wantCode(t *testing.T, got testkit.Result, want int) {
	t.Helper()
	if got.Code != want {
		t.Errorf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", got.Code, want, got.Stdout, got.Stderr)
	}
}

// Entries of the fixture, as paths for keepassxc-cli. Only mailEntry holds custom data.
const (
	mailEntry  = "/Пошта <&> \"quoted\" 😀"
	workEntry  = "/Робота/Друге"
	thirdEntry = "/Кошик/У кошику"
)

// newCopy imports the fixture into a file named name in dir.
func newCopy(t *testing.T, dir, name, xml string) testkit.DB {
	t.Helper()
	return testkit.DB{Path: filepath.Join(dir, name), Password: testkit.Password}.Import(t, xml)
}

// copyOf copies the file of db to a file named name beside it.
func copyOf(t *testing.T, db testkit.DB, name string) testkit.DB {
	t.Helper()
	other := db
	other.Path = filepath.Join(filepath.Dir(db.Path), name)
	copyFile(t, db.Path, other.Path)
	return other
}

// passwords gives the lines for stdin that open n copies with the password of the fixtures.
func passwords(n int) string {
	return strings.Repeat(testkit.Password+"\n", n)
}

// saveAgain makes the real KeePassXC write the file with no edit in it: it sets the password
// the database already has. KeePassXC renews its service data, the salts and the seeds.
func saveAgain(t *testing.T, db testkit.DB) {
	t.Helper()
	before, err := os.ReadFile(db.Path)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := testkit.CLIPath()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli, "db-edit", "-q", "--set-password", db.Path)
	cmd.Stdin = strings.NewReader(strings.Repeat(db.Password+"\n", 3))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("keepassxc-cli db-edit: %v\n%s", err, out)
	}
	after, err := os.ReadFile(db.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("keepassxc-cli did not write the file")
	}
}

func TestStatusCountsAPlainSaveAsTheSame(t *testing.T) {
	dir := t.TempDir()
	a := newCopy(t, dir, "a.kdbx", testkit.RichXML())
	b := copyOf(t, a, "b.kdbx")
	saveAgain(t, b)

	got := testkit.Run(t, passwords(2), "status", a.Path, b.Path)

	wantCode(t, got, 0)
	wantUkrainian(t, got.Stdout, got.Stderr, a.Path, b.Path)
}

func wantLine(t *testing.T, output, want string) {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimRight(line, "\r") == want {
			return
		}
	}
	t.Errorf("no line %q in:\n%s", want, output)
}

func wantNoLine(t *testing.T, output, unwanted string) {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimRight(line, "\r") == unwanted {
			t.Errorf("the line %q should not be in:\n%s", unwanted, output)
		}
	}
}

func TestStatusNamesTheCopyThatLagsAndTheDirection(t *testing.T) {
	dir := t.TempDir()
	old := newCopy(t, dir, "old.kdbx", testkit.RichXML())
	edited := copyOf(t, old, "edited.kdbx")
	edited.CLI(t, "edit", "--notes", "нова нотатка", workEntry)

	for _, order := range [][]string{{old.Path, edited.Path}, {edited.Path, old.Path}} {
		got := testkit.Run(t, passwords(2), append([]string{"status"}, order...)...)

		wantCode(t, got, 1)
		wantLine(t, got.Stdout, "Відстає: "+old.Path)
		wantLine(t, got.Stdout, "Випереджає: "+edited.Path)
		wantLine(t, got.Stdout, "Напрямок копіювання: "+edited.Path+" -> "+old.Path)
		wantNoLine(t, got.Stdout, "Відстає: "+edited.Path)
		wantUkrainian(t, got.Stdout, got.Stderr, old.Path, edited.Path)
	}
}

// mergeCommand gives what the output of status offers as the next command, without the name
// of the program.
func mergeCommand(t *testing.T, stdout string) string {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(stdout, "\r", ""), "\n")
	i := slices.Index(lines, "Наступна команда:")
	if i < 0 || i+1 == len(lines) {
		t.Fatalf("no next command in:\n%s", stdout)
	}
	_, command, found := strings.Cut(lines[i+1], " merge ")
	if !found {
		t.Fatalf("the next command is not merge: %q", lines[i+1])
	}
	return command
}

// quoted gives a word with spaces as the shell of this system needs it.
func quoted(word string) string {
	if runtime.GOOS == "windows" {
		return `"` + word + `"`
	}
	return "'" + word + "'"
}

func TestStatusGivesTheMergeCommandForCopiesThatDiverged(t *testing.T) {
	for _, tc := range []struct {
		name     string
		inA, inB string
	}{
		{"changes in different entries", workEntry, thirdEntry},
		{"two changes of one entry", workEntry, workEntry},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := newCopy(t, dir, "a.kdbx", testkit.RichXML())
			b := copyOf(t, a, "b.kdbx")
			a.CLI(t, "edit", "--notes", "зміна в a", tc.inA)
			b.CLI(t, "edit", "--notes", "зміна в b", tc.inB)

			got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "status", "a.kdbx", "b.kdbx")

			wantCode(t, got, 2)
			if want, command := "a.kdbx b.kdbx --output a-merged.kdbx", mergeCommand(t, got.Stdout); command != want {
				t.Errorf("command %q, want %q", command, want)
			}
			wantUkrainian(t, got.Stdout, got.Stderr, "a.kdbx", "b.kdbx", "a-merged.kdbx", "merge", "--output", filepath.ToSlash(testkit.BinaryPath()))
		})
	}
}

func TestStatusMergeCommandKeepsWhatTheCommandLineSaid(t *testing.T) {
	dir := t.TempDir()
	const (
		first   = "копія один.kdbx"
		second  = "b.kdbx"
		keyName = "мій ключ.key"
	)
	key := testkit.NewKeyFile(t, filepath.Join(dir, keyName), 1)
	a := testkit.DB{Path: filepath.Join(dir, first), Password: testkit.Password, KeyFile: key}.Import(t, testkit.RichXML())
	b := testkit.DB{Path: filepath.Join(dir, second), Password: testkit.Password}.Import(t, testkit.RichXML())
	a.CLI(t, "edit", "--notes", "зміна в a", thirdEntry)
	b.CLI(t, "edit", "--notes", "зміна в b", workEntry)

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "status", first, "--key-file", keyName, second)

	wantCode(t, got, 2)
	want := quoted(first) + " --key-file " + quoted(keyName) + " " + second + " --output " + quoted("копія один-merged.kdbx")
	if command := mergeCommand(t, got.Stdout); command != want {
		t.Errorf("command %q, want %q", command, want)
	}
}

// The merge must not fail on a result that already exists, so the command names a free file.
func TestStatusMergeCommandNamesAFileThatDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	a := newCopy(t, dir, "a.kdbx", testkit.RichXML())
	b := copyOf(t, a, "b.kdbx")
	a.CLI(t, "edit", "--notes", "зміна в a", thirdEntry)
	b.CLI(t, "edit", "--notes", "зміна в b", workEntry)
	for _, taken := range []string{"a-merged.kdbx", "a-merged-2.kdbx"} {
		copyFile(t, a.Path, filepath.Join(dir, taken))
	}

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "status", "a.kdbx", "b.kdbx")

	wantCode(t, got, 2)
	if want, command := "a.kdbx b.kdbx --output a-merged-3.kdbx", mergeCommand(t, got.Stdout); command != want {
		t.Errorf("command %q, want %q", command, want)
	}
}

// After -- every word is a file, so --output must stand in front of it.
func TestStatusMergeCommandKeepsFilesThatLookLikeOptions(t *testing.T) {
	dir := t.TempDir()
	const name = "--a.kdbx"
	a := newCopy(t, dir, name, testkit.RichXML())
	b := copyOf(t, a, "b.kdbx")
	a.CLI(t, "edit", "--notes", "зміна в a", thirdEntry)
	b.CLI(t, "edit", "--notes", "зміна в b", workEntry)

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "status", "--", name, "b.kdbx")

	wantCode(t, got, 2)
	if want, command := "--output --a-merged.kdbx -- --a.kdbx b.kdbx", mergeCommand(t, got.Stdout); command != want {
		t.Errorf("command %q, want %q", command, want)
	}
}

// A real shell reads the command. Its arguments must reach the program as they came. The
// test runs them with compare, which names each file in its output.
func TestStatusMergeCommandSurvivesARealShell(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		shell = `C:\Program Files\Git\bin\sh.exe`
		if _, err := os.Stat(shell); err != nil {
			t.Skip("no sh on this system")
		}
	}
	dir := t.TempDir()
	const (
		first   = "копія один.kdbx"
		second  = "друга 'копія'.kdbx"
		keyName = "мій ключ.key"
	)
	key := testkit.NewKeyFile(t, filepath.Join(dir, keyName), 1)
	a := testkit.DB{Path: filepath.Join(dir, first), Password: testkit.Password, KeyFile: key}.Import(t, testkit.RichXML())
	b := testkit.DB{Path: filepath.Join(dir, second), Password: testkit.Password}.Import(t, testkit.RichXML())
	a.CLI(t, "edit", "--notes", "зміна в a", thirdEntry)
	b.CLI(t, "edit", "--notes", "зміна в b", workEntry)

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "status", first, "--key-file", keyName, second)
	wantCode(t, got, 2)

	lines := strings.Split(strings.ReplaceAll(got.Stdout, "\r", ""), "\n")
	line := strings.TrimSpace(lines[slices.Index(lines, "Наступна команда:")+1])
	line = strings.Replace(line, " merge ", " compare ", 1)
	line, _, _ = strings.Cut(line, " --output ")

	cmd := exec.Command(shell, "-c", line)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(passwords(2))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v\n%s", line, err, out)
	}
	passports(t, string(out), first, second)
}

// wantAhead checks the verdict that one copy holds all and the others lag.
func wantAhead(t *testing.T, got testkit.Result, ahead string, behind ...string) {
	t.Helper()
	wantCode(t, got, 1)
	wantLine(t, got.Stdout, "Випереджає: "+ahead)
	for _, path := range behind {
		wantLine(t, got.Stdout, "Відстає: "+path)
		wantLine(t, got.Stdout, "Напрямок копіювання: "+ahead+" -> "+path)
	}
}

// The file differs in each case, and no person has edited anything in it. Each case moves
// one value of the fixture.
func TestStatusCountsAsTheSameWhatChangesWithoutAnEdit(t *testing.T) {
	const later = "2026-10-01T10:00:00Z"
	dir := t.TempDir()
	original := newCopy(t, dir, "original.kdbx", testkit.RichXML())
	for _, tc := range []struct {
		name     string
		from, to string
	}{
		{"nothing, the file was only written twice", "", ""},
		{"an entry was read", "2026-04-02T10:00:00Z", later},
		{"an entry was used", "<UsageCount>7<", "<UsageCount>8<"},
		{"a group was read", "2026-04-01T10:00:02Z", later},
		{"an entry went away and came back", "2026-02-01T10:00:02Z", later},
		{"a group went away and came back", "2026-02-01T10:00:04Z", later},
		{"a group got a new date and the same fields", "2026-03-01T10:00:02Z", later},
		{"a group was opened in the window", "<IsExpanded>False<", "<IsExpanded>True<"},
		{"a group shows another top entry", "<LastTopVisibleEntry>AAAAAAAAAAAAAAAAAAAAAA==<", "<LastTopVisibleEntry>AAAAAAAAAAAAAAAAAAABAQ==<"},
		{"another group was selected in the window", "<LastSelectedGroup>AAAAAAAAAAAAAAAAAAAAIA==<", "<LastSelectedGroup>AAAAAAAAAAAAAAAAAAAAMA==<"},
		{"another group is at the top of the window", "<LastTopVisibleGroup>AAAAAAAAAAAAAAAAAAAAEA==<", "<LastTopVisibleGroup>AAAAAAAAAAAAAAAAAAAAIA==<"},
		{"the settings got a new date and the same values", "2026-02-06T10:00:00Z", later},
		{"the name got a new date and the same value", "2026-02-02T10:00:01Z", later},
		{"the master key got a new date", "2026-02-03T10:00:00Z", later},
		{"a custom icon got a new date", "2026-02-04T10:00:00Z", later},
		{"custom data of the database got a new date", "2026-02-07T10:00:00Z", later},
		{"custom data of a group got a new date", "2026-02-08T10:00:00Z", later},
		{"a record about a deletion got a new date", "2026-02-10T10:00:00Z", later},
		{"the tags of an entry have another spelling", "<Tags>alpha;бета;gamma<", "<Tags>gamma,бета;alpha<"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(testkit.RichXML(), tc.from) {
				t.Fatalf("no %s in the fixture", tc.from)
			}
			other := newCopy(t, t.TempDir(), "other.kdbx", strings.Replace(testkit.RichXML(), tc.from, tc.to, 1))

			wantCode(t, testkit.Run(t, passwords(2), "status", original.Path, other.Path), 0)
		})
	}
}

// metaItems gives the custom data of the database for PlainXML.
func metaItems(items ...string) string {
	var result strings.Builder
	for _, item := range items {
		key, value, _ := strings.Cut(item, "=")
		result.WriteString("<Item><Key>" + key + "</Key><Value>" + value + "</Value></Item>")
	}
	return strings.Replace(testkit.PlainXML, "</Meta>", "<CustomData>"+result.String()+"</CustomData></Meta>", 1)
}

func TestStatusIgnoresTheServiceDataKeePassXCRenewsOnEverySave(t *testing.T) {
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", metaItems("KPXC_RANDOM_SLUG=aaaa", "_LAST_MODIFIED=Mon Jan 1 2026", "own=value"))
	b := rawCopy(t, dir, "b.kdbx", metaItems("own=value", "_LAST_MODIFIED=Tue Feb 2 2026", "KPXC_RANDOM_SLUG=bbbb"))
	c := rawCopy(t, dir, "c.kdbx", metaItems("own=value"))

	wantCode(t, testkit.Run(t, passwords(3), "status", a.Path, b.Path, c.Path), 0)
}

func TestStatusIgnoresTheOrderOfCustomData(t *testing.T) {
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", metaItems("first=1", "second=2"))
	b := rawCopy(t, dir, "b.kdbx", metaItems("second=2", "first=1"))

	wantCode(t, testkit.Run(t, passwords(2), "status", a.Path, b.Path), 0)
}

// A merge takes all root groups for one group, so their UUIDs tell nothing.
func TestStatusTakesAllRootGroupsForOne(t *testing.T) {
	dir := t.TempDir()
	const rootID = "AAAAAAAAAAAAAAAAAAAAEA=="
	if !strings.Contains(testkit.PlainXML, rootID) {
		t.Fatalf("no %s in the fixture", rootID)
	}
	a := rawCopy(t, dir, "a.kdbx", testkit.PlainXML)
	b := rawCopy(t, dir, "b.kdbx", strings.Replace(testkit.PlainXML, rootID, "AAAAAAAAAAAAAAAAAAAAEQ==", 1))
	c := rawCopy(t, dir, "c.kdbx", strings.Replace(testkit.PlainXML, rootID, "AAAAAAAAAAAAAAAAAAAADw==", 1))

	wantCode(t, testkit.Run(t, passwords(3), "status", a.Path, b.Path, c.Path), 0)
}

// withGroups gives PlainXML with two more groups. The second one lies inside the first one
// or beside it, with the time of its last move.
func withGroups(nested bool, moved string) string {
	second := "<Group><UUID>AAAAAAAAAAAAAAAAAAADAQ==</UUID><Name>Друга</Name><Times><LocationChanged>" + moved + "</LocationChanged></Times></Group>"
	first := "<Group><UUID>AAAAAAAAAAAAAAAAAAADAA==</UUID><Name>Перша</Name><Times><LocationChanged>" + v1 + "</LocationChanged></Times>"
	if nested {
		first += second + "</Group>"
	} else {
		first += "</Group>" + second
	}
	return strings.Replace(testkit.PlainXML, "</Entry>", "</Entry>"+first, 1)
}

func TestStatusSeesAGroupThatMoved(t *testing.T) {
	dir := t.TempDir()
	old := rawCopy(t, dir, "old.kdbx", withGroups(false, v1))
	moved := rawCopy(t, dir, "moved.kdbx", withGroups(true, v2))

	wantAhead(t, testkit.Run(t, passwords(2), "status", old.Path, moved.Path), moved.Path, old.Path)
}

// withEntryInGroup gives PlainXML with one more group, and the entry in it or in the root
// group, with the time of its last move. A move alone makes no new version here.
func withEntryInGroup(inGroup bool, moved string) string {
	start := strings.Index(testkit.PlainXML, "<Entry>")
	end := strings.Index(testkit.PlainXML, "</Entry>") + len("</Entry>")
	entry := strings.Replace(testkit.PlainXML[start:end], "</Times>", "<LocationChanged>"+moved+"</LocationChanged></Times>", 1)
	group := "<Group><UUID>AAAAAAAAAAAAAAAAAAADAA==</UUID><Name>Перша</Name>"
	if inGroup {
		group += entry + "</Group>"
	} else {
		group = entry + group + "</Group>"
	}
	return testkit.PlainXML[:start] + group + testkit.PlainXML[end:]
}

func TestStatusSeesAnEntryThatMovedWithoutANewVersion(t *testing.T) {
	dir := t.TempDir()
	old := rawCopy(t, dir, "old.kdbx", withEntryInGroup(false, v1))
	moved := rawCopy(t, dir, "moved.kdbx", withEntryInGroup(true, v2))

	wantAhead(t, testkit.Run(t, passwords(2), "status", old.Path, moved.Path), moved.Path, old.Path)
}

// KeePassXC leaves the custom data of an entry out of the older version it makes on an edit.
// The Merge Engine covers custom data in its fingerprint, so it takes the main version of
// the older copy for a version the newer copy does not hold, and it reports a true conflict.
// Status follows the engine and says the copies diverged. That is safe, as nothing gets lost
// on the way to a merge, but the older copy only lags.
func TestStatusCallsAnEditOfAnEntryWithCustomDataALag(t *testing.T) {
	t.Skip("the Merge Engine sees a true conflict here: see the report of ticket #10")
	dir := t.TempDir()
	old := newCopy(t, dir, "old.kdbx", testkit.RichXML())
	edited := copyOf(t, old, "edited.kdbx")
	edited.CLI(t, "edit", "--notes", "нова нотатка", mailEntry)

	wantAhead(t, testkit.Run(t, passwords(2), "status", old.Path, edited.Path), edited.Path, old.Path)
}

func TestStatusSeesNoDifferenceBetweenFileFormats(t *testing.T) {
	dir := t.TempDir()
	three := testkit.RawFile{Major: 3, Minor: 1, XML: testkit.PlainXML}.Write(t, filepath.Join(dir, "three.kdbx"), testkit.Password)
	four := rawCopy(t, dir, "four.kdbx", testkit.PlainXML)

	wantCode(t, testkit.Run(t, passwords(2), "status", three.Path, four.Path), 0)
}

// Each case is a real change, so the changed copy must lead: a filter for the service data
// must not swallow it.
func TestStatusSeesAChangeInEveryPartOfTheDatabase(t *testing.T) {
	const later = "2026-10-01T10:00:00Z"
	settings := "2026-02-06T10:00:00Z"
	for _, tc := range []struct {
		name    string
		changes [][2]string
	}{
		{"the name of a group", [][2]string{{"<Name>Робота<", "<Name>Робота нова<"}, {"2026-03-01T10:00:02Z", later}}},
		{"the notes of a group", [][2]string{{"<Name>Робота</Name><Notes/>", "<Name>Робота</Name><Notes>нові</Notes>"}, {"2026-03-01T10:00:02Z", later}}},
		{"custom data of a group", [][2]string{{"<Value>group-value<", "<Value>new-value<"}, {"2026-03-01T10:00:01Z", later}}},
		{"the name of the database", [][2]string{{"<DatabaseName>Синтетична база<", "<DatabaseName>Нова назва<"}, {"2026-02-02T10:00:01Z", later}, {settings, later}}},
		{"custom data of the database", [][2]string{{"<Value>meta-custom-value<", "<Value>new-value<"}, {"2026-02-07T10:00:00Z", later}, {settings, later}}},
		{"a setting of the database", [][2]string{{"<HistoryMaxItems>25<", "<HistoryMaxItems>50<"}, {settings, later}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xml := testkit.RichXML()
			for _, change := range tc.changes {
				if !strings.Contains(xml, change[0]) {
					t.Fatalf("no %s in the fixture", change[0])
				}
				xml = strings.Replace(xml, change[0], change[1], 1)
			}
			dir := t.TempDir()
			old := newCopy(t, dir, "old.kdbx", testkit.RichXML())
			changed := newCopy(t, dir, "changed.kdbx", xml)

			wantAhead(t, testkit.Run(t, passwords(2), "status", old.Path, changed.Path), changed.Path, old.Path)
		})
	}
}

// These changes come from the real KeePassXC.
func TestStatusSeesWhatKeePassXCDoes(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func(t *testing.T, db testkit.DB)
	}{
		{"an entry moved to another group", func(t *testing.T, db testkit.DB) { db.CLI(t, "mv", workEntry, "/") }},
		{"a new entry", func(t *testing.T, db testkit.DB) {
			db.CLI(t, "add", "--username", "новий", "/Новий запис")
		}},
		{"a new group", func(t *testing.T, db testkit.DB) { db.Resave(t) }},
		{"a new attachment", func(t *testing.T, db testkit.DB) {
			file := filepath.Join(t.TempDir(), "new.bin")
			if err := os.WriteFile(file, []byte("new bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			db.CLI(t, "attachment-import", workEntry, "new.bin", file)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			old := newCopy(t, dir, "old.kdbx", testkit.RichXML())
			changed := copyOf(t, old, "changed.kdbx")
			tc.do(t, changed)

			wantAhead(t, testkit.Run(t, passwords(2), "status", old.Path, changed.Path), changed.Path, old.Path)
		})
	}
}

// rawCopy writes XML as a file, with no KDBX library and no KeePassXC in between.
func rawCopy(t *testing.T, dir, name, xml string) testkit.DB {
	t.Helper()
	return testkit.RawFile{Major: 4, Minor: 1, XML: xml}.Write(t, filepath.Join(dir, name), testkit.Password)
}

// withVersions gives PlainXML where the one entry has this main version and these older
// versions. A version is named by its time, and its notes tell it from the others.
func withVersions(main string, older ...string) string {
	const id = "<UUID>AAAAAAAAAAAAAAAAAAABAA==</UUID>"
	version := func(stamp string) string {
		return "<Times><LastModificationTime>" + stamp + "</LastModificationTime></Times>" +
			"<String><Key>Title</Key><Value>Запис</Value></String>" +
			"<String><Key>Notes</Key><Value>" + stamp + "</Value></String>"
	}
	var history strings.Builder
	for _, stamp := range older {
		history.WriteString("<Entry>" + id + version(stamp) + "</Entry>")
	}
	start, end := strings.Index(testkit.PlainXML, "<Entry>"), strings.Index(testkit.PlainXML, "</Entry>")
	return testkit.PlainXML[:start] + "<Entry>" + id + version(main) + "<History>" + history.String() + "</History>" + testkit.PlainXML[end:]
}

// The times of the versions, the oldest first.
const (
	v1 = "2026-01-01T10:00:00Z"
	v2 = "2026-01-05T10:00:00Z"
	v3 = "2026-01-10T10:00:00Z"
	v4 = "2026-01-15T10:00:00Z"
	v5 = "2026-01-20T10:00:00Z"
)

func TestStatusReadsTheVersionsOfAnEntry(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b string
		// ahead is "a" or "b" when that copy holds all, and "" when none does or both do.
		ahead string
		want  int
	}{
		{"the same versions", withVersions(v2, v1), withVersions(v2, v1), "", 0},
		{"the same versions in another order", withVersions(v3, v1, v2), withVersions(v3, v2, v1), "", 0},
		{"the main version is also in the history of the file", withVersions(v2, v1, v2), withVersions(v2, v1), "", 0},
		{"the main version of a copy is in the history of the other", withVersions(v1), withVersions(v2, v1), "b", 1},
		{"the main version is the same and the history is longer", withVersions(v3, v2), withVersions(v3, v1, v2), "b", 1},
		{"two new main versions", withVersions(v2, v1), withVersions(v3, v1), "", 2},
		{"two new main versions far apart", withVersions(v2, v1), withVersions(v5, v1), "", 2},
		// KeePassXC cut the history when it saved the newer copy, so that copy lacks old
		// passwords the other one has. A plain copy over the older file would lose them.
		{"the newer copy has lost old versions", withVersions(v4, v1, v2, v3), withVersions(v5, v3, v4), "", 2},
		// The clock of the copy with v2 was behind when it replaced v3: its history says v3 is
		// older, whatever the time says. It holds all that the other holds.
		{"the clock put the replaced version on top", withVersions(v2, v3), withVersions(v3), "a", 1},
		{"a merge by the clock against the copy that replaced the version", withVersions(v2, v3), withVersions(v3, v2), "a", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := rawCopy(t, dir, "a.kdbx", tc.a)
			b := rawCopy(t, dir, "b.kdbx", tc.b)

			got := testkit.Run(t, passwords(2), "status", a.Path, b.Path)

			switch tc.ahead {
			case "a":
				wantAhead(t, got, a.Path, b.Path)
			case "b":
				wantAhead(t, got, b.Path, a.Path)
			default:
				wantCode(t, got, tc.want)
			}
		})
	}
}

func TestStatusWithMoreThanTwoCopies(t *testing.T) {
	dir := t.TempDir()
	base := newCopy(t, dir, "base.kdbx", testkit.RichXML())

	one := copyOf(t, base, "one.kdbx")
	one.CLI(t, "edit", "--notes", "зміна один", workEntry)
	oneAgain := copyOf(t, one, "one-again.kdbx")
	saveAgain(t, oneAgain)
	oneThenMore := copyOf(t, one, "one-then-more.kdbx")
	oneThenMore.CLI(t, "edit", "--username", "ще зміна", thirdEntry)
	two := copyOf(t, base, "two.kdbx")
	two.CLI(t, "edit", "--notes", "зміна два", thirdEntry)
	three := copyOf(t, base, "three.kdbx")
	three.CLI(t, "add", "--username", "новий", "/Новий запис")

	// merged holds all of one and of three, while neither of those holds the other.
	merged := copyOf(t, one, "merged.kdbx")
	mergeWithKeePassXC(t, merged, three)

	permutations := func(paths ...string) [][]string {
		rotations := make([][]string, 0, len(paths))
		for i := range paths {
			rotations = append(rotations, slices.Concat(paths[i:], paths[:i]))
		}
		return rotations
	}
	for _, tc := range []struct {
		name  string
		paths []string
		want  int
		// ahead holds the copies that hold all, behind the others, when want is 1.
		ahead, behind []string
	}{
		{name: "all the same", paths: []string{one.Path, oneAgain.Path, copyOf(t, one, "one-copy.kdbx").Path}, want: 0},
		{
			name: "one copy lags behind two that are the same", paths: []string{base.Path, one.Path, oneAgain.Path}, want: 1,
			ahead: []string{one.Path, oneAgain.Path}, behind: []string{base.Path},
		},
		{
			name: "two copies lag in a chain", paths: []string{base.Path, one.Path, oneThenMore.Path}, want: 1,
			ahead: []string{oneThenMore.Path}, behind: []string{base.Path, one.Path},
		},
		{
			name: "two copies lag and differ from each other", paths: []string{one.Path, three.Path, merged.Path}, want: 1,
			ahead: []string{merged.Path}, behind: []string{one.Path, three.Path},
		},
		{name: "two diverged and one lags behind both", paths: []string{base.Path, one.Path, two.Path}, want: 2},
		{name: "three diverged", paths: []string{one.Path, two.Path, three.Path}, want: 2},
		{name: "a copy that holds all except one change of another", paths: []string{merged.Path, two.Path, base.Path}, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, paths := range permutations(tc.paths...) {
				got := testkit.Run(t, passwords(len(paths)), append([]string{"status"}, paths...)...)
				wantCode(t, got, tc.want)
				for _, path := range tc.ahead {
					wantLine(t, got.Stdout, "Випереджає: "+path)
				}
				for _, path := range tc.behind {
					wantLine(t, got.Stdout, "Відстає: "+path)
				}
				if tc.want == 2 {
					command := mergeCommand(t, got.Stdout)
					for _, path := range paths {
						if !strings.Contains(command, path) {
							t.Errorf("the command %q does not name %s", command, path)
						}
					}
				}
			}
		})
	}
}

// mergeWithKeePassXC merges from into db with the real keepassxc-cli.
func mergeWithKeePassXC(t *testing.T, db, from testkit.DB) {
	t.Helper()
	cli, err := testkit.CLIPath()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli, "merge", "-q", "--same-credentials", db.Path, from.Path)
	cmd.Stdin = strings.NewReader(db.Password + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("keepassxc-cli merge: %v\n%s", err, out)
	}
}

// withSecondEntry gives PlainXML with another entry under its own UUID. It has the same
// title as the first one, so the two look like one account after an import.
func withSecondEntry(id string) string {
	second := "<Entry><UUID>" + id + "</UUID><Times><LastModificationTime>2026-03-15T10:00:00Z</LastModificationTime></Times>" +
		"<String><Key>Title</Key><Value>Запис</Value></String></Entry>"
	return strings.Replace(testkit.PlainXML, "</Entry>", "</Entry>"+second, 1)
}

// The Merge Engine asks about similar entries even when the copies agree on them. A question
// alone must not make copies different: only one that the copies bring to each other does.
func TestStatusCountsSimilarEntriesOnlyWhenTheCopiesDifferInThem(t *testing.T) {
	const (
		firstID  = "AAAAAAAAAAAAAAAAAAACAA=="
		secondID = "AAAAAAAAAAAAAAAAAAACAQ=="
	)
	dir := t.TempDir()
	one := rawCopy(t, dir, "one.kdbx", testkit.PlainXML)
	pair := rawCopy(t, dir, "pair.kdbx", withSecondEntry(firstID))
	samePair := testkit.RawFile{Major: 3, Minor: 1, XML: withSecondEntry(firstID)}.Write(t, filepath.Join(dir, "same-pair.kdbx"), testkit.Password)
	otherPair := rawCopy(t, dir, "other-pair.kdbx", withSecondEntry(secondID))

	t.Run("both copies hold the same pair", func(t *testing.T) {
		wantCode(t, testkit.Run(t, passwords(2), "status", pair.Path, samePair.Path), 0)
	})
	t.Run("only one copy holds the second entry", func(t *testing.T) {
		wantAhead(t, testkit.Run(t, passwords(2), "status", one.Path, pair.Path), pair.Path, one.Path)
	})
	t.Run("each copy holds its own import of the entry", func(t *testing.T) {
		wantCode(t, testkit.Run(t, passwords(2), "status", pair.Path, otherPair.Path), 2)
	})
}

// A record about a deletion is data of the copy, with no entry left to ask about.
func TestStatusSeesARecordAboutADeletionOnlyOneCopyHolds(t *testing.T) {
	start := strings.Index(testkit.PlainXML, "<DeletedObjects>")
	end := strings.Index(testkit.PlainXML, "</DeletedObjects>") + len("</DeletedObjects>")
	dir := t.TempDir()
	withRecord := rawCopy(t, dir, "with.kdbx", testkit.PlainXML)
	without := rawCopy(t, dir, "without.kdbx", testkit.PlainXML[:start]+testkit.PlainXML[end:])

	wantAhead(t, testkit.Run(t, passwords(2), "status", withRecord.Path, without.Path), withRecord.Path, without.Path)
}

// A deletion goes through a question of the Merge Engine, whichever copy made it. The exit code
// is 2, because the merge has a question neither copy has alone.
func TestStatusSeesADeletion(t *testing.T) {
	noBin := strings.Replace(testkit.RichXML(), "<RecycleBinEnabled>True<", "<RecycleBinEnabled>False<", 1)
	for _, tc := range []struct {
		name string
		xml  string
	}{
		{"to the recycle bin", testkit.RichXML()},
		{"with a record about the deletion", noBin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			kept := newCopy(t, dir, "kept.kdbx", tc.xml)
			deleted := copyOf(t, kept, "deleted.kdbx")
			deleted.CLI(t, "rm", workEntry)

			wantCode(t, testkit.Run(t, passwords(2), "status", kept.Path, deleted.Path), 2)
		})
	}

	t.Run("the same deletion in both copies", func(t *testing.T) {
		dir := t.TempDir()
		a := newCopy(t, dir, "a.kdbx", noBin)
		a.CLI(t, "rm", workEntry)
		b := copyOf(t, a, "b.kdbx")
		saveAgain(t, b)

		wantCode(t, testkit.Run(t, passwords(2), "status", a.Path, b.Path), 0)
	})
}

// After an answer the result holds the record about the deletion and the memory of the answer, so
// a merge with the old copy brings nothing new and asks nothing: the old copy only lags.
func TestStatusCallsAnOldCopyBehindAResultWithAnAnswerAboutADeletion(t *testing.T) {
	const (
		second = "00000000000000000000000000000101"
		// The real deletions below have the time of this run, and an answer knows no later one.
		through = "2099-01-01T00:00:00Z"
		decided = "2026-10-01T10:00:00Z"
	)
	noBin := strings.Replace(testkit.RichXML(), "<RecycleBinEnabled>True<", "<RecycleBinEnabled>False<", 1)
	withAnswer := func(xml, kind string) string {
		item := "<Item><Key>cleanpass/" + kind + "/" + second + "/" + through + "</Key><Value>" + decided + "</Value></Item>"
		xml = strings.Replace(xml, "<Item><Key>meta-custom-key</Key>", item+"<Item><Key>meta-custom-key</Key>", 1)
		return strings.Replace(xml, "<DeletedObjects>", "<DeletedObjects><DeletedObject><UUID>AAAAAAAAAAAAAAAAAAABAQ==</UUID><DeletionTime>"+decided+"</DeletionTime></DeletedObject>", 1)
	}

	t.Run("the human kept the entry", func(t *testing.T) {
		dir := t.TempDir()
		result := newCopy(t, dir, "result.kdbx", withAnswer(noBin, "rejected"))
		deleted := newCopy(t, dir, "deleted.kdbx", noBin)
		deleted.CLI(t, "rm", workEntry)

		wantAhead(t, testkit.Run(t, passwords(2), "status", result.Path, deleted.Path), result.Path, deleted.Path)
	})
	t.Run("the human deleted the entry", func(t *testing.T) {
		dir := t.TempDir()
		result := newCopy(t, dir, "result.kdbx", withAnswer(noBin, "accepted"))
		result.CLI(t, "rm", workEntry)
		old := newCopy(t, dir, "old.kdbx", noBin)

		wantAhead(t, testkit.Run(t, passwords(2), "status", result.Path, old.Path), result.Path, old.Path)
	})
}

func TestStatusExplainsWhatItCannotDoWithCodeThree(t *testing.T) {
	dir := t.TempDir()
	good := newCopy(t, dir, "good.kdbx", testkit.RichXML())
	other := newCopy(t, dir, "other.kdbx", testkit.RichXML())
	missing := filepath.Join(dir, "немає.kdbx")
	notDatabase := filepath.Join(dir, "text.kdbx")
	if err := os.WriteFile(notDatabase, []byte("це не база"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{"no files", "", nil, "Не вказано жодного файла"},
		{"one file", passwords(1), []string{good.Path}, "щонайменше дві"},
		{"missing file", passwords(2), []string{good.Path, missing}, "Файл не знайдено: " + missing},
		{"not a database", passwords(2), []string{good.Path, notDatabase}, notDatabase + " не схожий на базу"},
		{"the same file that is not a database", "", []string{notDatabase, notDatabase}, notDatabase + " не схожий на базу"},
		{"unknown option", passwords(2), []string{good.Path, other.Path, "--verbose"}, "Невідома опція: --verbose"},
		{"no password on stdin", "", []string{good.Path, other.Path}, "не введено"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := testkit.Run(t, tc.stdin, append([]string{"status"}, tc.args...)...)
			wantCode(t, got, 3)
			if !strings.Contains(got.Stderr, tc.want) {
				t.Errorf("stderr has no %q:\n%s", tc.want, got.Stderr)
			}
			wantUkrainian(t, got.Stdout, got.Stderr, append(tc.args, "status")...)
		})
	}
}

func TestStatusAsksOnceForFilesWithTheSameBytes(t *testing.T) {
	dir := t.TempDir()
	old := newCopy(t, dir, "old.kdbx", testkit.RichXML())
	twin := copyOf(t, old, "twin.kdbx")
	edited := copyOf(t, old, "edited.kdbx")
	edited.CLI(t, "edit", "--notes", "нова нотатка", workEntry)

	got := testkit.Run(t, passwords(2), "status", old.Path, twin.Path, edited.Path)

	wantCode(t, got, 1)
	if count := strings.Count(got.Stderr, "Пароль для"); count != 2 {
		t.Errorf("%d questions about a password, want 2:\n%s", count, got.Stderr)
	}
	wantLine(t, got.Stdout, "Випереджає: "+edited.Path)
	wantLine(t, got.Stdout, "Відстає: "+old.Path)
	wantLine(t, got.Stdout, "Відстає: "+twin.Path)
	wantLine(t, got.Stdout, "Напрямок копіювання: "+edited.Path+" -> "+old.Path)
	wantLine(t, got.Stdout, "Напрямок копіювання: "+edited.Path+" -> "+twin.Path)
}

// The key file may stand beside either of two files with the same bytes.
func TestStatusTakesTheKeyFileOfAnyFileWithTheSameBytes(t *testing.T) {
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "a.key"), 1)
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password, KeyFile: key}.Import(t, testkit.RichXML())
	twin := copyOf(t, a, "twin.kdbx")
	other := newCopy(t, dir, "other.kdbx", testkit.RichXML())
	a.CLI(t, "ls") // the key file really is needed: keepassxc-cli opens the file with it

	got := testkit.Run(t, passwords(2), "status", a.Path, twin.Path, "--key-file", key, other.Path)

	wantCode(t, got, 0)
	if strings.Contains(got.Stderr, "Неправильний") {
		t.Errorf("the key file did not reach the file that opens first:\n%s", got.Stderr)
	}
}

func TestStatusReadsADatabaseWithAKeyFileAndADifferentPassword(t *testing.T) {
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "a.key"), 1)
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: "пароль першої", KeyFile: key}.Import(t, testkit.RichXML())
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: "пароль другої"}.Import(t, testkit.RichXML())

	got := testkit.Run(t, a.Password+"\n"+b.Password+"\n", "status", a.Path, "-k", key, b.Path)

	wantCode(t, got, 0)
}

func TestStatusShowsTheWarningOfTheFieldGuard(t *testing.T) {
	dir := t.TempDir()
	const name = "with-future.kdbx"
	xml := strings.Replace(testkit.PlainXML, "</Meta>", "<Future>SECRET-MARKER-value</Future></Meta>", 1)
	with := testkit.RawFile{Major: 4, Minor: 1, XML: xml}.Write(t, filepath.Join(dir, name), testkit.Password)
	without := testkit.RawFile{Major: 4, Minor: 1, XML: testkit.PlainXML}.Write(t, filepath.Join(dir, "plain.kdbx"), testkit.Password)

	got := testkit.Run(t, passwords(2), "status", with.Path, without.Path)

	// A warning is not an error: the verdict stays, with what it cannot see named.
	wantCode(t, got, 0)
	wantLine(t, got.Stdout, "Копія "+with.Path+":")
	wantNoLine(t, got.Stdout, "Копія "+without.Path+":")
	if !strings.Contains(got.Stdout, fieldLossWarning) || !strings.Contains(got.Stdout, "KeePassFile/Meta/Future (у файлі: 1, лишиться: 0)") {
		t.Errorf("no warning that names the path:\n%s", got.Stdout)
	}
	if !strings.Contains(got.Stdout, "Вердикт не враховує") {
		t.Errorf("no word that the verdict does not see this data:\n%s", got.Stdout)
	}
	if strings.Contains(got.Stdout+got.Stderr, "SECRET-MARKER") {
		t.Errorf("the output holds a value of the unknown field:\n%s\n%s", got.Stdout, got.Stderr)
	}
}

func TestStatusIsSilentAboutTheFieldGuardOnKeePassXCFiles(t *testing.T) {
	dir := t.TempDir()
	a := newCopy(t, dir, "a.kdbx", testkit.RichXML())
	b := copyOf(t, a, "b.kdbx")
	saveAgain(t, b)

	got := testkit.Run(t, passwords(2), "status", a.Path, b.Path)

	wantCode(t, got, 0)
	if strings.Contains(got.Stdout, fieldLossWarning) {
		t.Errorf("false alarm:\n%s", got.Stdout)
	}
}

// Each verdict runs with an empty PATH, so no other program can start, and with its own
// folder for temporary files. The test waits for the end of the process and of everything
// that holds its output, so a process left behind would hang it.
func TestStatusChangesNothingOnDiskAndNeedsNoOtherProgram(t *testing.T) {
	inputs := filepath.Join(t.TempDir(), "Мої бази")
	work, temp := t.TempDir(), t.TempDir()
	old := newCopy(t, inputs, "стара копія.kdbx", testkit.RichXML())
	twin := copyOf(t, old, "та сама.kdbx")
	ahead := copyOf(t, old, "нова копія.kdbx")
	ahead.CLI(t, "edit", "--notes", "нова нотатка", workEntry)
	other := copyOf(t, old, "інша копія.kdbx")
	other.CLI(t, "edit", "--notes", "інша нотатка", thirdEntry)

	for _, tc := range []struct {
		name  string
		paths []string
		want  int
	}{
		{"the same bytes", []string{old.Path, twin.Path}, 0},
		{"a copy lags", []string{old.Path, ahead.Path}, 1},
		{"the copies diverged", []string{ahead.Path, other.Path}, 2},
		{"an error", []string{old.Path, filepath.Join(inputs, "немає.kdbx")}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := []map[string]string{diskState(t, inputs), diskState(t, work), diskState(t, temp)}

			got := testkit.RunWith(t, testkit.Options{
				Stdin: passwords(len(tc.paths)),
				Dir:   work,
				Env:   []string{"PATH=" + t.TempDir(), "TMP=" + temp, "TEMP=" + temp, "TMPDIR=" + temp},
			}, append([]string{"status"}, tc.paths...)...)

			wantCode(t, got, tc.want)
			for i, after := range []map[string]string{diskState(t, inputs), diskState(t, work), diskState(t, temp)} {
				wantSameDisk(t, before[i], after)
			}
		})
	}
}

func TestStatusKeepsSecretsOutOfTheOutput(t *testing.T) {
	dir := t.TempDir()
	password := "SECRET-MARKER-майстер-пароль"
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: password}.Import(t, testkit.RichXML())
	b := copyOf(t, a, "b.kdbx")
	a.CLI(t, "edit", "--notes", "зміна в a", thirdEntry)
	b.CLI(t, "edit", "--notes", "зміна в b", workEntry)

	got := testkit.Run(t, "неправильний-SECRET-MARKER\n"+password+"\n\n\n", "status", a.Path, b.Path)

	wantCode(t, got, 2)
	if strings.Contains(got.Stdout+got.Stderr, "SECRET-MARKER") {
		t.Errorf("the output holds a secret:\n%s\n%s", got.Stdout, got.Stderr)
	}
}

func TestHelpNamesStatusAndItsExitCodes(t *testing.T) {
	got := testkit.Run(t, "", "--help")
	if !strings.Contains(got.Stdout, "cleanpass status") {
		t.Errorf("no status in the help:\n%s", got.Stdout)
	}
	if !strings.Contains(got.Stdout, "2 розійшлись") {
		t.Errorf("no exit codes in the help:\n%s", got.Stdout)
	}
}

func TestStatusAnswersWithoutAPasswordForFilesWithTheSameBytes(t *testing.T) {
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	b := filepath.Join(dir, "b.kdbx")
	c := filepath.Join(dir, "c.kdbx")
	copyFile(t, a.Path, b)
	copyFile(t, a.Path, c)

	// Stdin is empty, so a question about a password would end in an error.
	got := testkit.Run(t, "", "status", a.Path, b, c)

	wantCode(t, got, 0)
	if strings.Contains(got.Stderr, "Пароль для") {
		t.Errorf("asks for a password:\n%s", got.Stderr)
	}
	if !strings.Contains(got.Stdout, "побайтно") {
		t.Errorf("does not say the bytes are the same:\n%s", got.Stdout)
	}
	wantUkrainian(t, got.Stdout, got.Stderr)
}

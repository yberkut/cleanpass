package main_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/testkit"
)

const timeLayout = "2006-01-02 15:04:05 -07:00"

func localTime(t *testing.T, rfc3339 string) string {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Local().Format(timeLayout)
}

// passports splits the output of compare into the blocks of each copy, by path.
func passports(t *testing.T, stdout string, paths ...string) map[string]string {
	t.Helper()
	blocks := map[string]string{}
	rest := stdout
	for i, path := range paths {
		header := fmt.Sprintf("Копія %d: %s", i+1, path)
		start := strings.Index(rest, header)
		if start < 0 {
			t.Fatalf("no line %q in output:\n%s", header, stdout)
		}
		block := rest[start:]
		if i+1 < len(paths) {
			next := strings.Index(block, fmt.Sprintf("Копія %d: ", i+2))
			if next < 0 {
				t.Fatalf("no block for copy %d in output:\n%s", i+2, stdout)
			}
			block, rest = block[:next], block[next:]
		}
		blocks[path] = block
	}
	return blocks
}

// field returns the value after "label:" in a passport block.
func field(t *testing.T, block, label string) string {
	t.Helper()
	for line := range strings.SplitSeq(block, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), label+":"); ok {
			return strings.TrimSpace(value)
		}
	}
	t.Fatalf("no field %q in block:\n%s", label, block)
	return ""
}

func wantField(t *testing.T, block, label, want string) {
	t.Helper()
	if got := field(t, block, label); got != want {
		t.Errorf("%s = %q, want %q\nblock:\n%s", label, got, want, block)
	}
}

func isFreshest(block string) bool {
	header, _, _ := strings.Cut(block, "\n")
	return strings.Contains(header, "найсвіжіша")
}

func TestComparePassportOfEachCopy(t *testing.T) {
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML()).Resave(t)

	got := testkit.Run(t, testkit.Password+"\n"+testkit.Password+"\n", "compare", a.Path, b.Path)
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
	}

	blocks := passports(t, got.Stdout, a.Path, b.Path)
	wantGroups := map[string]string{a.Path: "3", b.Path: "4"}
	for _, db := range []testkit.DB{a, b} {
		block := blocks[db.Path]
		info, err := os.Stat(db.Path)
		if err != nil {
			t.Fatal(err)
		}
		wantField(t, block, "Розмір файлу (байт)", strconv.FormatInt(info.Size(), 10))
		wantField(t, block, "Дата файлу", info.ModTime().Format(timeLayout))
		wantField(t, block, "Записів", "3")
		wantField(t, block, "Груп", wantGroups[db.Path])
		wantField(t, block, "Вкладень", "2")
		wantField(t, block, "Записів про видалення", "1")
		wantField(t, block, "Формат", "KDBX 4.1")
		wantField(t, block, "Шифр", "AES-256")
		if kdf := field(t, block, "KDF"); !strings.HasPrefix(kdf, "AES-KDF") {
			t.Errorf("KDF = %q, want AES-KDF", kdf)
		}
	}
	wantField(t, blocks[a.Path], "Остання зміна в базі", localTime(t, testkit.RichLastChange))
	if resaved := field(t, blocks[b.Path], "Остання зміна в базі"); resaved <= localTime(t, testkit.RichLastChange) {
		t.Errorf("resaved copy shows last change %q, want later than the fixture", resaved)
	}
	if isFreshest(blocks[a.Path]) {
		t.Errorf("copy a is marked freshest:\n%s", blocks[a.Path])
	}
	if !isFreshest(blocks[b.Path]) {
		t.Errorf("copy b is not marked freshest:\n%s", blocks[b.Path])
	}
	wantUkrainian(t, got.Stdout, got.Stderr, a.Path, b.Path)
}

func TestCompareMarksFreshestByTheChangeInsideNotByTheFileDate(t *testing.T) {
	dir := t.TempDir()
	newerInside := strings.Replace(testkit.RichXML(), testkit.RichLastChange, "2026-05-01T10:00:00Z", 1)
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, newerInside)

	// The older copy has the newer file date, as after a plain copy to a USB stick.
	if err := os.Chtimes(b.Path, time.Time{}, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	got := testkit.Run(t, testkit.Password+"\n\n", "compare", a.Path, b.Path)
	blocks := passports(t, got.Stdout, a.Path, b.Path)
	if isFreshest(blocks[a.Path]) || !isFreshest(blocks[b.Path]) {
		t.Errorf("want only copy b marked:\n%s", got.Stdout)
	}
	wantField(t, blocks[b.Path], "Остання зміна в базі", localTime(t, "2026-05-01T10:00:00Z"))
}

func TestCompareMarksAllCopiesWithTheSameNewestChange(t *testing.T) {
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	b := filepath.Join(dir, "b.kdbx")
	copyFile(t, a.Path, b)

	got := testkit.Run(t, testkit.Password+"\n\n", "compare", a.Path, b)
	blocks := passports(t, got.Stdout, a.Path, b)
	if !isFreshest(blocks[a.Path]) || !isFreshest(blocks[b]) {
		t.Errorf("want both copies marked:\n%s", got.Stdout)
	}
}

// Each case moves one time stamp of the fixture and looks at what the report calls the
// last change inside the database.
func TestCompareLastChangeInsideTheDatabase(t *testing.T) {
	const later = "2026-09-01T10:00:00Z"
	for _, tc := range []struct {
		name  string
		stamp string
		count bool
	}{
		{"main version changed", "2026-03-15T10:00:00Z", true},
		{"entry moved, for example to the recycle bin", "2026-02-01T10:00:02Z", true},
		{"older version newer than the main one, after a clock error", "2026-02-20T10:00:00Z", true},
		{"group changed", "2026-03-01T10:00:02Z", true},
		{"group moved", "2026-02-01T10:00:04Z", true},
		{"record about a deletion", "2026-02-10T10:00:00Z", true},
		{"database name changed", "2026-02-02T10:00:01Z", true},
		{"master key changed", "2026-02-03T10:00:00Z", true},
		{"custom icon changed", "2026-02-04T10:00:00Z", true},
		{"settings changed", "2026-02-06T10:00:00Z", true},
		{"custom data of the database changed", "2026-02-07T10:00:00Z", true},
		{"entry only read", "2026-04-02T10:00:00Z", false},
		{"expiry date in the future", "2030-01-01T10:00:02Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(testkit.RichXML(), tc.stamp) {
				t.Fatalf("no time stamp %s in the fixture", tc.stamp)
			}
			xml := strings.ReplaceAll(testkit.RichXML(), tc.stamp, later)
			db := testkit.DB{Path: filepath.Join(t.TempDir(), "a.kdbx"), Password: testkit.Password}.Import(t, xml)

			got := testkit.Run(t, testkit.Password+"\n", "compare", db.Path)
			want := testkit.RichLastChange
			if tc.count {
				want = later
			}
			wantField(t, passports(t, got.Stdout, db.Path)[db.Path], "Остання зміна в базі", localTime(t, want))
		})
	}
}

func TestCompareOpensEveryCipherAndKDF(t *testing.T) {
	base := testkit.DB{Path: filepath.Join(t.TempDir(), "base.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	argon := func(name string) string {
		return name + " (пам'ять, МіБ: 1; ітерацій: 2; потоків: 1)"
	}
	for _, cipher := range []struct {
		name string
		id   testkit.Cipher
	}{
		{"AES-256", testkit.AES256},
		{"ChaCha20", testkit.ChaCha20},
		{"Twofish", testkit.Twofish},
	} {
		for _, kdf := range []struct {
			name string
			kdf  testkit.KDF
			want string
		}{
			{"AES-KDF", testkit.KDFAES, "AES-KDF (раундів: 6000)"},
			{"Argon2d", testkit.KDFArgon2d, argon("Argon2d")},
			{"Argon2id", testkit.KDFArgon2id, argon("Argon2id")},
		} {
			t.Run(cipher.name+" "+kdf.name, func(t *testing.T) {
				db := base.WithEncryption(t, filepath.Join(t.TempDir(), "copy.kdbx"), cipher.id, kdf.kdf)

				got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
				if got.Code != 0 {
					t.Fatalf("exit code %d\nstderr:\n%s", got.Code, got.Stderr)
				}
				if strings.Contains(got.Stderr, "Неправильний") {
					t.Errorf("false message about the password:\n%s", got.Stderr)
				}
				wantNoFieldLossWarning(t, got)
				block := passports(t, got.Stdout, db.Path)[db.Path]
				wantField(t, block, "Шифр", cipher.name)
				wantField(t, block, "KDF", kdf.want)
				wantField(t, block, "Записів", "3")
				wantField(t, block, "Груп", "4") // the fixture and the group of Resave
			})
		}
	}
}

func TestCompareReadsKDBX3(t *testing.T) {
	db := testkit.NewKDBX3(t, filepath.Join(t.TempDir(), "old.kdbx"), testkit.Password)
	db.CLI(t, "ls") // the real KeePassXC reads this file too

	got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstderr:\n%s", got.Code, got.Stderr)
	}
	wantNoFieldLossWarning(t, got)
	block := passports(t, got.Stdout, db.Path)[db.Path]
	wantField(t, block, "Формат", "KDBX 3.1")
	wantField(t, block, "Шифр", "AES-256")
	wantField(t, block, "KDF", "AES-KDF (раундів: 6000)")
	wantField(t, block, "Записів", "1")
	wantField(t, block, "Вкладень", "1")
}

func TestCompareChangesNothingOnDiskAndTakesAnyPath(t *testing.T) {
	inputs := filepath.Join(t.TempDir(), "Мої бази з пробілами")
	work := t.TempDir()
	keyFile := testkit.NewKeyFile(t, filepath.Join(inputs, "мій ключ.key"), 1)
	a := testkit.DB{Path: filepath.Join(inputs, "копія один.kdbx"), Password: "пароль з пробілами", KeyFile: keyFile}.Import(t, testkit.RichXML())
	b := testkit.DB{Path: filepath.Join(inputs, "копія два.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	inputsBefore, workBefore := diskState(t, inputs), diskState(t, work)

	got := testkit.RunWith(t, testkit.Options{
		Stdin: a.Password + "\n" + b.Password + "\n",
		Dir:   work,
	}, "compare", a.Path, "--key-file", keyFile, b.Path)
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
	}

	passports(t, got.Stdout, a.Path, b.Path)
	wantSameDisk(t, inputsBefore, diskState(t, inputs))
	wantSameDisk(t, workBefore, diskState(t, work))
}

func TestDoubleDashMakesTheRestFiles(t *testing.T) {
	dir := t.TempDir()
	const name = "--key-file.kdbx"
	testkit.DB{Path: filepath.Join(dir, name), Password: testkit.Password}.Import(t, testkit.RichXML())

	got := testkit.RunWith(t, testkit.Options{Stdin: testkit.Password + "\n", Dir: dir}, "compare", "--", name)
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstderr:\n%s", got.Code, got.Stderr)
	}
	passports(t, got.Stdout, name)
}

func TestCompareKeepsSecretsOutOfTheOutput(t *testing.T) {
	password := "SECRET-MARKER-майстер-пароль"
	db := testkit.DB{Path: filepath.Join(t.TempDir(), "a.kdbx"), Password: password}.Import(t, testkit.RichXML())

	got := testkit.Run(t, "неправильний-SECRET-MARKER\n"+password+"\n\n", "compare", db.Path)
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstderr:\n%s", got.Code, got.Stderr)
	}
	for name, output := range map[string]string{"stdout": got.Stdout, "stderr": got.Stderr} {
		if strings.Contains(output, "SECRET-MARKER") {
			t.Errorf("%s holds a secret:\n%s", name, output)
		}
	}
}

func TestCompareExplainsAFileItCannotUse(t *testing.T) {
	dir := t.TempDir()
	good := testkit.DB{Path: filepath.Join(dir, "good.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	content, err := os.ReadFile(good.Path)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	flipped := bytes.Clone(content)
	flipped[len(flipped)/2] ^= 0xFF
	badHeader := bytes.Clone(content)
	badHeader[40] ^= 0xFF

	missing := filepath.Join(dir, "немає.kdbx")
	notDatabase := write("text.kdbx", []byte("це не база"))
	empty := write("empty.kdbx", nil)
	truncated := write("truncated.kdbx", content[:len(content)/2])
	damagedBody := write("body.kdbx", flipped)
	damagedHeader := write("header.kdbx", badHeader)

	for _, tc := range []struct {
		name string
		args []string
		// asksPassword is false when the tool must see the problem before any question.
		asksPassword bool
		want         string
	}{
		{"missing file", []string{missing}, false, "Файл не знайдено: " + missing},
		{"second file missing", []string{good.Path, missing}, false, "Файл не знайдено: " + missing},
		{"not a database", []string{notDatabase}, false, notDatabase + " не схожий на базу"},
		{"empty file", []string{empty}, false, empty + " не схожий на базу"},
		{"truncated file", []string{truncated}, true, "Не вдалося прочитати базу " + truncated},
		{"damaged body", []string{damagedBody}, true, "Не вдалося прочитати базу " + damagedBody},
		{"damaged header", []string{damagedHeader}, true, "Не вдалося прочитати базу " + damagedHeader},
		{"key file missing", []string{good.Path, "--key-file", missing}, false, "Не вдалося прочитати файл-ключ " + missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := testkit.Run(t, testkit.Password+"\n", append([]string{"compare"}, tc.args...)...)
			if got.Code != 3 {
				t.Errorf("exit code %d, want 3\nstderr:\n%s", got.Code, got.Stderr)
			}
			if !strings.Contains(got.Stderr, tc.want) {
				t.Errorf("stderr has no %q:\n%s", tc.want, got.Stderr)
			}
			if strings.Contains(got.Stderr, "Пароль для") != tc.asksPassword {
				t.Errorf("asks for a password: want %v\nstderr:\n%s", tc.asksPassword, got.Stderr)
			}
			if strings.Contains(got.Stderr, "Неправильний") {
				t.Errorf("a damaged file is reported as a wrong password:\n%s", got.Stderr)
			}
			wantUkrainian(t, got.Stdout, got.Stderr, tc.args...)
		})
	}
}

func TestCommandLineMistakesAreExplained(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no arguments", nil, "Використання"},
		{"unknown command", []string{"sync", "a.kdbx"}, "Невідома команда: sync"},
		{"compare without files", []string{"compare"}, "Не вказано жодного файла"},
		{"option before any file", []string{"compare", "--key-file", "k.key", "a.kdbx"}, "--key-file"},
		{"option without value", []string{"compare", "a.kdbx", "--key-file"}, "--key-file"},
		{"unknown option", []string{"compare", "a.kdbx", "--verbose"}, "Невідома опція: --verbose"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := testkit.Run(t, "", tc.args...)
			if got.Code != 3 {
				t.Errorf("exit code %d, want 3", got.Code)
			}
			if !strings.Contains(got.Stderr, tc.want) {
				t.Errorf("stderr has no %q:\n%s", tc.want, got.Stderr)
			}
			wantUkrainian(t, got.Stdout, got.Stderr)
		})
	}
}

func TestHelpIsInUkrainian(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		got := testkit.Run(t, "", flag)
		if got.Code != 0 {
			t.Errorf("%s: exit code %d", flag, got.Code)
		}
		if !strings.Contains(got.Stdout, "cleanpass compare") {
			t.Errorf("%s: no usage:\n%s", flag, got.Stdout)
		}
		wantUkrainian(t, got.Stdout, got.Stderr)
	}
}

// wantUkrainian checks that text for the human holds no English sentences. It removes
// the given paths and the names that stay in Latin letters in any language first.
func wantUkrainian(t *testing.T, stdout, stderr string, paths ...string) {
	t.Helper()
	allowed := map[string]bool{
		"cleanpass": true, "compare": true, "status": true, "file": true, "help": true, "key": true,
		"KDBX": true, "KDF": true, "AES": true, "ChaCha": true, "Twofish": true, "Argon": true,
		"Enter": true, "stdin": true, "KeePass": true, "KeePassXC": true, "verbose": true, "sync": true,
		"merge": true, "output": true, "allow": true, "unrelated": true,
		"UUID": true, "URL": true, "Ctrl": true,
		"Git": true, "Bash": true, "mintty": true, "winpty": true, "Windows": true, "Terminal": true,
	}
	for _, output := range []string{stdout, stderr} {
		output = commandLine.ReplaceAllString(output, "")
		for _, path := range paths {
			output = strings.ReplaceAll(output, path, "")
		}
		for _, word := range latinWord.FindAllString(output, -1) {
			if !allowed[word] {
				t.Errorf("word %q is not Ukrainian in:\n%s", word, output)
			}
		}
		if strings.TrimSpace(output) != "" && !cyrillic.MatchString(output) {
			t.Errorf("no Ukrainian text in:\n%s", output)
		}
	}
}

var (
	latinWord = regexp.MustCompile(`[A-Za-z]{2,}`)
	cyrillic  = regexp.MustCompile(`\p{Cyrillic}`)
	// commandLine is the command a report offers: it holds paths and options, not text.
	commandLine = regexp.MustCompile(`(?m)^  .* merge .*$`)
)

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	content, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// diskState maps every path below dir to its modification time and SHA-256.
func diskState(t *testing.T, dir string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state[path] = info.ModTime().String()
		if !entry.IsDir() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state[path] += fmt.Sprintf(" %x", sha256.Sum256(content))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func wantSameDisk(t *testing.T, before, after map[string]string) {
	t.Helper()
	if !maps.Equal(before, after) {
		t.Errorf("the disk changed\nbefore: %v\nafter: %v", before, after)
	}
}

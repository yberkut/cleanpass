package main_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

// newCopies imports the fixture once per password, into files named by their index.
func newCopies(t *testing.T, passwords ...string) []testkit.DB {
	t.Helper()
	dir := t.TempDir()
	copies := make([]testkit.DB, 0, len(passwords))
	for i, password := range passwords {
		path := filepath.Join(dir, string(rune('a'+i))+".kdbx")
		copies = append(copies, testkit.DB{Path: path, Password: password}.Import(t, testkit.RichXML()))
	}
	return copies
}

func compareArgs(copies []testkit.DB) []string {
	args := []string{"compare"}
	for _, c := range copies {
		args = append(args, c.Path)
	}
	return args
}

// wantOpened checks that the command worked and shows a passport for each copy.
func wantOpened(t *testing.T, got testkit.Result, copies []testkit.DB) {
	t.Helper()
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
	}
	paths := make([]string, 0, len(copies))
	for _, c := range copies {
		paths = append(paths, c.Path)
	}
	blocks := passports(t, got.Stdout, paths...)
	for _, path := range paths {
		wantField(t, blocks[path], "Записів", "3")
	}
}

func TestEachCopyHasItsOwnPassword(t *testing.T) {
	copies := newCopies(t, "пароль-один", "пароль-два")
	got := testkit.Run(t, "пароль-один\nпароль-два\n", compareArgs(copies)...)
	wantOpened(t, got, copies)
}

func TestEnterRepeatsTheTypedPassword(t *testing.T) {
	copies := newCopies(t, "спільний пароль", "спільний пароль", "спільний пароль")
	got := testkit.Run(t, "спільний пароль\n\n\n", compareArgs(copies)...)
	wantOpened(t, got, copies)
	if strings.Contains(got.Stderr, "Неправильний") {
		t.Errorf("unexpected retry:\n%s", got.Stderr)
	}
}

func TestLinesWithWindowsEndingsAndNoLastEndingWork(t *testing.T) {
	copies := newCopies(t, "пароль-один", "пароль-два")
	got := testkit.Run(t, "пароль-один\r\nпароль-два", compareArgs(copies)...)
	wantOpened(t, got, copies)
}

func TestWrongPasswordGivesAClearErrorAndANewTry(t *testing.T) {
	copies := newCopies(t, "правильний")
	// Wrong password; then the right one and Enter at the question about the key file.
	got := testkit.Run(t, "хибний\nправильний\n\n", compareArgs(copies)...)
	wantOpened(t, got, copies)
	if !strings.Contains(got.Stderr, "Неправильний пароль або файл-ключ для "+copies[0].Path) {
		t.Errorf("no clear error:\n%s", got.Stderr)
	}
	wantUkrainian(t, got.Stdout, got.Stderr, copies[0].Path)
}

func TestEnterRepeatsTheLastPasswordAfterARetry(t *testing.T) {
	copies := newCopies(t, "перший", "другий", "другий")
	// a: first password. b: Enter repeats it and fails, then the right one, then Enter
	// at the question about the key file. c: Enter repeats the right one.
	got := testkit.Run(t, "перший\n\nдругий\n\n\n", compareArgs(copies)...)
	wantOpened(t, got, copies)
}

func TestStdinEndsBeforeAPasswordCame(t *testing.T) {
	copies := newCopies(t, "перший", "другий")
	for _, tc := range []struct {
		name  string
		stdin string
	}{
		{"no input at all", ""},
		{"input for the first copy only", "перший\n"},
		{"retries end without a right password", "перший\nхибний\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := testkit.Run(t, tc.stdin, compareArgs(copies)...)
			if got.Code != 3 {
				t.Errorf("exit code %d, want 3", got.Code)
			}
			if !strings.Contains(got.Stderr, "не введено") {
				t.Errorf("no clear error:\n%s", got.Stderr)
			}
			if got.Stdout != "" {
				t.Errorf("no report is possible, stdout:\n%s", got.Stdout)
			}
		})
	}
}

func TestKeyFileIsSetForEachCopySeparately(t *testing.T) {
	dir := t.TempDir()
	keyA := testkit.NewKeyFile(t, filepath.Join(dir, "a.key"), 1)
	keyB := testkit.NewKeyFile(t, filepath.Join(dir, "b.key"), 2)
	copies := []testkit.DB{
		testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password, KeyFile: keyA}.Import(t, testkit.RichXML()),
		testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password, KeyFile: keyB}.Import(t, testkit.RichXML()),
		testkit.DB{Path: filepath.Join(dir, "c.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML()),
	}
	for name, args := range map[string][]string{
		"long option":                  {"compare", copies[0].Path, "--key-file", keyA, copies[1].Path, "--key-file", keyB, copies[2].Path},
		"short option and equals sign": {"compare", copies[0].Path, "-k", keyA, copies[1].Path, "--key-file=" + keyB, copies[2].Path},
	} {
		t.Run(name, func(t *testing.T) {
			got := testkit.Run(t, testkit.Password+"\n\n\n", args...)
			wantOpened(t, got, copies)
		})
	}
}

func TestKeyFileCanBeGivenByAnswerToAQuestion(t *testing.T) {
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "ключ.key"), 1)
	copies := []testkit.DB{testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password, KeyFile: key}.Import(t, testkit.RichXML())}

	// The first try has no key file and fails. The retry asks for the password and the key.
	got := testkit.Run(t, testkit.Password+"\n\n"+key+"\n", compareArgs(copies)...)
	wantOpened(t, got, copies)
	if !strings.Contains(got.Stderr, "Файл-ключ для") {
		t.Errorf("no question about the key file:\n%s", got.Stderr)
	}
}

func TestMissingKeyFileInAnAnswerGivesANewTry(t *testing.T) {
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "ключ.key"), 1)
	copies := []testkit.DB{testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password, KeyFile: key}.Import(t, testkit.RichXML())}
	missing := filepath.Join(dir, "немає.key")

	stdin := testkit.Password + "\n\n" + missing + "\n\n" + key + "\n"
	got := testkit.Run(t, stdin, compareArgs(copies)...)
	wantOpened(t, got, copies)
	if !strings.Contains(got.Stderr, "Не вдалося прочитати файл-ключ "+missing) {
		t.Errorf("no clear error:\n%s", got.Stderr)
	}
}

func TestWrongKeyFileOptionCanBeCorrectedByAnAnswer(t *testing.T) {
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "right.key"), 1)
	wrong := testkit.NewKeyFile(t, filepath.Join(dir, "wrong.key"), 2)
	db := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password, KeyFile: key}.Import(t, testkit.RichXML())

	got := testkit.Run(t, testkit.Password+"\n\n"+key+"\n", "compare", db.Path, "--key-file", wrong)
	wantOpened(t, got, []testkit.DB{db})
}

func TestDatabaseWithOnlyAKeyFileOpensWithEnter(t *testing.T) {
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "k.key"), 1)
	db := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), KeyFile: key}.Import(t, testkit.RichXML())
	db.CLI(t, "ls") // the real KeePassXC opens it without a password

	got := testkit.Run(t, "\n", "compare", db.Path, "--key-file", key)
	wantOpened(t, got, []testkit.DB{db})
}

func TestEnterAlsoOpensACopyWithoutAPasswordAfterAPasswordWasTyped(t *testing.T) {
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "k.key"), 1)
	withPassword := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	keyOnly := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), KeyFile: key}.Import(t, testkit.RichXML())

	got := testkit.Run(t, testkit.Password+"\n\n", "compare", withPassword.Path, keyOnly.Path, "--key-file", key)
	wantOpened(t, got, []testkit.DB{withPassword, keyOnly})
}

func TestEnterOpensADatabaseWithAnEmptyPassword(t *testing.T) {
	db := testkit.ImportWithEmptyPassword(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.RichXML())

	got := testkit.Run(t, "\n", "compare", db.Path)
	wantOpened(t, got, []testkit.DB{db})
}

func TestMasterPasswordDoesNotComeFromArgumentsOrEnvironment(t *testing.T) {
	const password = "SECRET-MARKER-майстер-пароль"
	db := testkit.DB{Path: filepath.Join(t.TempDir(), "a.kdbx"), Password: password}.Import(t, testkit.RichXML())

	t.Run("arguments", func(t *testing.T) {
		for _, args := range [][]string{
			{"compare", db.Path, "--password=" + password},
			{"compare", db.Path, "--password", password},
			{"compare", db.Path, "-p", password},
		} {
			got := testkit.Run(t, "", args...)
			if got.Code != 3 {
				t.Errorf("%v: exit code %d, want 3", args[2:], got.Code)
			}
			if strings.Contains(got.Stdout+got.Stderr, "SECRET-MARKER") {
				t.Errorf("%v: the value is in the output:\n%s", args[2:], got.Stderr)
			}
		}
	})

	t.Run("environment", func(t *testing.T) {
		got := testkit.RunWith(t, testkit.Options{Env: []string{
			"CLEANPASS_PASSWORD=" + password,
			"KEEPASS_PASSWORD=" + password,
			"KEEPASSXC_PASSWORD=" + password,
			"PASSWORD=" + password,
		}}, "compare", db.Path)
		if got.Code != 3 {
			t.Errorf("exit code %d, want 3: the database must stay closed", got.Code)
		}
		if strings.Contains(got.Stdout, "Записів") {
			t.Errorf("the database opened:\n%s", got.Stdout)
		}
	})
}

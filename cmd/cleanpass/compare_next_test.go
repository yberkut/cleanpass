package main_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

const nextCommand = "Наступна команда:"

// outputLines gives the lines of the output without the empty ones at its end.
func outputLines(stdout string) []string {
	return strings.Split(strings.TrimRight(strings.ReplaceAll(stdout, "\r", ""), "\n"), "\n")
}

// divergedCopies gives two copies where each holds a change the other lacks.
func divergedCopies(t *testing.T, dir string) (a, b testkit.DB) {
	t.Helper()
	a = newCopy(t, dir, "a.kdbx", testkit.RichXML())
	b = copyOf(t, a, "b.kdbx")
	a.CLI(t, "edit", "--notes", "зміна в a", thirdEntry)
	b.CLI(t, "edit", "--notes", "зміна в b", workEntry)
	return a, b
}

func TestCompareEndsWithTheNextCommand(t *testing.T) {
	dir := t.TempDir()
	divergedCopies(t, dir)

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "compare", "a.kdbx", "b.kdbx")

	wantCode(t, got, 0)
	lines := outputLines(got.Stdout)
	if at := len(lines) - 2; at < 0 || lines[at] != nextCommand {
		t.Fatalf("the report does not end with the next command:\n%s", got.Stdout)
	}
	if want, command := "a.kdbx b.kdbx --output a-merged.kdbx", mergeCommand(t, got.Stdout); command != want {
		t.Errorf("command %q, want %q", command, want)
	}
	wantLine(t, got.Stdout, "Копії розійшлись: жодна з них не містить усього, що є в інших.")
	wantUkrainian(t, got.Stdout, got.Stderr, "a.kdbx", "b.kdbx", "u2", "b.example")
}

func TestCompareNextCommandKeepsWhatTheCommandLineSaid(t *testing.T) {
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

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "compare", first, "--key-file", keyName, second)

	wantCode(t, got, 0)
	want := quoted(first) + " --key-file " + quoted(keyName) + " " + second + " --output " + quoted("копія один-merged.kdbx")
	if command := mergeCommand(t, got.Stdout); command != want {
		t.Errorf("command %q, want %q", command, want)
	}
}

func TestCompareNextCommandNamesAFileThatDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	a, _ := divergedCopies(t, dir)
	copyFile(t, a.Path, filepath.Join(dir, "a-merged.kdbx"))

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "compare", "a.kdbx", "b.kdbx")

	if want, command := "a.kdbx b.kdbx --output a-merged-2.kdbx", mergeCommand(t, got.Stdout); command != want {
		t.Errorf("command %q, want %q", command, want)
	}
}

func TestCompareNextCommandKeepsFilesThatLookLikeOptions(t *testing.T) {
	dir := t.TempDir()
	a := newCopy(t, dir, "--a.kdbx", testkit.RichXML())
	b := copyOf(t, a, "b.kdbx")
	a.CLI(t, "edit", "--notes", "зміна в a", thirdEntry)
	b.CLI(t, "edit", "--notes", "зміна в b", workEntry)

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "compare", "--", "--a.kdbx", "b.kdbx")

	if want, command := "--output --a-merged.kdbx -- --a.kdbx b.kdbx", mergeCommand(t, got.Stdout); command != want {
		t.Errorf("command %q, want %q", command, want)
	}
}

// inShell runs one line in a real shell, as a person who copies the line does.
func inShell(t *testing.T, dir, line, stdin string) (stdout, stderr string, code int) {
	t.Helper()
	shell, err := exec.LookPath("sh")
	if err != nil {
		shell = `C:\Program Files\Git\bin\sh.exe`
		if _, err := os.Stat(shell); err != nil {
			t.Skip("no sh on this system")
		}
	}
	cmd := exec.Command(shell, "-c", line)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, problems bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &problems
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatalf("%s: %v", line, err)
	}
	return out.String(), problems.String(), code
}

// theCommandLine gives the whole line the report offers.
func theCommandLine(t *testing.T, stdout string) string {
	t.Helper()
	lines := outputLines(stdout)
	at := slices.Index(lines, nextCommand)
	if at < 0 || at+1 == len(lines) {
		t.Fatalf("no next command in:\n%s", stdout)
	}
	return strings.TrimSpace(lines[at+1])
}

// keyedCopies gives copies whose names and key file need quotes in any shell. The first copy gets
// its edit last: in the same second the first one on the command line counts as the newest too,
// so the result of a merge always opens with its keys.
func keyedCopies(t *testing.T, dir string) (first, second, keyName string) {
	t.Helper()
	first, second, keyName = "копія один.kdbx", "друга 'копія'.kdbx", "мій ключ.key"
	key := testkit.NewKeyFile(t, filepath.Join(dir, keyName), 1)
	a := testkit.DB{Path: filepath.Join(dir, first), Password: testkit.Password, KeyFile: key}.Import(t, testkit.RichXML())
	b := testkit.DB{Path: filepath.Join(dir, second), Password: testkit.Password}.Import(t, testkit.RichXML())
	b.CLI(t, "edit", "--notes", "зміна в b", workEntry)
	a.CLI(t, "edit", "--notes", "зміна в a", thirdEntry)
	return first, second, keyName
}

// The arguments must reach the program as they came. Compare names each file in its output, so
// it stands in for merge here, on any tip of the integration branch.
func TestCompareNextCommandSurvivesARealShell(t *testing.T) {
	dir := t.TempDir()
	first, second, keyName := keyedCopies(t, dir)

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "compare", first, "--key-file", keyName, second)
	wantCode(t, got, 0)

	line := strings.Replace(theCommandLine(t, got.Stdout), " merge ", " compare ", 1)
	line, _, _ = strings.Cut(line, " --output ")
	stdout, stderr, code := inShell(t, dir, line, passwords(2))
	if code != 0 {
		t.Fatalf("%s: exit code %d\n%s", line, code, stderr)
	}
	passports(t, stdout, first, second)
}

// skipWithoutMerge leaves a test out until the command it needs has landed.
func skipWithoutMerge(t *testing.T) {
	t.Helper()
	if strings.Contains(testkit.Run(t, "", "merge").Stderr, "Невідома команда") {
		t.Skip("the merge command has not landed yet")
	}
}

// The real command runs here as a person runs it, from the text of the report.
func TestCompareNextCommandRunsAsTheRealMerge(t *testing.T) {
	skipWithoutMerge(t)
	dir := t.TempDir()
	first, second, keyName := keyedCopies(t, dir)

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "compare", first, "--key-file", keyName, second)
	wantCode(t, got, 0)

	line := theCommandLine(t, got.Stdout)
	if _, stderr, code := inShell(t, dir, line, passwords(2)); code != 0 {
		t.Fatalf("%s: exit code %d\n%s", line, code, stderr)
	}
	result := testkit.DB{Path: filepath.Join(dir, "копія один-merged.kdbx"), Password: testkit.Password, KeyFile: filepath.Join(dir, keyName)}
	if listing := result.CLI(t, "ls", "-R", "-f"); !strings.Contains(listing, "Друге") {
		t.Errorf("the result of the command does not hold the entries:\n%s", listing)
	}
}

func TestCompareSaysNoCommandIsNeededForTheSameContent(t *testing.T) {
	dir := t.TempDir()
	a := newCopy(t, dir, "a.kdbx", testkit.RichXML())
	b := copyOf(t, a, "b.kdbx")
	saveAgain(t, b)

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	wantLine(t, got.Stdout, "Копії однакові: записи, групи й налаштування збігаються. Файли різняться лише службовими даними або шифруванням.")
	wantLine(t, got.Stdout, "Наступна команда не потрібна.")
	wantNoLine(t, got.Stdout, nextCommand)
}

func TestCompareNamesTheCopyThatLagsAndStillOffersTheCommand(t *testing.T) {
	dir := t.TempDir()
	old := newCopy(t, dir, "old.kdbx", testkit.RichXML())
	edited := copyOf(t, old, "edited.kdbx")
	edited.CLI(t, "edit", "--notes", "нова нотатка", workEntry)

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "compare", "old.kdbx", "edited.kdbx")

	wantCode(t, got, 0)
	wantLine(t, got.Stdout, "Випереджає: edited.kdbx")
	wantLine(t, got.Stdout, "Відстає: old.kdbx")
	wantLine(t, got.Stdout, "Напрямок копіювання: edited.kdbx -> old.kdbx")
	if want, command := "old.kdbx edited.kdbx --output old-merged.kdbx", mergeCommand(t, got.Stdout); command != want {
		t.Errorf("command %q, want %q", command, want)
	}
}

func TestCompareOfOneCopyHasNoNextCommand(t *testing.T) {
	db := newCopy(t, t.TempDir(), "a.kdbx", testkit.RichXML())

	got := testkit.Run(t, passwords(1), "compare", db.Path)

	wantCode(t, got, 0)
	wantLine(t, got.Stdout, "Порівнювати нема з чим: вказано одну копію. Наступна команда не потрібна.")
	wantNoLine(t, got.Stdout, nextCommand)
}

// Merge stops on such a copy with no way around, so a command would not work when copied.
func TestCompareOffersNoCommandThatWouldStopOnUnknownData(t *testing.T) {
	dir := t.TempDir()
	unknown := testkit.UnknownFields(t, filepath.Join(dir, "unknown.kdbx"))
	other := newCopy(t, dir, "other.kdbx", testkit.RichXML())

	got := testkit.Run(t, passwords(2), "compare", unknown.Path, other.Path)

	wantCode(t, got, 0)
	wantNoLine(t, got.Stdout, nextCommand)
	wantLine(t, got.Stdout, "Наступної команди немає: злиття цих копій зупиниться на даних, яких тула не вміє зберегти, і обходу цієї зупинки немає.")
}

// The difference shows in no entry here, so the report must not leave the verdict without a word.
func TestCompareExplainsADifferenceOutsideTheMainVersions(t *testing.T) {
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withVersions(v3, v1, v2))
	b := rawCopy(t, dir, "b.kdbx", withVersions(v3, v2))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	wantLine(t, got.Stdout, "Головні версії записів збігаються. Копії різняться історією записів, групами або налаштуваннями бази.")
	wantLine(t, got.Stdout, "Випереджає: "+a.Path)
}

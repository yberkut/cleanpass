package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

// A person runs scripts/manual-check.sh in a terminal with a real key. These tests run what they
// can of it: the parts with no person and no real data, with the fake ykman.

const syntheticOnly = "CLEANPASS_MANUAL_CHECK_SYNTHETIC_ONLY=1"

func bashPath(t *testing.T) string {
	t.Helper()
	// On Windows, the bash on PATH can be the one of WSL.
	if runtime.GOOS == "windows" {
		shell := `C:\Program Files\Git\bin\bash.exe`
		if _, err := os.Stat(shell); err != nil {
			t.Skip("no Git Bash on this system")
		}
		return shell
	}
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash on this system")
	}
	return shell
}

func manualCheckScript(t *testing.T) string {
	t.Helper()
	return filepath.ToSlash(filepath.Join(repoRoot(t), "scripts", "manual-check.sh"))
}

// runManualCheck runs the script on the built cleanpass with lines on stdin. Stdin is a pipe here,
// as it is for any program a model starts.
func runManualCheck(t *testing.T, env []string, stdin string, args ...string) testkit.Result {
	t.Helper()
	shell := bashPath(t)
	script := manualCheckScript(t)
	cmd := exec.Command(shell, append([]string{script}, args...)...)
	// The directory of the script is the temporary directory of the test, so that the test can
	// see what stays. The last value of a repeated variable wins.
	cmd.Env = append(append(os.Environ(), "TMPDIR="+filepath.ToSlash(t.TempDir())), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	result := testkit.Result{}
	if err := cmd.Run(); err != nil {
		exit, isExit := err.(*exec.ExitError)
		if !isExit {
			t.Fatal(err)
		}
		result.Code = exit.ExitCode()
	}
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	return result
}

// pathWith puts a directory in front of PATH: the script needs the programs of the system.
func pathWith(dir string) string {
	return "PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

func TestManualCheckIsValidBash(t *testing.T) {
	t.Parallel()
	check := exec.Command(bashPath(t), "-n", manualCheckScript(t))
	if out, err := check.CombinedOutput(); err != nil {
		t.Errorf("bash -n: %v\n%s", err, out)
	}
}

// The script ships in the release and says it sends nothing anywhere. So it holds no code that
// could keep a value in a file or send it to GitHub, not even code nobody calls, and no text for the
// person in another language than the tool's.
func TestManualCheckHoldsNothingThatCouldStoreOrSendAValue(t *testing.T) {
	t.Parallel()
	script, err := os.ReadFile(manualCheckScript(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"gh secret", "gh variable", ".env", "ENV_FILE",
		"Press Enter", "[y/N]", "Stage ", "Setup complete", "opening", "Ready to start", "Enter keeps current",
		"krok",
	} {
		if bytes.Contains(script, []byte(text)) {
			t.Errorf("the script holds %q", text)
		}
	}
}

// confirm is the one yes-or-no gate of the script, before real bases. A person may answer in
// Ukrainian, and anything else is no.
func TestManualCheckTakesAYesInUkrainian(t *testing.T) {
	t.Parallel()
	for answer, want := range map[string]bool{"так": true, "Т": true, "y": true, "Yes": true, "ні": false, "н": false, "n": false, "": false, "і": false} {
		gate := `eval "$(sed -n '/^confirm() {/,/^}/p' "$1")"; confirm питання`
		cmd := exec.Command(bashPath(t), "-c", gate, "bash", manualCheckScript(t))
		cmd.Stdin = strings.NewReader(answer + "\n")
		err := cmd.Run()
		if _, isExit := err.(*exec.ExitError); err != nil && !isExit {
			t.Fatal(err)
		}
		if got := err == nil; got != want {
			t.Errorf("the answer %q gives yes = %v, want %v", answer, got, want)
		}
	}
}

// A program that a model starts has no terminal. The way to the master passwords stays closed.
func TestManualCheckRefusesToRunWithoutATerminal(t *testing.T) {
	t.Parallel()
	got := runManualCheck(t, nil, "\n\n\n", testkit.BinaryPath())

	wantCode(t, got, 2)
	wantInOutput(t, got.Stderr, "власному терміналі")
	wantNotInOutput(t, got.Stdout, "Підсумок")
}

func TestManualCheckExplainsItself(t *testing.T) {
	t.Parallel()
	got := runManualCheck(t, nil, "", "--help")

	wantCode(t, got, 0)
	wantInOutput(t, got.Stdout, "нікуди нічого не надсилає", "не читає паролів")
}

func TestManualCheckWithoutAKeyLeavesTheKeyStepsOpen(t *testing.T) {
	t.Parallel()
	got := runManualCheck(t, []string{syntheticOnly}, "\n\n", testkit.BinaryPath())

	wantCode(t, got, 0)
	wantInOutput(t, got.Stdout, "Підсумок", "Вивід ykman: один рядок із 40 hex-символів: не перевірено", "cleanpass відкрив базу з YubiKey із KeePassXC: не перевірено", "Злиття справжніх баз: не перевірено")
}

func TestManualCheckAcceptsTheFormOfYkmanTheToolReads(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

	got := runManualCheck(t, append(fake.PlanEnv(), syntheticOnly, pathWith(fake.Dir)), "\n\n2\n\n\n", testkit.BinaryPath())

	wantCode(t, got, 0)
	wantInOutput(t, got.Stdout, "Вивід ykman: один рядок із 40 hex-символів: так")
	if response := regexp.MustCompile(`[0-9a-fA-F]{40}`).FindString(got.Stdout + got.Stderr); response != "" {
		t.Errorf("the response of the key is on the screen: %s", response)
	}
	calls := fake.Calls(t)
	if len(calls) != 2 {
		t.Fatalf("%d calls of ykman, want one with a touch and one without", len(calls))
	}
	if words := calls[0].Args; len(words) != 4 || words[0] != "otp" || words[1] != "calculate" || words[2] != "2" || len(words[3]) != 128 {
		t.Errorf("the call of ykman: %q, want the form the tool uses", words)
	}
}

func TestManualCheckCallsAnotherFormOfYkmanAFailure(t *testing.T) {
	t.Parallel()
	for name, output := range map[string]string{
		"another text":   "Touch your YubiKey\n",
		"too short":      "abcdef\n",
		"not one line":   strings.Repeat("ab", 20) + "\n" + strings.Repeat("cd", 20) + "\n",
		"not hex":        strings.Repeat("zz", 20) + "\n",
		"nothing at all": "",
	} {
		t.Run(name, func(t *testing.T) {
			fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{Output: &output})

			got := runManualCheck(t, append(fake.PlanEnv(), syntheticOnly, pathWith(fake.Dir)), "\n\n2\n\n\n", testkit.BinaryPath())

			wantCode(t, got, 0)
			wantInOutput(t, got.Stdout, "Вивід ykman: один рядок із 40 hex-символів: НІ")
			if printed := strings.TrimSpace(output); printed != "" {
				wantNotInOutput(t, got.Stdout, printed)
			}
		})
	}
}

func TestManualCheckNotesAKeyThatGaveNoAnswer(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{FailCalls: []int{1, 2}})

	got := runManualCheck(t, append(fake.PlanEnv(), syntheticOnly, pathWith(fake.Dir)), "\n\n2\n\n\n", testkit.BinaryPath())

	wantCode(t, got, 0)
	wantInOutput(t, got.Stdout, "Вивід ykman: один рядок із 40 hex-символів: НІ (код 1)", "Вікно дотику ykman:")
}

// The person can send the summary to anyone, so it must hold nothing about the data.
func TestManualCheckSummaryHoldsNoPathAndNoPassword(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

	got := runManualCheck(t, append(fake.PlanEnv(), syntheticOnly, pathWith(fake.Dir)), "\n\n2\n\n\n", testkit.BinaryPath())

	at := strings.LastIndex(got.Stdout, "Підсумок")
	if at < 0 {
		t.Fatalf("no summary:\n%s", got.Stdout)
	}
	summary := got.Stdout[at:]
	for _, word := range []string{"cleanpass-synthetic-check", ".kdbx", `:\`, "/tmp", "Temp"} {
		if strings.Contains(summary, word) {
			t.Errorf("the summary holds %q:\n%s", word, summary)
		}
	}
}

// The step with the window of KeePassXC looks for these words in the report. Here keepassxc-cli does
// what the person does in that window.
func TestManualCheckLooksForWhatCompareReallySays(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := testkit.DB{Path: filepath.Join(dir, "base.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	edited := copyOf(t, base, "edited.kdbx")
	edited.CLI(t, "edit", "--notes", "нова нотатка", mailEntry)
	edited.CLI(t, "rm", workEntry)

	got := testkit.Run(t, passwordLines(2), "compare", base.Path, edited.Path)

	wantCode(t, got, 0)
	script, err := os.ReadFile(manualCheckScript(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, words := range []string{"Копія відстає", "Видалення:"} {
		wantInOutput(t, string(script), `grep -q "`+words+`"`)
		wantInOutput(t, got.Stdout, words)
	}
	wantInOutput(t, string(script), `grep -q "Версії сперечаються"`)
	wantNotInOutput(t, got.Stdout, "Версії сперечаються")
}

func TestManualCheckLeavesNoFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	got := runManualCheck(t, []string{syntheticOnly, "TMPDIR=" + filepath.ToSlash(dir)}, "\n\n", testkit.BinaryPath())

	wantCode(t, got, 0)
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range left {
		t.Errorf("the script left %s behind", entry.Name())
	}
}

// In a real mintty the tool refuses. A pipe of a test is no such terminal, so here the tool goes on
// and works, and the check has to say that this is no refusal.
func TestManualCheckCallsAToolThatDoesNotRefuseAFailure(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("mintty is a terminal of Git Bash on Windows")
	}

	got := runManualCheck(t, []string{syntheticOnly, "TERM_PROGRAM=mintty"}, "cleanpass-synthetic-check\n\n", testkit.BinaryPath())

	wantCode(t, got, 0)
	wantInOutput(t, got.Stdout, "Відмова в mintty за 2 с: НІ")
}

func TestManualCheckNeedsNoMinttyOnOtherTerminals(t *testing.T) {
	t.Parallel()
	got := runManualCheck(t, []string{syntheticOnly, "TERM_PROGRAM="}, "\n\n", testkit.BinaryPath())

	wantCode(t, got, 0)
	if runtime.GOOS == "windows" {
		wantInOutput(t, got.Stdout, "Відмова в mintty за 2 с: не перевірено")
	} else {
		wantInOutput(t, got.Stdout, "Відмова в mintty за 2 с: не стосується цієї системи")
	}
}

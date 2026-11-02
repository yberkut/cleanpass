package main_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/yberkut/cleanpass/internal/safewriter"
	"github.com/yberkut/cleanpass/internal/testkit"
)

var (
	createNamedPipe  = kernel32.NewProc("CreateNamedPipeW")
	getConsoleMode   = kernel32.NewProc("GetConsoleMode")
	connectNamedPipe = kernel32.NewProc("ConnectNamedPipe")
)

const (
	pipeAccessOutbound = 0x00000002
	pipeBuffer         = 64 * 1024
	enableEchoInput    = 0x0004
	exitInterrupted    = 130
	ctrlC              = "\x03"
)

// runWithNamedStdin runs the tool with the read end of a named pipe of this name on its stdin. That
// is all a program sees of the terminal of Git Bash, so the same name stands in for it here.
func runWithNamedStdin(t *testing.T, name, lines string, args ...string) testkit.Result {
	t.Helper()
	path := `\\.\pipe\` + name
	wide, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, _, callErr := createNamedPipe.Call(uintptr(unsafe.Pointer(wide)), pipeAccessOutbound, 0, 1, pipeBuffer, 0, 0, 0)
	if syscall.Handle(handle) == syscall.InvalidHandle {
		t.Fatalf("CreateNamedPipe: %v", callErr)
	}
	writer := os.NewFile(handle, path)
	defer writer.Close()
	reader, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	// The client came first, so this only gives an error that says so.
	_, _, _ = connectNamedPipe.Call(handle, 0)
	// The writing end stays open while the tool runs, as a terminal keeps it: a pipe that lost that
	// end no longer tells its name.
	if _, err := writer.WriteString(lines); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(testkit.BinaryPath(), args...)
	cmd.Stdin = reader
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exit, isExit := err.(*exec.ExitError)
		if !isExit {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return testkit.Result{Stdout: stdout.String(), Stderr: stderr.String(), Code: code}
}

func uniqueWord(t *testing.T) string {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(random[:])
}

func TestATerminalThatWouldShowThePasswordIsRefused(t *testing.T) {
	db := rawCopy(t, t.TempDir(), "a.kdbx", testkit.PlainXML)
	for _, form := range []string{
		"msys-%s-pty0-from-master",
		"msys-%s-pty12-from-master",
		"cygwin-%s-pty0-from-master",
		"cygwin-%s-pty3-from-master-nat",
	} {
		t.Run(form, func(t *testing.T) {
			got := runWithNamedStdin(t, fmt.Sprintf(form, uniqueWord(t)), passwords(1), "compare", db.Path)

			wantCode(t, got, 3)
			wantInOutput(t, got.Stderr, "winpty", "Windows Terminal")
			wantNotInOutput(t, got.Stderr, "Пароль для")
			wantNotInOutput(t, got.Stdout, "Записів")
			wantUkrainian(t, got.Stdout, got.Stderr)
		})
	}
}

// Windows can block a question about a synchronous pipe while someone reads from the same pipe, and
// behind a terminal someone may. The tool must not hang there without a word.
func TestATerminalPipeThatSomeoneReadsDoesNotHangTheTool(t *testing.T) {
	db := rawCopy(t, t.TempDir(), "a.kdbx", testkit.PlainXML)
	path := `\\.\pipe\msys-` + uniqueWord(t) + "-pty0-from-master"
	wide, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, _, callErr := createNamedPipe.Call(uintptr(unsafe.Pointer(wide)), pipeAccessOutbound, 0, 1, pipeBuffer, 0, 0, 0)
	if syscall.Handle(handle) == syscall.InvalidHandle {
		t.Fatalf("CreateNamedPipe: %v", callErr)
	}
	writer := os.NewFile(handle, path)
	defer writer.Close()
	reader, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	// This read waits on the same pipe object the tool gets as its stdin. Ending the test ends it.
	go func() {
		var one [1]byte
		_, _ = reader.Read(one[:])
	}()
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, testkit.BinaryPath(), "compare", db.Path)
	cmd.Stdin = reader
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("the tool hangs:\n%s", stderr.String())
	}
	if exit, isExit := err.(*exec.ExitError); !isExit || exit.ExitCode() != 3 {
		t.Errorf("exit: %v\n%s", err, stderr.String())
	}
	wantInOutput(t, stderr.String(), "winpty")
	// The read above ends with this byte, so the handles can close.
	_, _ = writer.WriteString("\n")
}

// A script that pipes passwords must go on working, also when its pipe has a name of the same family.
func TestAPipeOfAScriptIsNotRefused(t *testing.T) {
	db := rawCopy(t, t.TempDir(), "a.kdbx", testkit.PlainXML)
	for _, form := range []string{
		"msys-%s-pipe-nt-0x12",
		"cygwin-%s-pipe-nt-0x12",
		"cleanpass-%s",
		"pty0-from-master-%s",
	} {
		t.Run(form, func(t *testing.T) {
			got := runWithNamedStdin(t, fmt.Sprintf(form, uniqueWord(t)), passwords(1), "compare", db.Path)

			wantCode(t, got, 0)
			wantInOutput(t, got.Stdout, "Записів")
		})
	}
}

// The pipe of a real shell from Git for Windows, as a person writes such a script.
func TestAPipeOfARealShellIsNotRefused(t *testing.T) {
	dir := t.TempDir()
	rawCopy(t, dir, "a.kdbx", testkit.PlainXML)
	line := "printf '%s\\n' '" + testkit.Password + "' | '" + filepath.ToSlash(testkit.BinaryPath()) + "' compare a.kdbx"

	stdout, stderr, code := inShell(t, dir, line, "")

	if code != 0 {
		t.Fatalf("%s: exit code %d\n%s", line, code, stderr)
	}
	wantInOutput(t, stdout, "Записів")
}

// A command that needs no password has nothing to hide, so it works in such a terminal too.
func TestSuchATerminalIsNoProblemWithoutAPassword(t *testing.T) {
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", testkit.PlainXML)
	b := copyOf(t, a, "b.kdbx")

	got := runWithNamedStdin(t, "msys-"+uniqueWord(t)+"-pty0-from-master", "", "status", a.Path, b.Path)

	wantCode(t, got, 0)
}

// probeVariable holds the program that the test binary runs in place of its tests. See consoleProbe.
const probeVariable = "CLEANPASS_TEST_CONSOLE_PROBE"

func init() {
	if program := os.Getenv(probeVariable); program != "" {
		os.Exit(consoleProbe(program, os.Args[1:]))
	}
}

// consoleProbe runs a program in its own console and watches the mode of that console from outside.
// So a test sees what a shell finds after the program has ended, and that a test of the echo has
// teeth: the echo goes off first.
func consoleProbe(program string, args []string) int {
	// Ctrl+C reaches every process in a console, and this one has to outlive the program.
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	echo := func() string {
		var mode uint32
		ok, _, _ := getConsoleMode.Call(os.Stdin.Fd(), uintptr(unsafe.Pointer(&mode)))
		switch {
		case ok == 0:
			return "unknown"
		case mode&enableEchoInput != 0:
			return "on"
		}
		return "off"
	}
	cmd := exec.Command(program, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Println("PROBE-FAILED", err)
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	last := ""
	for {
		select {
		case <-done:
			fmt.Printf("PROBE-EXIT=%d\n", cmd.ProcessState.ExitCode())
			fmt.Printf("PROBE-LAST-ECHO=%s\n", echo())
			return 0
		case <-time.After(5 * time.Millisecond):
			if now := echo(); now != last {
				fmt.Printf("PROBE-ECHO=%s\n", now)
				last = now
			}
		}
	}
}

func (c *console) press(keys string) {
	c.t.Helper()
	var written uint32
	if err := syscall.WriteFile(c.input, []byte(keys), &written, nil); err != nil {
		c.t.Fatal(err)
	}
}

func TestCtrlCAtAPasswordPromptGivesTheEchoBack(t *testing.T) {
	db := rawCopy(t, t.TempDir(), "a.kdbx", testkit.PlainXML)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(probeVariable, testkit.BinaryPath())

	c := startInConsole(t, self, "compare", db.Path)

	c.waitFor("Пароль для", 1)
	c.waitFor("PROBE-ECHO=off", 1)
	c.press(ctrlC)
	c.waitFor(fmt.Sprintf("PROBE-EXIT=%d", exitInterrupted), 1)
	c.waitFor("PROBE-LAST-ECHO=on", 1)
	if !strings.Contains(c.screen(), "Перервано") {
		t.Errorf("no word about the interrupt:\n%q", c.screen())
	}
}

// startMergeInConsole runs a merge in a console and types the passwords.
func startMergeInConsole(t *testing.T, output string, copies ...testkit.DB) *console {
	t.Helper()
	args := []string{"merge"}
	for _, db := range copies {
		args = append(args, db.Path)
	}
	c := startInConsole(t, testkit.BinaryPath(), append(args, "--output", output)...)
	for i := range copies {
		c.waitFor("Пароль для", i+1)
		c.typeLine(testkit.Password)
	}
	return c
}

// A console ends the read of a line at Ctrl+C, which looks like the end of the input. So without
// care this person would get a result by the rules for no answer.
func TestCtrlCAtAQuestionGivesNoResult(t *testing.T) {
	dir := t.TempDir()
	gone := rawCopy(t, dir, "gone.kdbx", withoutTheEntry(deletedAt))
	held := rawCopy(t, dir, "held.kdbx", testkit.PlainXML)
	output := filepath.Join(dir, "new.kdbx")

	c := startMergeInConsole(t, output, gone, held)
	c.waitFor("Ваша відповідь", 1)
	c.press(ctrlC)

	if code := c.exitCode(); code != exitInterrupted {
		t.Errorf("exit code %d, want %d\n%q", code, exitInterrupted, c.screen())
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Errorf("a result appeared: %v", err)
	}
	wantNoTempFiles(t, dir)
}

// The questions work on a real terminal too, with what a person types.
func TestAQuestionTakesAnAnswerFromATerminal(t *testing.T) {
	dir := t.TempDir()
	gone := rawCopy(t, dir, "gone.kdbx", withoutTheEntry(deletedAt))
	held := rawCopy(t, dir, "held.kdbx", testkit.PlainXML)
	result := resultIn(dir, "new.kdbx")

	c := startMergeInConsole(t, result.Path, gone, held)
	c.waitFor("Ваша відповідь", 1)
	c.typeLine("2")
	c.waitFor("Чим відкриватиметься новий файл?", 1)
	c.waitFor("Ваша відповідь", 2)
	c.typeLine("")

	if code := c.exitCode(); code != 0 {
		t.Fatalf("exit code %d\n%q", code, c.screen())
	}
	c.waitFor("Видалено записів за вашим рішенням: 1", 1)
	wantNotInOutput(t, result.CLI(t, "ls"), "Запис")
}

// Each kind of question on a terminal, with a value shown on request. Only the value that was asked
// for reaches the screen.
func TestEveryKindOfQuestionWorksOnATerminal(t *testing.T) {
	dir := t.TempDir()
	const shown, hidden = secretMarker + "-показане", secretMarker + "-приховане"
	mail := func(password string) string {
		return fields("Title", "Пошта", "UserName", "я", "Password", password)
	}
	bank := fields("Title", "Банк", "UserName", "ти")
	record := "<DeletedObject><UUID>" + thirdEntryUUID + "</UUID><DeletionTime>" + deletedAt + "</DeletionTime></DeletedObject>"
	withStamp := func(stamp string, entries ...string) string {
		return strings.ReplaceAll(withEntries(entries...), v1, stamp)
	}
	a := rawCopy(t, dir, "a.kdbx", strings.Replace(withStamp(nearOne, mail(hidden), bank), "<DeletedObjects>", "<DeletedObjects>"+record, 1))
	b := rawCopy(t, dir, "b.kdbx", withStamp(nearTwo, mail(shown), "", fields("Title", "Третє"), bank))
	result := resultIn(dir, "new.kdbx")

	c := startMergeInConsole(t, result.Path, a, b)
	c.waitFor("Пар схожих записів з однаковим вмістом: 1.", 1)
	c.waitFor("Ваша відповідь", 1)
	c.typeLine("2") // merge the entries of Банк
	c.waitFor("Версії запису сперечаються:", 1)
	c.waitFor("Ваша відповідь", 2)
	c.typeLine("п")
	c.waitFor("Номер значення", 1)
	c.typeLine("1")
	c.waitFor(shown, 1)
	c.waitFor("Ваша відповідь", 3)
	c.typeLine("2") // the older version of Пошта
	c.waitFor("Запис видалено в одній копії", 1)
	c.waitFor("Ваша відповідь", 4)
	c.typeLine("2") // delete Третє
	c.waitFor("Чим відкриватиметься новий файл?", 1)
	c.waitFor("Ваша відповідь", 5)
	c.typeLine("")

	if code := c.exitCode(); code != 0 {
		t.Fatalf("exit code %d\n%q", code, c.screen())
	}
	c.waitFor("Видалено записів за вашим рішенням: 1", 1)
	c.waitFor("Злито схожих записів в інші: 1", 1)
	if strings.Contains(c.screen(), hidden) {
		t.Errorf("a value nobody asked for shows on the screen:\n%q", c.screen())
	}
	listing := result.CLI(t, "ls")
	wantInOutput(t, listing, "Пошта", "Банк")
	wantNotInOutput(t, listing, "Третє")
	if password := strings.TrimSpace(result.CLI(t, "show", "-s", "-a", "Password", "/Пошта")); password != hidden {
		t.Errorf("the main version holds another password than the chosen one: %q", password)
	}
}

// A new password stays hidden on a terminal too, while a typed answer shows.
func TestANewPasswordIsHiddenOnATerminal(t *testing.T) {
	const newPassword = "SECRET-MARKER-нове"
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", testkit.PlainXML)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: newPassword}

	c := startMergeInConsole(t, result.Path, a)
	c.waitFor("Ваша відповідь", 1)
	c.typeLine("2")
	c.waitFor("Новий пароль", 1)
	c.typeLine(newPassword)
	c.waitFor("Повторіть новий пароль", 1)
	c.typeLine(newPassword)
	c.waitFor("Новий файл-ключ", 1)
	c.typeLine("")
	c.waitFor("Новий слот YubiKey", 1)
	c.typeLine("")

	if code := c.exitCode(); code != 0 {
		t.Fatalf("exit code %d\n%q", code, c.screen())
	}
	if strings.Contains(c.screen(), "SECRET-MARKER") {
		t.Errorf("the new password shows on the screen:\n%q", c.screen())
	}
	if !strings.Contains(result.CLI(t, "ls"), "Запис") {
		t.Error("the new password does not open the result")
	}
}

// Each of these points lies inside the write protocol, with a temporary file on the disk. At the
// first two the file is still open, and on Windows nobody can remove an open file.
func TestCtrlCWhileWritingRemovesTheTemporaryFile(t *testing.T) {
	for _, point := range []string{
		safewriter.PointPartialWrite,
		safewriter.PointBeforeSync,
		safewriter.PointReadBack,
		safewriter.PointAudit,
		safewriter.PointJudge,
		safewriter.PointPublish,
	} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			a, b := divergedPair(t, dir)
			beforeA, beforeB := digestOf(t, a.Path), digestOf(t, b.Path)
			output := filepath.Join(dir, "new.kdbx")
			t.Setenv(safewriter.FaultVariable, safewriter.FaultPausePrefix+point)

			c := startMergeInConsole(t, output, a, b)
			c.waitFor("Ваша відповідь", 1)
			c.typeLine("")
			c.waitFor(safewriter.PausedAt+point, 1)
			if found := tempFiles(t, dir); len(found) != 1 {
				t.Fatalf("temporary files at the pause: %q, want one", found)
			}
			c.press(ctrlC)

			if code := c.exitCode(); code != exitInterrupted {
				t.Errorf("exit code %d, want %d\n%q", code, exitInterrupted, c.screen())
			}
			wantNoTempFiles(t, dir)
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Errorf("a file lies at the target: %v", err)
			}
			if digestOf(t, a.Path) != beforeA || digestOf(t, b.Path) != beforeB {
				t.Error("an input changed")
			}
			if strings.Contains(c.screen(), secretMarker) {
				t.Errorf("a secret shows on the screen:\n%q", c.screen())
			}
		})
	}
}

// Without hard links the temporary file itself takes the name of the result.
func TestCtrlCAfterTheResultGotItsNameLeavesAWholeResult(t *testing.T) {
	for name, fault := range map[string]string{
		"with a hard link":   safewriter.FaultPausePrefix + safewriter.PointPublished,
		"without hard links": safewriter.FaultNoHardLink + "," + safewriter.FaultPausePrefix + safewriter.PointPublished,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			a, b := divergedPair(t, dir)
			result := resultIn(dir, "new.kdbx")
			t.Setenv(safewriter.FaultVariable, fault)

			c := startMergeInConsole(t, result.Path, a, b)
			c.waitFor("Ваша відповідь", 1)
			c.typeLine("")
			c.waitFor(safewriter.PausedAt+safewriter.PointPublished, 1)
			c.press(ctrlC)

			if code := c.exitCode(); code != exitInterrupted {
				t.Errorf("exit code %d, want %d\n%q", code, exitInterrupted, c.screen())
			}
			wantNoTempFiles(t, dir)
			keptAllLines(t, result, a, b)
			c.waitFor("уже створено", 1)
		})
	}
}

// The real program holds the file open while it runs a slow KDF, and it gets the same Ctrl+C. The
// tool must wait for it to let go: on Windows nobody can remove a file another process reads.
func TestCtrlCDuringTheOutsideCheckRemovesTheTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	output := filepath.Join(dir, "new.kdbx")
	marker := filepath.Join(t.TempDir(), "the-program-holds-the-file")
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{HangCommand: "export", HangMarker: marker})
	for _, variable := range fake.Env() {
		name, value, _ := strings.Cut(variable, "=")
		t.Setenv(name, value)
	}

	c := startMergeInConsole(t, output, a, b)
	c.waitFor("Ваша відповідь", 1)
	c.typeLine("")
	deadline := time.Now().Add(ttyDeadline)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the program never started:\n%q", c.screen())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if found := tempFiles(t, dir); len(found) != 1 {
		t.Fatalf("temporary files during the check: %q, want one", found)
	}
	c.press(ctrlC)

	if code := c.exitCode(); code != exitInterrupted {
		t.Errorf("exit code %d, want %d\n%q", code, exitInterrupted, c.screen())
	}
	wantNoTempFiles(t, dir)
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Errorf("a file lies at the target: %v", err)
	}
}

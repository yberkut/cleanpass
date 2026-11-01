package main_test

import (
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/yberkut/cleanpass/internal/testkit"
)

// The tool sees a real console here: a pseudo console of Windows (ConPTY). It uses only
// the standard library, so that the test adds no direct dependency.

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	createPseudoConsole    = kernel32.NewProc("CreatePseudoConsole")
	closePseudoConsole     = kernel32.NewProc("ClosePseudoConsole")
	initializeAttributes   = kernel32.NewProc("InitializeProcThreadAttributeList")
	updateProcThreadAttrib = kernel32.NewProc("UpdateProcThreadAttribute")
	deleteAttributes       = kernel32.NewProc("DeleteProcThreadAttributeList")
)

const (
	extendedStartupInfoPresent = 0x00080000
	attributePseudoConsole     = 0x00020016
	consoleColumns             = 300
	consoleRows                = 100
	ttyDeadline                = time.Minute
)

type startupInfoEx struct {
	syscall.StartupInfo
	attributeList *byte
}

// console is a process that runs inside a pseudo console.
type console struct {
	t       *testing.T
	input   syscall.Handle
	process syscall.Handle
	handle  uintptr

	mu     sync.Mutex
	output strings.Builder
	closed chan struct{}
}

func startInConsole(t *testing.T, program string, args ...string) *console {
	t.Helper()
	if err := createPseudoConsole.Find(); err != nil {
		t.Skipf("this Windows has no pseudo console: %v", err)
	}

	var inputRead, inputWrite, outputRead, outputWrite syscall.Handle
	if err := syscall.CreatePipe(&inputRead, &inputWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := syscall.CreatePipe(&outputRead, &outputWrite, nil, 0); err != nil {
		t.Fatal(err)
	}

	c := &console{t: t, input: inputWrite, closed: make(chan struct{})}
	size := uintptr(consoleColumns) | uintptr(consoleRows)<<16
	result, _, _ := createPseudoConsole.Call(size, uintptr(inputRead), uintptr(outputWrite), 0, uintptr(unsafe.Pointer(&c.handle)))
	if result != 0 {
		t.Fatalf("CreatePseudoConsole: 0x%x", result)
	}

	var listSize uintptr
	initializeAttributes.Call(0, 1, 0, uintptr(unsafe.Pointer(&listSize)))
	list := make([]byte, listSize)
	ok, _, err := initializeAttributes.Call(uintptr(unsafe.Pointer(&list[0])), 1, 0, uintptr(unsafe.Pointer(&listSize)))
	if ok == 0 {
		t.Fatalf("InitializeProcThreadAttributeList: %v", err)
	}
	defer deleteAttributes.Call(uintptr(unsafe.Pointer(&list[0])))
	ok, _, err = updateProcThreadAttrib.Call(uintptr(unsafe.Pointer(&list[0])), 0, attributePseudoConsole,
		c.handle, unsafe.Sizeof(c.handle), 0, 0)
	if ok == 0 {
		t.Fatalf("UpdateProcThreadAttribute: %v", err)
	}

	commandLine := syscall.EscapeArg(program)
	for _, arg := range args {
		commandLine += " " + syscall.EscapeArg(arg)
	}
	commandLineUTF16, convertErr := syscall.UTF16PtrFromString(commandLine)
	if convertErr != nil {
		t.Fatal(convertErr)
	}
	info := startupInfoEx{attributeList: &list[0]}
	info.Cb = uint32(unsafe.Sizeof(info))
	// The handles of the test process are pipes. Without this the child would take them
	// instead of the pseudo console.
	info.Flags = syscall.STARTF_USESTDHANDLES
	info.StdInput, info.StdOutput, info.StdErr = syscall.InvalidHandle, syscall.InvalidHandle, syscall.InvalidHandle
	var processInfo syscall.ProcessInformation
	if err := syscall.CreateProcess(nil, commandLineUTF16, nil, nil, false,
		extendedStartupInfoPresent|syscall.CREATE_UNICODE_ENVIRONMENT, nil, nil,
		&info.StartupInfo, &processInfo); err != nil {
		t.Fatalf("CreateProcess: %v", err)
	}
	c.process = processInfo.Process
	syscall.CloseHandle(processInfo.Thread)
	syscall.CloseHandle(inputRead)
	syscall.CloseHandle(outputWrite)

	go func() {
		defer close(c.closed)
		buffer := make([]byte, 4096)
		for {
			var n uint32
			if err := syscall.ReadFile(outputRead, buffer, &n, nil); err != nil || n == 0 {
				return
			}
			c.mu.Lock()
			c.output.Write(buffer[:n])
			c.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		syscall.TerminateProcess(c.process, 1)
		closePseudoConsole.Call(c.handle)
		<-c.closed
		syscall.CloseHandle(outputRead)
		syscall.CloseHandle(c.input)
		syscall.CloseHandle(c.process)
	})
	return c
}

func (c *console) screen() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output.String()
}

// waitFor waits until the text has come count times.
func (c *console) waitFor(text string, count int) {
	c.t.Helper()
	deadline := time.Now().Add(ttyDeadline)
	for strings.Count(c.screen(), text) < count {
		if time.Now().After(deadline) {
			c.t.Fatalf("no %q (time %d) on the screen:\n%q", text, count, c.screen())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// typeLine types a line and Enter, as a person does.
func (c *console) typeLine(line string) {
	c.t.Helper()
	data := []byte(line + "\r")
	var written uint32
	if err := syscall.WriteFile(c.input, data, &written, nil); err != nil {
		c.t.Fatal(err)
	}
}

func (c *console) exitCode() uint32 {
	c.t.Helper()
	event, err := syscall.WaitForSingleObject(c.process, uint32(ttyDeadline.Milliseconds()))
	if err != nil || event != syscall.WAIT_OBJECT_0 {
		c.t.Fatalf("the process did not end: event %d, %v", event, err)
	}
	var code uint32
	if err := syscall.GetExitCodeProcess(c.process, &code); err != nil {
		c.t.Fatal(err)
	}
	return code
}

// The password does not show on a terminal, while an ordinary answer does. That answer
// also proves that a typed word would be visible on this screen.
func TestPasswordIsHiddenOnATerminal(t *testing.T) {
	const (
		wrong    = "SECRET-MARKER-wrong"
		right    = "SECRET-MARKER-right"
		answer   = "visible-answer.key"
		question = "Файл-ключ для"
		prompt   = "Пароль для"
	)
	db := testkit.DB{Path: filepath.Join(t.TempDir(), "a.kdbx"), Password: right}.Import(t, testkit.RichXML())
	c := startInConsole(t, testkit.BinaryPath(), "compare", db.Path)

	c.waitFor(prompt, 1)
	c.typeLine(wrong)
	c.waitFor("Неправильний", 1)
	c.waitFor(prompt, 2)
	c.typeLine(right)
	c.waitFor(question, 1)
	c.typeLine(answer)
	c.waitFor("Не вдалося прочитати файл-ключ", 1)
	c.waitFor(prompt, 3)
	c.typeLine("") // Enter repeats the password that was typed
	c.waitFor(question, 2)
	c.typeLine("") // Enter means no key file

	if code := c.exitCode(); code != 0 {
		t.Errorf("exit code %d\nscreen:\n%q", code, c.screen())
	}
	c.waitFor("Записів", 1)
	screen := c.screen()
	if strings.Contains(screen, "SECRET-MARKER") {
		t.Errorf("the password shows on the screen:\n%q", screen)
	}
	if !strings.Contains(screen, answer) {
		t.Errorf("an ordinary answer does not show, so this screen proves nothing:\n%q", screen)
	}
}

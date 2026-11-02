package session

import (
	"os"
	"regexp"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32                         = syscall.NewLazyDLL("kernel32.dll")
	procGetFileInformationByHandleEx = kernel32.NewProc("GetFileInformationByHandleEx")
)

// fileNameInfo is the class FILE_NAME_INFO of GetFileInformationByHandleEx.
const fileNameInfo = 2

// ptyPipeName is how MSYS2 and Cygwin name the pipes behind a terminal such as mintty. The full
// name looks like \msys-dd50a72ab4668b33-pty0-from-master. The pipe of a script has another form.
var ptyPipeName = regexp.MustCompile(`(^|\\)(msys|cygwin)-[^-\\]+-pty\d+`)

// pipeQuestionTime bounds the question about a pipe. It is long, so that a slow machine never takes
// the pipe of a script for a terminal.
const pipeQuestionTime = 2 * time.Second

// echoesTyping says stdin is a terminal of Git Bash that this program sees as a plain pipe. The
// terminal shows what is typed by itself, and a program has no way to hide it, because to Windows
// no console is there. A plain pipe from a script has no person behind it, and it must go on
// working.
//
// Windows blocks the question about a name for as long as someone else reads from the same pipe.
// Nobody but this program reads the pipe of a script, so such a pipe counts as a terminal. The
// goroutine that stays blocked ends with the process.
func echoesTyping(stdin *os.File) bool {
	handle := syscall.Handle(stdin.Fd())
	answer := make(chan bool, 1)
	go func() { answer <- isPtyPipe(handle) }()
	select {
	case echoes := <-answer:
		return echoes
	case <-time.After(pipeQuestionTime):
		return true
	}
}

func isPtyPipe(handle syscall.Handle) bool {
	kind, err := syscall.GetFileType(handle)
	if err != nil || kind != syscall.FILE_TYPE_PIPE {
		return false
	}
	return ptyPipeName.MatchString(pipeName(handle))
}

// pipeName gives the name of a pipe, or nothing for a pipe with no name.
func pipeName(handle syscall.Handle) string {
	// This is FILE_NAME_INFO: the length of the name in bytes, and then the name.
	var info struct {
		length uint32
		name   [syscall.MAX_PATH]uint16
	}
	ok, _, _ := procGetFileInformationByHandleEx.Call(uintptr(handle), fileNameInfo, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if ok == 0 || int(info.length/2) > len(info.name) {
		return ""
	}
	return syscall.UTF16ToString(info.name[:info.length/2])
}

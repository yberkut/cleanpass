package judge

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procCreateNamedPipe  = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe = kernel32.NewProc("ConnectNamedPipe")
)

const (
	pipeAccessInbound         = 0x00000001
	fileFlagFirstPipeInstance = 0x00080000
	pipeRejectRemoteClients   = 0x00000008
	pipeSingleInstance        = 1
	pipeBufferSize            = 64 * 1024
	// pipeReleaseTimeout ends the wait for a reader that nothing can wake any more.
	pipeReleaseTimeout = 10 * time.Second
)

// attachmentStream names where attachment-export can write so that the bytes reach out and no
// file: a named pipe. Its --stdout option would not do. It sends bytes through a text stream, and
// that changes line ends and what is not valid UTF-8.
//
// finish must run after the program has ended. When the program never opened the pipe, finish
// opens it itself, or the reader would wait forever.
func attachmentStream(_ *exec.Cmd, out io.Writer) (target string, finish func() error, err error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", nil, err
	}
	name := `\\.\pipe\cleanpass-` + hex.EncodeToString(random[:])
	wide, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", nil, err
	}
	handle, _, callErr := procCreateNamedPipe.Call(
		uintptr(unsafe.Pointer(wide)),
		pipeAccessInbound|fileFlagFirstPipeInstance,
		pipeRejectRemoteClients,
		pipeSingleInstance,
		0,
		pipeBufferSize,
		0,
		0,
	)
	if syscall.Handle(handle) == syscall.InvalidHandle {
		return "", nil, callErr
	}
	done := make(chan error, 1)
	go func() {
		// The result does not count: a program that came first also gives an error here, and
		// the read below works in every case.
		_, _, _ = procConnectNamedPipe.Call(handle, 0)
		pipe := os.NewFile(handle, name)
		_, copyErr := io.Copy(out, pipe)
		done <- errors.Join(copyErr, pipe.Close())
	}()
	return name, func() error {
		if client, err := os.OpenFile(name, os.O_WRONLY, 0); err == nil {
			client.Close()
		}
		select {
		case err := <-done:
			return err
		case <-time.After(pipeReleaseTimeout):
			return errors.New("the stream of an attachment did not end")
		}
	}, nil
}

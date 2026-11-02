package testkit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Process is a run of the built cleanpass that a test talks to while it works. So a test can
// do something at the moment the tool waits for the person.
type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout bytes.Buffer
	stderr lockedBuffer
	cancel context.CancelFunc
}

// lockedBuffer lets a test read what the process has written so far.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// Start runs the built cleanpass and leaves its stdin open.
func Start(t testing.TB, args ...string) *Process {
	t.Helper()
	return StartWithEnv(t, nil, args...)
}

// StartWithEnv is Start with variables added to the environment of the test process.
func StartWithEnv(t testing.TB, env []string, args ...string) *Process {
	t.Helper()
	if builtBinary == "" {
		t.Fatal("testkit.RunMain did not run: add it to TestMain")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	p := &Process{cancel: cancel}
	p.cmd = exec.CommandContext(ctx, builtBinary, args...)
	p.cmd.Env = append(os.Environ(), env...)
	p.cmd.Stdout, p.cmd.Stderr = &p.stdout, &p.stderr
	var err error
	if p.stdin, err = p.cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	return p
}

// WaitForStderr waits until stderr holds the text, for example the prompt of a copy.
func (p *Process) WaitForStderr(t testing.TB, text string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !strings.Contains(p.stderr.String(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q on stderr:\n%s", text, p.stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Send writes a line to stdin.
func (p *Process) Send(t testing.TB, line string) {
	t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

// Interrupt sends the signal of Ctrl+C. Windows has no such signal between processes: a test there
// presses the key in a console.
func (p *Process) Interrupt(t testing.TB) {
	t.Helper()
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
}

// Wait closes stdin and gives what the run gave.
func (p *Process) Wait(t testing.TB) Result {
	t.Helper()
	_ = p.stdin.Close()
	err := p.cmd.Wait()
	result := Result{Stdout: p.stdout.String(), Stderr: p.stderr.String()}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		result.Code = exit.ExitCode()
	case err != nil:
		t.Fatalf("run cleanpass: %v", err)
	}
	return result
}

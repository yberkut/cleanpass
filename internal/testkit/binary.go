package testkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var builtBinary string

// RunMain builds cleanpass and runs the tests of a package against that file.
// Use it from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testkit.RunMain(m)) }
func RunMain(m *testing.M) int {
	if _, err := CLIPath(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	dir, err := os.MkdirTemp("", "cleanpass-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)

	builtBinary = filepath.Join(dir, BinaryName())
	build := exec.Command("go", "build", "-o", builtBinary, "github.com/yberkut/cleanpass/cmd/cleanpass")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build cleanpass: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// BinaryName is the file name of the tool on this OS.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "cleanpass.exe"
	}
	return "cleanpass"
}

// BinaryPath returns the file RunMain built.
func BinaryPath() string { return builtBinary }

// Result is what one run of cleanpass gave.
type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// Options changes how Run starts the process.
type Options struct {
	Stdin string
	// Env is added to the environment of the test process.
	Env []string
	// Dir is the working directory of the process.
	Dir string
}

// Run starts the built cleanpass with lines on stdin and waits for it.
func Run(t testing.TB, stdin string, args ...string) Result {
	t.Helper()
	return RunWith(t, Options{Stdin: stdin}, args...)
}

// RunWith is Run with more control over the process.
func RunWith(t testing.TB, opts Options, args ...string) Result {
	t.Helper()
	if builtBinary == "" {
		t.Fatal("testkit.RunMain did not run: add it to TestMain")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, builtBinary, args...)
	cmd.Stdin = strings.NewReader(opts.Stdin)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), opts.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		result.Code = exit.ExitCode()
	case err != nil:
		t.Fatalf("run cleanpass: %v", err)
	}
	return result
}

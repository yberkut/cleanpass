package main_test

import (
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
	"github.com/yberkut/cleanpass/internal/text"
)

// A person compares the tool version with the releases on GitHub, so the tool says exactly the
// version that go build wrote into the same file, on one line of its own.
func TestToolVersionIsTheOneInTheFile(t *testing.T) {
	t.Parallel()
	info, err := buildinfo.ReadFile(testkit.BinaryPath())
	if err != nil {
		t.Fatal(err)
	}
	want := info.Main.Version
	if want == "" || want == "(devel)" {
		want = text.VersionUnknown
	}
	got := testkit.Run(t, "", "--version")

	wantCode(t, got, 0)
	if got.Stdout != want+"\n" {
		t.Errorf("stdout %q, want %q", got.Stdout, want+"\n")
	}
	if got.Stderr != "" {
		t.Errorf("stderr:\n%s", got.Stderr)
	}
}

func TestHelpNamesTheVersionOption(t *testing.T) {
	t.Parallel()
	got := testkit.Run(t, "", "--help")
	wantInOutput(t, got.Stdout, "  --version ")
}

// A file built without the data of git has no tool version to say. It says so in words, not with
// an empty line or the "(devel)" of Go.
func TestToolVersionOfABuildWithoutGitIsSaidInWords(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), testkit.BinaryName())
	build := exec.Command("go", "build", "-buildvcs=false", "-o", path, mainPkg)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Main.Version != "(devel)" {
		t.Fatalf("the file has version %q, so this test proves nothing", info.Main.Version)
	}
	got := testkit.RunWith(t, testkit.Options{Binary: path}, "--version")

	wantCode(t, got, 0)
	if lines := strings.Split(strings.TrimSuffix(got.Stdout, "\n"), "\n"); len(lines) != 1 || lines[0] == "" {
		t.Errorf("stdout is not one line of text: %q", got.Stdout)
	}
	wantNotInOutput(t, got.Stdout, "(devel)")
	wantUkrainian(t, got.Stdout, got.Stderr)
	if got.Stderr != "" {
		t.Errorf("stderr:\n%s", got.Stderr)
	}
}

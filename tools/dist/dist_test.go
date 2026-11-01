package main_test

import (
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The targets of the spec, written out here on purpose: the test must not read them
// from the tool it checks.
var targets = []struct{ file, goos, goarch string }{
	{"cleanpass-linux-amd64", "linux", "amd64"},
	{"cleanpass-linux-arm64", "linux", "arm64"},
	{"cleanpass-darwin-arm64", "darwin", "arm64"},
	{"cleanpass-darwin-amd64", "darwin", "amd64"},
	{"cleanpass-windows-amd64.exe", "windows", "amd64"},
}

var output string

// TestMain runs the tool once as a separate process, because all tests look at the same
// result. The environment asks for the wrong thing, as a shell left in another state
// could: the tool must set these values itself.
func TestMain(m *testing.M) { os.Exit(runTool(m)) }

func runTool(m *testing.M) int {
	dir, err := os.MkdirTemp("", "cleanpass-dist-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)

	tool := filepath.Join(dir, "dist-tool.exe")
	if out, err := exec.Command("go", "build", "-o", tool, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build the tool: %v\n%s", err, out)
		return 1
	}

	output = filepath.Join(dir, "out")
	run := exec.Command(tool, "-o", output)
	run.Env = append(os.Environ(), "CGO_ENABLED=1", "GOOS=plan9", "GOARCH=mips")
	if out, err := run.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "run the tool: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// SHA256SUMS holds exactly what `sha256sum -c` reads: one line for each file, in the
// binary-safe text form, with the name and no directory.
func TestChecksumsMatchTheFiles(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(output, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for _, target := range targets {
		data, err := os.ReadFile(filepath.Join(output, target.file))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&want, "%x  %s\n", sha256.Sum256(data), target.file)
	}
	if string(content) != want.String() {
		t.Errorf("SHA256SUMS:\n%s\nwant:\n%s", content, want.String())
	}
}

// The output directory holds only what the release publishes.
func TestOutputHoldsOnlyTheFilesAndTheirChecksums(t *testing.T) {
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	want := []string{"SHA256SUMS"}
	for _, target := range targets {
		want = append(want, target.file)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("files %v, want %v", got, want)
	}
}

// A file for Linux must carry no dependency on the system libraries of the machine.
func TestLinuxFilesAreStatic(t *testing.T) {
	for _, name := range []string{"cleanpass-linux-amd64", "cleanpass-linux-arm64"} {
		t.Run(name, func(t *testing.T) {
			file, err := elf.Open(filepath.Join(output, name))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			for _, program := range file.Progs {
				if program.Type == elf.PT_INTERP || program.Type == elf.PT_DYNAMIC {
					t.Errorf("the file has program header %v, so it is dynamically linked", program.Type)
				}
			}
		})
	}
}

// The spec wants a file of about 3 MB. Debug data is what would make it bigger for no use
// on a flash drive. A file size limit would break as the tool grows, so this looks for
// the data itself.
func TestFilesCarryNoDebugData(t *testing.T) {
	file, err := elf.Open(filepath.Join(output, "cleanpass-linux-amd64"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, section := range []string{".symtab", ".debug_info"} {
		if file.Section(section) != nil {
			t.Errorf("the file has section %s", section)
		}
	}
}

// macOS on Apple silicon kills a file without a signature at start. Nothing in this test
// runs that file, so it looks for the ad-hoc signature that the Go linker has to leave.
func TestAppleSiliconFileIsSigned(t *testing.T) {
	const loadCodeSignature = macho.LoadCmd(0x1d)
	file, err := macho.Open(filepath.Join(output, "cleanpass-darwin-arm64"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, load := range file.Loads {
		if raw := load.Raw(); len(raw) >= 4 && macho.LoadCmd(file.ByteOrder.Uint32(raw)) == loadCodeSignature {
			return
		}
	}
	t.Error("the file has no code signature")
}

// The README gives one plain `go build` for every OS. That is honest only while no
// dependency brings in cgo on a machine where cgo is on, such as Linux with gcc. A new
// import of net or os/user would change this and the file would stop being static.
func TestNoDependencyUsesCgoWhenCgoIsOn(t *testing.T) {
	cgoPackages := func(goos, pkg string) string {
		t.Helper()
		list := exec.Command("go", "list", "-deps", "-f", "{{if .CgoFiles}}{{.ImportPath}}{{end}}", pkg)
		list.Env = append(os.Environ(), "CGO_ENABLED=1", "GOOS="+goos)
		out, err := list.CombinedOutput()
		if err != nil {
			t.Fatalf("go list %s for %s: %v\n%s", pkg, goos, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if cgoPackages("linux", "net") == "" {
		t.Fatal("the check does not see the cgo of package net, so it proves nothing")
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if found := cgoPackages(goos, "github.com/yberkut/cleanpass/cmd/cleanpass"); found != "" {
			t.Errorf("for %s these packages use cgo:\n%s", goos, found)
		}
	}
}

// The file for this machine, if there is one, starts. This is what "runs" can mean here:
// the release files for other systems are checked by their format above.
func TestFileForThisMachineRuns(t *testing.T) {
	for _, target := range targets {
		if target.goos != runtime.GOOS || target.goarch != runtime.GOARCH {
			continue
		}
		path := filepath.Join(output, target.file)
		if out, err := exec.Command(path, "--help").CombinedOutput(); err != nil {
			t.Fatalf("%s --help: %v\n%s", target.file, err, out)
		}
		return
	}
	t.Skipf("no release target for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func TestEveryTargetIsBuiltWithoutCgo(t *testing.T) {
	for _, target := range targets {
		t.Run(target.file, func(t *testing.T) {
			info, err := buildinfo.ReadFile(filepath.Join(output, target.file))
			if err != nil {
				t.Fatal(err)
			}
			if want := "github.com/yberkut/cleanpass/cmd/cleanpass"; info.Path != want {
				t.Errorf("the file holds package %q, want %q", info.Path, want)
			}
			want := map[string]string{
				"CGO_ENABLED": "0",
				"GOOS":        target.goos,
				"GOARCH":      target.goarch,
				"-trimpath":   "true",
			}
			got := map[string]string{}
			for _, setting := range info.Settings {
				got[setting.Key] = setting.Value
			}
			for key, value := range want {
				if got[key] != value {
					t.Errorf("%s = %q, want %q", key, got[key], value)
				}
			}
		})
	}
}

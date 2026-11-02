package testkit

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// FakePlan says in which way the fake keepassxc-cli lies. The zero plan passes each call on to
// the real program. The type is the same as Plan in the command fakecli.
type FakePlan struct {
	// Version is what --version prints instead of the real version.
	Version string
	// VersionFails makes --version end with a failure.
	VersionFails bool
	// FailCommand is a command that ends with a failure and does nothing.
	FailCommand string
	// ExportOld and ExportNew change the first place of a text in what export prints.
	ExportOld, ExportNew string
	// ExportText is printed by export instead of what the real program prints.
	ExportText *string
	// AttachmentBytes is what attachment-export writes for the attachment AttachmentName.
	AttachmentName, AttachmentBytes string
	// SwapAfterMkdir is a file that takes the place of the database after mkdir saved it.
	SwapAfterMkdir string
	// WatchDirs and WatchFor look for clear text: at the end of each call, the fake notes each
	// file below these directories that holds one of the texts.
	WatchDirs, WatchFor []string
	// YubiKeySecret lets a call with --yubikey work with a key in software that holds this secret.
	// The real program cannot meet such a key. So the fake hands it a twin of the file that opens
	// without the key, and after a save it locks the file again with the key and a new salt. The
	// real program still reads what the file holds, but nothing here tests how it uses a key. It
	// works for a file with a password, and not with a key file.
	YubiKeySecret string
}

// FakeCall is what the fake noted about one call.
type FakeCall struct {
	Args  []string
	Env   []string
	Stdin string
	// Lied says that the plan changed what this call gave.
	Lied bool
	// ClearText names the watched files that held a watched text when the call ended.
	ClearText []string
}

// FakeCLI is the fake keepassxc-cli of one test.
type FakeCLI struct {
	// Dir holds the program. It is meant for PATH.
	Dir     string
	log     string
	plan    []byte
	realCLI string
}

var (
	fakeOnce sync.Once
	fakeDir  string
	fakeErr  error
)

// NewFakeCLI builds the fake once and gives it for one test with this plan.
func NewFakeCLI(t testing.TB, plan FakePlan) *FakeCLI {
	t.Helper()
	fakeOnce.Do(func() {
		if builtBinary == "" {
			fakeErr = errors.New("testkit.RunMain did not run: add it to TestMain")
			return
		}
		fakeDir = filepath.Join(filepath.Dir(builtBinary), "fake-keepassxc")
		build := exec.Command("go", "build", "-o", filepath.Join(fakeDir, fakeName()), "github.com/yberkut/cleanpass/internal/testkit/fakecli")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			fakeErr = errors.Join(err, errors.New(string(out)))
		}
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	realCLI, err := CLIPath()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return &FakeCLI{Dir: fakeDir, log: filepath.Join(t.TempDir(), "fake.log"), plan: encoded, realCLI: realCLI}
}

func fakeName() string {
	if runtime.GOOS == "windows" {
		return "keepassxc-cli.exe"
	}
	return "keepassxc-cli"
}

// Program gives the path of the fake.
func (f *FakeCLI) Program() string { return filepath.Join(f.Dir, fakeName()) }

// Env makes a run of cleanpass find the fake first, and no other keepassxc-cli on PATH.
func (f *FakeCLI) Env() []string {
	return append(f.PlanEnv(), "PATH="+f.Dir)
}

// PlanEnv is what the fake needs in order to run, without a change of PATH.
func (f *FakeCLI) PlanEnv() []string {
	return []string{"FAKE_CLI_PLAN=" + string(f.plan), "FAKE_CLI_LOG=" + f.log, "FAKE_CLI_REAL=" + f.realCLI}
}

// Calls gives what the fake noted, in order. No call means an empty list.
func (f *FakeCLI) Calls(t testing.TB) []FakeCall {
	t.Helper()
	file, err := os.Open(f.log)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var calls []FakeCall
	lines := bufio.NewScanner(file)
	lines.Buffer(nil, 1<<24)
	for lines.Scan() {
		var call FakeCall
		if err := json.Unmarshal(lines.Bytes(), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	return calls
}

// WantLied fails the test when the plan never acted. A test that proves a stop would prove
// nothing if the stop came from something else.
func (f *FakeCLI) WantLied(t testing.TB) {
	t.Helper()
	for _, call := range f.Calls(t) {
		if call.Lied {
			return
		}
	}
	t.Error("the fake keepassxc-cli never lied: the plan did not fit this run")
}

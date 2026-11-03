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
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit/softkey"
)

// YkmanPlan says what the fake ykman does. The zero plan is a key with softkey.FixtureSecret in
// slot 2 and an empty slot 1.
type YkmanPlan struct {
	// Slots gives the secret of each slot. A slot that is not here is empty.
	Slots map[int]string
	// Output is printed in place of a response.
	Output *string
	// FailCalls are the numbers of the calls that fail, from 1, the way a missed touch does.
	FailCalls []int
	// WaitFor is a file that must exist before the fake answers, the way a key waits for a touch.
	WaitFor string
}

// YkmanCall is what the fake noted about one call.
type YkmanCall struct {
	Args []string
	Env  []string
}

// FakeYkman is the fake ykman of one test.
type FakeYkman struct {
	// Dir holds the program. It is meant for PATH.
	Dir  string
	log  string
	plan []byte
}

var (
	fakeYkmanOnce sync.Once
	fakeYkmanDir  string
	fakeYkmanErr  error
)

// NewFakeYkman builds the fake once and gives it for one test with this plan.
func NewFakeYkman(t testing.TB, plan YkmanPlan) *FakeYkman {
	t.Helper()
	fakeYkmanOnce.Do(func() {
		if builtBinary == "" {
			fakeYkmanErr = errors.New("testkit.RunMain did not run: add it to TestMain")
			return
		}
		fakeYkmanDir = filepath.Join(filepath.Dir(builtBinary), "fake-ykman")
		build := exec.Command("go", "build", "-o", filepath.Join(fakeYkmanDir, ykmanName()), "github.com/yberkut/cleanpass/internal/testkit/fakeykman")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			fakeYkmanErr = errors.Join(err, errors.New(string(out)))
		}
	})
	if fakeYkmanErr != nil {
		t.Fatal(fakeYkmanErr)
	}
	if plan.Slots == nil {
		plan.Slots = map[int]string{2: softkey.FixtureSecret}
	}
	slots := make(map[string]string, len(plan.Slots))
	for slot, secret := range plan.Slots {
		slots[strconv.Itoa(slot)] = secret
	}
	encoded, err := json.Marshal(map[string]any{"Slots": slots, "Output": plan.Output, "FailCalls": plan.FailCalls, "WaitFor": plan.WaitFor})
	if err != nil {
		t.Fatal(err)
	}
	return &FakeYkman{Dir: fakeYkmanDir, log: filepath.Join(t.TempDir(), "ykman.log"), plan: encoded}
}

func ykmanName() string {
	if runtime.GOOS == "windows" {
		return "ykman.exe"
	}
	return "ykman"
}

// Program gives the path of the fake.
func (f *FakeYkman) Program() string { return filepath.Join(f.Dir, ykmanName()) }

// Env makes a run of cleanpass find the fake first.
func (f *FakeYkman) Env() []string { return append(f.PlanEnv(), PathTo(f.Dir)) }

// PlanEnv is what the fake needs in order to run, without a change of PATH.
func (f *FakeYkman) PlanEnv() []string {
	return []string{"FAKE_YKMAN_PLAN=" + string(f.plan), "FAKE_YKMAN_LOG=" + f.log}
}

// Calls gives what the fake noted, in order. No call means an empty list.
func (f *FakeYkman) Calls(t testing.TB) []YkmanCall {
	t.Helper()
	file, err := os.Open(f.log)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var calls []YkmanCall
	lines := bufio.NewScanner(file)
	lines.Buffer(nil, 1<<24)
	for lines.Scan() {
		var call YkmanCall
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

// PathTo gives a PATH that holds only these directories.
func PathTo(dirs ...string) string {
	return "PATH=" + strings.Join(dirs, string(os.PathListSeparator))
}

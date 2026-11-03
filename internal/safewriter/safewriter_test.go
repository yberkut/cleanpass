package safewriter_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yberkut/cleanpass/internal/safewriter"
)

const content = "the result, already encrypted"

// setup makes a directory for the result, one input, and a plan whose steps all pass and do
// nothing. A test changes the steps it looks at.
type setup struct {
	dir, input, target string
	plan               safewriter.Plan
}

func newSetup(t *testing.T) *setup {
	t.Helper()
	dir := t.TempDir()
	s := &setup{
		dir:    dir,
		input:  filepath.Join(dir, "input.kdbx"),
		target: filepath.Join(dir, "result.kdbx"),
	}
	writeFile(t, s.input, "input bytes")
	s.plan = safewriter.Plan{
		Target: s.target,
		Inputs: []safewriter.Input{{Path: s.input, Digest: sha256.Sum256([]byte("input bytes"))}},
		Encode: func(w io.Writer) error {
			_, err := io.WriteString(w, content)
			return err
		},
		ReadBack: func(string) error { return nil },
		Audit:    func() error { return nil },
		Judge:    func(string) error { return nil },
	}
	return s
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// names gives the files in a directory, sorted.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result
}

// wantOnly checks that the directory holds these files and nothing else: no temporary file.
func wantOnly(t *testing.T, dir string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := names(t, dir); !slices.Equal(got, want) {
		t.Errorf("the directory holds %q, want %q", got, want)
	}
}

func wantFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s holds %q, want %q", path, got, want)
	}
}

func TestAPlanThatPassesPublishesTheResult(t *testing.T) {
	s := newSetup(t)
	if err := safewriter.Run(s.plan); err != nil {
		t.Fatal(err)
	}
	wantFile(t, s.target, content)
	wantFile(t, s.input, "input bytes")
	wantOnly(t, s.dir, "input.kdbx", "result.kdbx")
}

// The result stays out of sight of the person until every check has passed. Each check looks
// at a finished temporary file in the directory of the result.
func TestTheStepsRunInTheOrderOfTheProtocol(t *testing.T) {
	s := newSetup(t)
	var steps []string
	var tempSeen string

	see := func(step, temp string) {
		t.Helper()
		steps = append(steps, step)
		if _, err := os.Lstat(s.target); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("at step %s the result is already there", step)
		}
		if temp == "" {
			return
		}
		if filepath.Dir(temp) != s.dir {
			t.Errorf("the temporary file %s lies outside the directory of the result", temp)
		}
		wantFile(t, temp, content)
		if tempSeen != "" && tempSeen != temp {
			t.Errorf("step %s looks at %s, an earlier step at %s", step, temp, tempSeen)
		}
		tempSeen = temp
	}
	encode := s.plan.Encode
	s.plan.Encode = func(w io.Writer) error {
		see("encode", "")
		return encode(w)
	}
	s.plan.ReadBack = func(temp string) error {
		see("read back", temp)
		return nil
	}
	s.plan.Audit = func() error {
		see("audit", "")
		return nil
	}
	s.plan.Judge = func(temp string) error {
		see("judge", temp)
		return nil
	}

	if err := safewriter.Run(s.plan); err != nil {
		t.Fatal(err)
	}
	if want := []string{"encode", "read back", "audit", "judge"}; !slices.Equal(steps, want) {
		t.Errorf("the steps ran as %q, want %q", steps, want)
	}
}

var errStep = errors.New("this step fails")

func TestAnyFailingStepStopsTheWork(t *testing.T) {
	for name, breakStep := range map[string]func(*safewriter.Plan){
		"encode":    func(p *safewriter.Plan) { p.Encode = func(io.Writer) error { return errStep } },
		"read back": func(p *safewriter.Plan) { p.ReadBack = func(string) error { return errStep } },
		"audit":     func(p *safewriter.Plan) { p.Audit = func() error { return errStep } },
		"judge":     func(p *safewriter.Plan) { p.Judge = func(string) error { return errStep } },
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetup(t)
			breakStep(&s.plan)
			if err := safewriter.Run(s.plan); !errors.Is(err, errStep) {
				t.Errorf("the error is %v, want the one of the step", err)
			}
			wantOnly(t, s.dir, "input.kdbx")
		})
	}
}

func TestAStepAfterTheFailingOneDoesNotRun(t *testing.T) {
	s := newSetup(t)
	s.plan.Audit = func() error { return errStep }
	s.plan.Judge = func(string) error {
		t.Error("the judge ran after a failed audit")
		return nil
	}
	if err := safewriter.Run(s.plan); !errors.Is(err, errStep) {
		t.Errorf("the error is %v", err)
	}
}

func TestAnExistingTargetIsRefusedAndStaysAsItIs(t *testing.T) {
	s := newSetup(t)
	writeFile(t, s.target, "the file of the person")
	if err := safewriter.Run(s.plan); !errors.Is(err, safewriter.ErrTargetExists) {
		t.Errorf("the error is %v", err)
	}
	wantFile(t, s.target, "the file of the person")
	wantOnly(t, s.dir, "input.kdbx", "result.kdbx")
}

func TestABrokenLinkAtTheTargetIsRefused(t *testing.T) {
	s := newSetup(t)
	if err := os.Symlink(filepath.Join(s.dir, "nowhere"), s.target); err != nil {
		t.Skipf("this system does not let the test make a link: %v", err)
	}
	if err := safewriter.Run(s.plan); !errors.Is(err, safewriter.ErrTargetExists) {
		t.Errorf("the error is %v", err)
	}
}

func TestADirectoryAtTheTargetIsRefused(t *testing.T) {
	s := newSetup(t)
	if err := os.Mkdir(s.target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := safewriter.Run(s.plan); !errors.Is(err, safewriter.ErrTargetExists) {
		t.Errorf("the error is %v", err)
	}
}

func TestATargetThatIsAnInputIsRefused(t *testing.T) {
	s := newSetup(t)
	hardLink := filepath.Join(s.dir, "the-same-file.kdbx")
	if err := os.Link(s.input, hardLink); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"the same path":    s.input,
		"another spelling": s.dir + string(filepath.Separator) + "." + string(filepath.Separator) + "input.kdbx",
		"a hard link":      hardLink,
	} {
		t.Run(name, func(t *testing.T) {
			s.plan.Target = target
			if err := safewriter.Run(s.plan); !errors.Is(err, safewriter.ErrTargetIsInput) {
				t.Errorf("the error is %v", err)
			}
			wantFile(t, s.input, "input bytes")
		})
	}
}

func TestATargetInADirectoryThatIsNotThereIsRefused(t *testing.T) {
	s := newSetup(t)
	s.plan.Target = filepath.Join(s.dir, "no-such-directory", "result.kdbx")
	s.plan.Encode = func(io.Writer) error {
		t.Error("the work went on")
		return nil
	}
	if err := safewriter.Run(s.plan); !errors.Is(err, safewriter.ErrTargetDirectory) {
		t.Errorf("the error is %v", err)
	}
}

func TestAnInputThatChangedBeforeTheWriteStopsTheWork(t *testing.T) {
	s := newSetup(t)
	writeFile(t, s.input, "changed after it was read")
	s.plan.Encode = func(io.Writer) error {
		t.Error("the write went on")
		return nil
	}
	var changed *safewriter.InputChangedError
	if err := safewriter.Run(s.plan); !errors.As(err, &changed) || changed.Path != s.input {
		t.Errorf("the error is %v, want one that names the input", err)
	}
	wantOnly(t, s.dir, "input.kdbx")
}

func TestAnInputThatChangedDuringTheWorkLeavesNoResult(t *testing.T) {
	for _, mode := range hardLinkModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv(safewriter.FaultVariable, mode.fault)
			s := newSetup(t)
			s.plan.Judge = func(string) error {
				writeFile(t, s.input, "changed during the work")
				return nil
			}
			var changed *safewriter.InputChangedError
			if err := safewriter.Run(s.plan); !errors.As(err, &changed) || changed.Path != s.input {
				t.Errorf("the error is %v, want one that names the input", err)
			}
			wantOnly(t, s.dir, "input.kdbx")
		})
	}
}

func TestAnInputThatDisappearedLeavesNoResult(t *testing.T) {
	s := newSetup(t)
	s.plan.Judge = func(string) error { return os.Remove(s.input) }
	var changed *safewriter.InputChangedError
	if err := safewriter.Run(s.plan); !errors.As(err, &changed) || changed.Path != s.input {
		t.Errorf("the error is %v, want one that names the input", err)
	}
	wantOnly(t, s.dir)
}

var hardLinkModes = []struct{ name, fault string }{
	{"with a hard link", ""},
	{"on a file system without hard links", safewriter.FaultNoHardLink},
}

func TestPublishingNeverOverwrites(t *testing.T) {
	for _, mode := range hardLinkModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv(safewriter.FaultVariable, mode.fault)
			s := newSetup(t)
			s.plan.Judge = func(string) error {
				writeFile(t, s.target, "came during the work")
				return nil
			}
			if err := safewriter.Run(s.plan); !errors.Is(err, safewriter.ErrTargetExists) {
				t.Errorf("the error is %v", err)
			}
			wantFile(t, s.target, "came during the work")
			wantOnly(t, s.dir, "input.kdbx", "result.kdbx")
		})
	}
}

func TestPublishingWorksWithoutHardLinks(t *testing.T) {
	t.Setenv(safewriter.FaultVariable, safewriter.FaultNoHardLink)
	s := newSetup(t)
	if err := safewriter.Run(s.plan); err != nil {
		t.Fatal(err)
	}
	wantFile(t, s.target, content)
	wantOnly(t, s.dir, "input.kdbx", "result.kdbx")
}

func TestAResultOutsideTheDirectoryOfTheInputs(t *testing.T) {
	s := newSetup(t)
	elsewhere := t.TempDir()
	s.plan.Target = filepath.Join(elsewhere, "шлях з пробілами і кирилицею.kdbx")
	if err := safewriter.Run(s.plan); err != nil {
		t.Fatal(err)
	}
	wantFile(t, s.plan.Target, content)
	wantOnly(t, elsewhere, "шлях з пробілами і кирилицею.kdbx")
}

func TestAWriteThatFailsHalfWayLeavesNoResult(t *testing.T) {
	s := newSetup(t)
	s.plan.Encode = func(w io.Writer) error {
		if _, err := fmt.Fprint(w, "half of the"); err != nil {
			return err
		}
		return errStep
	}
	if err := safewriter.Run(s.plan); !errors.Is(err, errStep) {
		t.Errorf("the error is %v", err)
	}
	wantOnly(t, s.dir, "input.kdbx")
}

func TestCheckTargetSaysTheSameBeforeAnyWork(t *testing.T) {
	s := newSetup(t)
	if err := safewriter.CheckTarget(s.target, []string{s.input}); err != nil {
		t.Errorf("a free target is refused: %v", err)
	}
	if err := safewriter.CheckTarget(s.input, []string{s.input}); !errors.Is(err, safewriter.ErrTargetIsInput) {
		t.Errorf("the error is %v", err)
	}
	if err := safewriter.CheckTarget(s.dir, []string{s.input}); !errors.Is(err, safewriter.ErrTargetExists) {
		t.Errorf("the error is %v", err)
	}
}

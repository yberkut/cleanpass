// Package safewriter is the Safe Writer: it brings a result to its place under the write
// protocol of the spec, so that a failure at any moment leaves the inputs alone and no half
// of a result in sight.
//
// It knows no database. The other modules come in as the steps of a Plan.
package safewriter

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yberkut/cleanpass/internal/interrupt"
)

// The errors of Run about the target.
var (
	// ErrTargetExists means something already lies at the path of the result.
	ErrTargetExists = errors.New("the target already exists")
	// ErrTargetIsInput means the path of the result leads to an input file.
	ErrTargetIsInput = errors.New("the target is an input file")
	// ErrTargetDirectory means the directory of the result is not there.
	ErrTargetDirectory = errors.New("the directory of the target is not there")
	// ErrResultLeft comes with an InputChangedError when the result was already in its place
	// and could not be taken back.
	ErrResultLeft = errors.New("the result could not be removed")
)

// InputChangedError means an input is no longer what was read.
type InputChangedError struct {
	Path string
	// Cause is what went wrong with reading the file, or nil when the bytes differ.
	Cause error
}

func (e *InputChangedError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("input %s cannot be checked: %v", e.Path, e.Cause)
	}
	return fmt.Sprintf("input %s has changed", e.Path)
}

func (e *InputChangedError) Unwrap() error { return e.Cause }

// Input is one input file and the hash of the bytes the work is based on.
type Input struct {
	Path   string
	Digest [sha256.Size]byte
}

// Plan says what to write and how to check it. Run calls the steps in the order of the
// protocol, and the first error stops the work.
type Plan struct {
	// Target is the path of the result.
	Target string
	Inputs []Input
	// Encode writes the result. What it writes is already encrypted.
	Encode func(w io.Writer) error
	// ReadBack reads the temporary file and compares it with the expected result.
	ReadBack func(temp string) error
	// Audit proves that nothing is lost.
	Audit func() error
	// Judge gives the temporary file to an outside judge.
	Judge func(temp string) error
	// Interrupted hears what Ctrl+C left behind, just before the process ends. It may be nil.
	Interrupted func(Interruption)
}

// Interruption says what Ctrl+C left behind.
type Interruption struct {
	// TempLeft is the path of a temporary file that could not be removed, or empty.
	TempLeft string
	// Published says the result was already in its place.
	Published bool
}

// Run carries out the protocol. Any error and Ctrl+C stop the work and leave no temporary file.
// Only a crash can leave a temporary file, and that one is encrypted. An error leaves no result,
// and Ctrl+C leaves one only if it was already in its place, whole.
func Run(plan Plan) error {
	faults := loadFaults()
	guard := &tempGuard{interrupted: plan.Interrupted}
	defer interrupt.Guard(guard.onInterrupt)()

	faults.hit(PointCheckTarget)
	paths := make([]string, 0, len(plan.Inputs))
	for _, input := range plan.Inputs {
		paths = append(paths, input.Path)
	}
	if err := CheckTarget(plan.Target, paths); err != nil {
		return err
	}

	faults.hit(PointHashInputs)
	if err := checkInputs(plan.Inputs); err != nil {
		return err
	}

	faults.hit(PointWriteTemp)
	temp, err := writeTemp(plan.Target, plan.Encode, faults, guard)
	if temp != "" {
		// After a rename the temporary file is gone, and this finds nothing to remove.
		defer os.Remove(temp)
	}
	if err != nil {
		return err
	}

	faults.hit(PointReadBack)
	if err := plan.ReadBack(temp); err != nil {
		return err
	}

	faults.hit(PointAudit)
	if err := plan.Audit(); err != nil {
		return err
	}

	faults.hit(PointJudge)
	if err := plan.Judge(temp); err != nil {
		return err
	}

	faults.hit(PointPublish)
	if err := guard.publish(temp, plan.Target, faults); err != nil {
		return err
	}

	faults.hit(PointPublished)
	if err := checkInputs(plan.Inputs); err != nil {
		// The result is whole, but it holds an old state of this input. The person would
		// take it for the newest, and the merge is quick to run again.
		if removeErr := os.Remove(plan.Target); removeErr != nil {
			return errors.Join(err, ErrResultLeft, removeErr)
		}
		return err
	}

	faults.hit(PointDone)
	return nil
}

// CheckTarget tells whether a result can go to this path. A caller can also use it before any
// work: Run asks again, because time passes in between.
func CheckTarget(target string, inputs []string) error {
	targetInfo, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		directory, err := os.Stat(filepath.Dir(target))
		if err != nil || !directory.IsDir() {
			return ErrTargetDirectory
		}
		return nil
	}
	if err != nil {
		return err
	}
	// The path may also be a link to an input, so look at the file behind it as well.
	followed, followErr := os.Stat(target)
	for _, input := range inputs {
		inputInfo, err := os.Stat(input)
		if err != nil {
			continue
		}
		if os.SameFile(targetInfo, inputInfo) || (followErr == nil && os.SameFile(followed, inputInfo)) {
			return ErrTargetIsInput
		}
	}
	return ErrTargetExists
}

func checkInputs(inputs []Input) error {
	for _, input := range inputs {
		digest, err := hashFile(input.Path)
		if err != nil {
			return &InputChangedError{Path: input.Path, Cause: err}
		}
		if digest != input.Digest {
			return &InputChangedError{Path: input.Path}
		}
	}
	return nil
}

func hashFile(path string) (digest [sha256.Size]byte, err error) {
	file, err := os.Open(path)
	if err != nil {
		return digest, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return digest, err
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

// writeTemp makes the temporary file beside the target: a rename stays on one file system
// only there. CreateTemp creates it exclusively and open for its owner only. It gives the
// path even with an error, so that the caller can remove what is there.
func writeTemp(target string, encode func(io.Writer) error, faults *faults, guard *tempGuard) (string, error) {
	file, err := guard.create(filepath.Dir(target))
	if err != nil {
		return "", err
	}
	if err := encode(&partialWriteFault{file: file, faults: faults}); err != nil {
		guard.closeFile()
		return file.Name(), err
	}
	faults.hit(PointBeforeSync)
	if err := file.Sync(); err != nil {
		guard.closeFile()
		return file.Name(), err
	}
	return file.Name(), guard.closeFile()
}

// tempGuard knows the temporary file, so that Ctrl+C can take it away at any step. Its lock keeps
// the steps apart: after an interrupt has taken it, no step that needs it goes on, and no result
// gets its name behind the back of an interrupt.
type tempGuard struct {
	mu          sync.Mutex
	path        string
	file        *os.File
	published   bool
	interrupted func(Interruption)
}

func (g *tempGuard) create(directory string) (*os.File, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	file, err := os.CreateTemp(directory, "cleanpass-*.tmp")
	if err != nil {
		return nil, err
	}
	g.path, g.file = file.Name(), file
	return file, nil
}

func (g *tempGuard) closeFile() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	err := g.file.Close()
	g.file = nil
	return err
}

func (g *tempGuard) publish(temp, target string, faults *faults) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	err := publish(temp, target, faults)
	g.published = err == nil
	return err
}

// removeTries and removeWait bound the wait for a temporary file another process still reads: on
// Windows no one can remove such a file, and a Judge that got the same Ctrl+C needs a moment to end.
const (
	removeTries = 50
	removeWait  = 100 * time.Millisecond
)

// onInterrupt runs on Ctrl+C, on its own goroutine. The lock stays taken: the process ends after this.
func (g *tempGuard) onInterrupt() {
	g.mu.Lock()
	if g.file != nil {
		g.file.Close()
	}
	result := Interruption{Published: g.published}
	if g.path != "" && !removeWithin(g.path, removeTries) {
		result.TempLeft = g.path
	}
	if g.interrupted != nil {
		g.interrupted(result)
	}
}

// removeWithin says the file is gone.
func removeWithin(path string, tries int) bool {
	for try := 0; ; try++ {
		err := os.Remove(path)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return true
		}
		if try == tries {
			return false
		}
		time.Sleep(removeWait)
	}
}

// publish puts the temporary file at the target and never overwrites. A hard link does that
// in one step. A file system without hard links (exFAT, FAT32) gets a check and a rename, as
// the spec says.
func publish(temp, target string, faults *faults) error {
	err := errHardLinksOff
	if !faults.noHardLink {
		err = os.Link(temp, target)
	}
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrExist):
		return ErrTargetExists
	default:
		if _, err := os.Lstat(target); err == nil {
			return ErrTargetExists
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := os.Rename(temp, target); err != nil {
			return err
		}
	}
	syncDirectory(filepath.Dir(target))
	return nil
}

var errHardLinksOff = errors.New("hard links are off")

// syncDirectory asks the system to keep the new name after a power cut. The file itself is
// already on the disk. Not all systems can do this for a directory, and that is no reason
// to give up a whole result.
func syncDirectory(path string) {
	directory, err := os.Open(path)
	if err != nil {
		return
	}
	defer directory.Close()
	_ = directory.Sync()
}

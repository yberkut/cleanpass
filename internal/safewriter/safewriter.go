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
}

// Run carries out the protocol. Any error stops the work and leaves no temporary file and no
// result. Only a crash can leave a temporary file, and that one is encrypted.
func Run(plan Plan) error {
	faults := loadFaults()

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
	temp, err := writeTemp(plan.Target, plan.Encode, faults)
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
	if err := publish(temp, plan.Target, faults); err != nil {
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
func writeTemp(target string, encode func(io.Writer) error, faults *faults) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(target), "cleanpass-*.tmp")
	if err != nil {
		return "", err
	}
	if err := encode(&partialWriteFault{file: file, faults: faults}); err != nil {
		file.Close()
		return file.Name(), err
	}
	faults.hit(PointBeforeSync)
	if err := file.Sync(); err != nil {
		file.Close()
		return file.Name(), err
	}
	return file.Name(), file.Close()
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

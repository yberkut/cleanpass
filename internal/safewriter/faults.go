package safewriter

import (
	"os"
	"strings"
)

// FaultVariable is the only way to turn on the faults below. They exist for the tests of a
// crash: a real power cut cannot run in a test. Without this variable nothing here acts.
const FaultVariable = "CLEANPASS_TEST_FAULT"

// FaultNoHardLink as the value of FaultVariable makes publishing act as on a file system
// without hard links.
const FaultNoHardLink = "no-hard-link"

// FaultCrashPrefix followed by a point makes the process end at that point at once, with
// CrashExitCode and without any cleanup. Several faults go in the variable with commas.
const FaultCrashPrefix = "crash:"

// CrashExitCode is the exit code of a crash a test asked for.
const CrashExitCode = 99

// The points of the protocol where a crash can come, in order. Each one lies before the step
// it names, apart from the ones that say otherwise.
const (
	PointCheckTarget = "check-target"
	PointHashInputs  = "hash-inputs"
	PointWriteTemp   = "write-temp"
	// PointPartialWrite lies after the first part of the file reached the disk.
	PointPartialWrite = "partial-write"
	// PointBeforeSync lies after the whole file was written and before the fsync.
	PointBeforeSync = "before-sync"
	PointReadBack   = "read-back"
	PointAudit      = "audit"
	PointJudge      = "judge"
	PointPublish    = "publish"
	// PointPublished lies after the result got its name.
	PointPublished = "published"
	// PointDone lies after all steps.
	PointDone = "done"
)

// faults is what FaultVariable asks for in this run.
type faults struct {
	crashAt    string
	noHardLink bool
}

func loadFaults() *faults {
	result := &faults{}
	for fault := range strings.SplitSeq(os.Getenv(FaultVariable), ",") {
		if point, isCrash := strings.CutPrefix(fault, FaultCrashPrefix); isCrash {
			result.crashAt = point
		}
		if fault == FaultNoHardLink {
			result.noHardLink = true
		}
	}
	return result
}

// hit ends the process if a test asked for a crash at this point.
func (f *faults) hit(point string) {
	if f.crashAt == point {
		os.Exit(CrashExitCode)
	}
}

// partialWriteFault passes the writes on to the file and gives the point of a crash after the
// first one. The library writes the header first, so the file then ends in the middle.
type partialWriteFault struct {
	file   *os.File
	faults *faults
}

func (w *partialWriteFault) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	w.faults.hit(PointPartialWrite)
	return n, err
}

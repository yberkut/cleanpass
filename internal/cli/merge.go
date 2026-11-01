package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/report"
	"github.com/yberkut/cleanpass/internal/safewriter"
	"github.com/yberkut/cleanpass/internal/session"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/text"
)

func init() { register("merge", runMerge) }

const outputOption = "--output"

// runMerge makes a new file from the copies and never changes them. It asks nothing but the
// keys for now: every question goes by the rule for no answer.
func runMerge(env Env, args []string) int {
	output, rest, problem := cutOutput(args)
	if problem != "" {
		return fail(env, problem)
	}
	copies, problem := parseCopies(rest)
	if problem != "" {
		return fail(env, problem)
	}
	if output == "" {
		return fail(env, text.MergeNeedsOutput)
	}
	if problem := checkCopies(copies); problem != "" {
		return fail(env, problem)
	}
	// This comes before the passwords only to save the person the typing. The Safe Writer
	// asks again, because the person may take their time.
	if err := safewriter.CheckTarget(output, copyPaths(copies)); err != nil {
		return fail(env, describeWriteError(output, err))
	}

	opened, code, stopped := openForMerge(env, copies)
	if stopped {
		return code
	}
	result, err := merge.Run(merge.Input{Copies: opened.forEngine(), Now: time.Now(), Key: merge.NewKey()})
	if err != nil {
		return fail(env, text.MergeEngineFailed)
	}

	keysFrom := opened.newest()
	if err := writeResult(env, output, opened, result.Merged, keysFrom); err != nil {
		return reportWriteError(env, output, err)
	}

	lines := []string{text.MergeDone(output), text.MergeKeysFrom(keysFrom.path)}
	if count := len(result.Questions); count > 0 {
		lines = append(lines, text.MergeUnanswered(count))
	}
	lines = append(lines, text.MergeInputsIntact)
	for _, line := range lines {
		if _, err := fmt.Fprintln(env.Stdout, line); err != nil {
			return exitError
		}
	}
	return exitOK
}

// cutOutput takes --output out of the arguments and leaves the rest as the other commands
// read them. The word after the option is its value, whatever it looks like: a file can be
// named --a-merged.kdbx. After -- every word is a file.
func cutOutput(args []string) (output string, rest []string, problem string) {
	seen := false
	take := func(value string) string {
		if seen {
			return text.OptionTwice(outputOption)
		}
		seen, output = true, value
		return ""
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, hasValue := strings.CutPrefix(arg, outputOption+"=")
		switch {
		case arg == "--":
			return output, append(rest, args[i:]...), ""
		case arg == "-k" || arg == "--key-file":
			rest = append(rest, arg)
			if i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
		case hasValue:
			if problem := take(value); problem != "" {
				return "", nil, problem
			}
		case arg == outputOption:
			if i+1 == len(args) {
				return "", nil, text.OptionNeedsValue(outputOption)
			}
			i++
			if problem := take(args[i]); problem != "" {
				return "", nil, problem
			}
		default:
			rest = append(rest, arg)
		}
	}
	return output, rest, ""
}

func copyPaths(copies []session.Copy) []string {
	paths := make([]string, 0, len(copies))
	for _, c := range copies {
		paths = append(paths, c.Path)
	}
	return paths
}

// openedCopy is a copy that is open, with the keys that fit it.
type openedCopy struct {
	path string
	snap *snapshot.Snapshot
	keys gateway.Credentials
}

type openedCopies []openedCopy

// openForMerge opens each copy. A copy with data a save would not keep ends the work at
// once, with the places of that data, and no option gets around this.
func openForMerge(env Env, copies []session.Copy) (opened openedCopies, code int, stopped bool) {
	dialog := session.New(env.Stdin, env.Stderr)
	for _, c := range copies {
		snap, keys, err := dialog.OpenWithKeys(c)
		if err != nil {
			return nil, fail(env, describeFileError(c.Path, err)), true
		}
		if len(snap.FieldLosses) > 0 {
			code := fail(env, text.MergeStoppedByFields(c.Path))
			_ = report.WriteFieldLosses(env.Stderr, snap.FieldLosses)
			return nil, code, true
		}
		opened = append(opened, openedCopy{path: c.Path, snap: snap, keys: keys})
	}
	return opened, exitOK, false
}

func (o openedCopies) forEngine() []merge.Copy {
	result := make([]merge.Copy, 0, len(o))
	for i := range o {
		result = append(result, merge.Copy{Name: o[i].path, Snapshot: o[i].snap})
	}
	return result
}

// newest gives the copy with the newest change: by default its keys, cipher and KDF go to the
// result. Of copies with the same time the first on the command line counts.
func (o openedCopies) newest() *openedCopy {
	best := &o[0]
	for i := range o {
		if o[i].snap.LastChange().After(best.snap.LastChange()) {
			best = &o[i]
		}
	}
	return best
}

// lossesError means the Loss Audit found something missing in the written file.
type lossesError struct{ losses []lossaudit.Loss }

func (e *lossesError) Error() string { return "the audit found losses" }

// writeResult runs the write protocol. The checks look at what the file really holds, not at
// what the merge gave.
func writeResult(env Env, output string, opened openedCopies, merged *snapshot.Snapshot, keysFrom *openedCopy) error {
	format := gateway.OutputFormat(keysFrom.snap.Format)
	inputs := make([]safewriter.Input, 0, len(opened))
	sources := make([]*snapshot.Snapshot, 0, len(opened))
	for i := range opened {
		inputs = append(inputs, safewriter.Input{Path: opened[i].path, Digest: opened[i].snap.FileDigest})
		sources = append(sources, opened[i].snap)
	}
	var written *snapshot.Snapshot
	return safewriter.Run(safewriter.Plan{
		Target: output,
		Inputs: inputs,
		Encode: func(w io.Writer) error { return gateway.Write(w, merged, format, keysFrom.keys) },
		ReadBack: func(temp string) (err error) {
			written, err = gateway.ReadBack(temp, merged, format, keysFrom.keys)
			return err
		},
		Audit: func() error {
			if losses := lossaudit.Audit(lossaudit.Input{Copies: sources, Result: written}); len(losses) > 0 {
				return &lossesError{losses: losses}
			}
			return nil
		},
		Judge: outsideCheck(env),
	})
}

// outsideCheck is step 6 of the protocol, the place of the KeePassXC Judge. No outside program
// looks at the file yet, and the person has to know that.
func outsideCheck(env Env) func(temp string) error {
	return func(string) error {
		_, err := fmt.Fprintln(env.Stdout, text.MergeNoOutsideCheck)
		return err
	}
}

func reportWriteError(env Env, output string, err error) int {
	var losses *lossesError
	if errors.As(err, &losses) {
		code := fail(env, text.MergeLossesFound)
		_ = report.WriteLosses(env.Stderr, losses.losses)
		return code
	}
	return fail(env, describeWriteError(output, err))
}

// describeWriteError gives the text for the human about an error of the write protocol.
func describeWriteError(output string, err error) string {
	var changed *safewriter.InputChangedError
	var readBack *gateway.ReadBackError
	switch {
	case errors.Is(err, safewriter.ErrTargetExists):
		return text.MergeTargetExists(output)
	case errors.Is(err, safewriter.ErrTargetIsInput):
		return text.MergeTargetIsInput(output)
	case errors.Is(err, safewriter.ErrTargetDirectory):
		return text.MergeTargetDirectory(output)
	case errors.As(err, &changed):
		message := text.MergeInputChanged(changed.Path)
		if errors.Is(err, safewriter.ErrResultLeft) {
			message += " " + text.MergeResultLeft(output)
		}
		return message
	case errors.As(err, &readBack):
		return text.MergeReadBackFailed(readBack.Place)
	}
	return text.MergeWriteFailed(output)
}

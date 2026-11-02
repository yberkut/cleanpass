package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/gateway"
	// The name of the package is taken in this package: status has a function named judge.
	keepassxc "github.com/yberkut/cleanpass/internal/judge"
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

// runMerge makes a new file from the copies and never changes them. It shows the report of the
// copies, leads the person through the decisions, and ends with a summary. A question with no
// answer goes by its rule, and those rules lose nothing.
func runMerge(env Env, args []string) int {
	options, rest, problem := cutMergeOptions(args)
	if problem != "" {
		return fail(env, problem)
	}
	copies, problem := parseCopies(rest)
	if problem != "" {
		return fail(env, problem)
	}
	output := options.output
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

	dialog := session.New(env.Stdin, env.Stderr)
	opened, code, stopped := openForMerge(env, dialog, copies)
	if stopped {
		return code
	}
	related := merge.RelatedGroups(opened.snapshots())
	if len(related) > 1 && !options.allowUnrelated {
		code := fail(env, text.MergeStoppedUnrelated)
		_ = report.WriteUnrelatedGroups(env.Stderr, pathsOfGroups(related, copies))
		_, _ = fmt.Fprintln(env.Stderr, text.MergeUnrelatedOption(allowUnrelatedOption))
		return code
	}

	// One key and one moment for all runs, because the answers refer to the questions of the first.
	input := merge.Input{Copies: opened.forEngine(), Now: time.Now(), Key: merge.NewKey()}
	asked, err := merge.Run(input)
	if err != nil {
		return fail(env, text.MergeEngineFailed)
	}
	if err := writeMergeReport(env, opened, asked.Facts(), input.Now, pathsOfGroups(related, copies)); err != nil {
		return exitError
	}

	answers := dialog.Decide(asked)
	choice, err := dialog.ChooseKeys(copyPaths(copies), opened.newest())
	if err != nil {
		return fail(env, text.NewKeysNotGiven)
	}
	result := asked
	if len(answers.Decisions) > 0 {
		input.Decisions = answers.Decisions
		if result, err = merge.Run(input); err != nil {
			return fail(env, text.MergeEngineFailed)
		}
	}

	keys := opened.keysFor(choice)
	if err := writeResult(env, output, opened, result.Merged, allowedBy(result, answers.Decisions), keys); err != nil {
		return reportWriteError(env, output, err)
	}
	if err := writeMergeEnd(env, output, keys, answers, result, input); err != nil {
		return exitError
	}
	return exitOK
}

// writeMergeReport shows the state of the copies before any answer: what compare shows, without
// the next step, since this is that step.
func writeMergeReport(env Env, opened openedCopies, facts merge.Facts, now time.Time, related [][]string) error {
	passports := make([]report.Passport, 0, len(opened))
	for i := range opened {
		passport, err := passportOf(opened[i].path, opened[i].snap, now)
		if err != nil {
			return err
		}
		passports = append(passports, passport)
	}
	markFreshest(passports)
	if err := report.WritePassports(env.Stdout, passports); err != nil {
		return err
	}
	if err := report.WriteDifferences(env.Stdout, facts); err != nil {
		return err
	}
	if len(related) > 1 {
		if _, err := fmt.Fprintln(env.Stdout, "\n"+text.MergeAllowedUnrelated); err != nil {
			return err
		}
		if err := report.WriteUnrelatedGroups(env.Stdout, related); err != nil {
			return err
		}
	}
	return nil
}

// writeMergeEnd says what came of the merge. The summary comes last, so a person finds it.
func writeMergeEnd(env Env, output string, keys resultKeys, answers session.Answers, result *merge.Result, input merge.Input) error {
	lines := []string{"", text.MergeDone(output), keys.description}
	if answers.Unanswered > 0 {
		lines = append(lines, text.MergeUnanswered(answers.Unanswered))
	}
	if len(answers.Decisions) > 0 {
		lines = append(lines, text.MergeDecisionsKept)
	}
	lines = append(lines, text.MergeInputsIntact)
	for _, line := range lines {
		if _, err := fmt.Fprintln(env.Stdout, line); err != nil {
			return err
		}
	}
	if err := report.WritePairsAnswered(env.Stdout, result.Facts()); err != nil {
		return err
	}
	return report.WriteSummary(env.Stdout, result.Summary(input))
}

// mergeOptions are the options only merge has.
type mergeOptions struct {
	output         string
	allowUnrelated bool
}

// cutMergeOptions takes the options of merge out of the arguments and leaves the rest as the
// other commands read them. The word after --output is its value, whatever it looks like: a file
// can be named --a-merged.kdbx. After -- every word is a file.
func cutMergeOptions(args []string) (options mergeOptions, rest []string, problem string) {
	seen := false
	take := func(value string) string {
		if seen {
			return text.OptionTwice(outputOption)
		}
		seen, options.output = true, value
		return ""
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, hasValue := strings.CutPrefix(arg, outputOption+"=")
		switch {
		case arg == "--":
			return options, append(rest, args[i:]...), ""
		case arg == "-k" || arg == "--key-file":
			rest = append(rest, arg)
			if i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
		case arg == allowUnrelatedOption:
			options.allowUnrelated = true
		case hasValue:
			if problem := take(value); problem != "" {
				return mergeOptions{}, nil, problem
			}
		case arg == outputOption:
			if i+1 == len(args) {
				return mergeOptions{}, nil, text.OptionNeedsValue(outputOption)
			}
			i++
			if problem := take(args[i]); problem != "" {
				return mergeOptions{}, nil, problem
			}
		default:
			rest = append(rest, arg)
		}
	}
	return options, rest, ""
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
func openForMerge(env Env, dialog *session.Session, copies []session.Copy) (opened openedCopies, code int, stopped bool) {
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

func (o openedCopies) snapshots() []*snapshot.Snapshot {
	result := make([]*snapshot.Snapshot, 0, len(o))
	for i := range o {
		result = append(result, o[i].snap)
	}
	return result
}

// newest gives the position of the copy with the newest change: by default its keys, cipher and
// KDF go to the result. Of copies with the same time the first on the command line counts.
func (o openedCopies) newest() int {
	best := 0
	for i := range o {
		if o[i].snap.LastChange().After(o[best].snap.LastChange()) {
			best = i
		}
	}
	return best
}

// resultKeys is what protects the result.
type resultKeys struct {
	format snapshot.Format
	keys   gateway.Credentials
	// description says for the person what opens the result.
	description string
}

func (o openedCopies) keysFor(choice session.KeysChoice) resultKeys {
	from := &o[choice.FromCopy]
	result := resultKeys{format: gateway.OutputFormat(from.snap.Format), keys: from.keys, description: text.MergeKeysFrom(from.path)}
	if choice.IsNew {
		result.keys, result.description = choice.New, text.MergeKeysNew
	}
	return result
}

// lossesError means the Loss Audit found something missing in the written file.
type lossesError struct{ losses []lossaudit.Loss }

func (e *lossesError) Error() string { return "the audit found losses" }

// allowedBy gives what the merge lets leave its place: the groups on one path, the entries a merge
// has taken into others, now or earlier, and the entries the human deleted in this run. An entry
// the human deleted in an earlier merge is not here: the Loss Audit reads that from the copies by
// itself. The deletions of this run come from the answers and not from the engine, so a bug in the
// engine cannot allow itself to lose an entry.
func allowedBy(result *merge.Result, decisions []merge.Decision) lossaudit.Allowed {
	allowed := lossaudit.Allowed{
		Absorbed:     make(map[snapshot.UUID]snapshot.UUID, len(result.Absorbed)),
		MergedGroups: result.MergedGroups,
	}
	for _, a := range result.Absorbed {
		allowed.Absorbed[a.Absorbed] = a.Into
	}
	for _, d := range decisions {
		if id, deletes := d.DeletesEntry(); deletes {
			allowed.Deleted = append(allowed.Deleted, id)
		}
	}
	return allowed
}

// writeResult runs the write protocol. The checks look at what the file really holds, not at
// what the merge gave.
func writeResult(env Env, output string, opened openedCopies, merged *snapshot.Snapshot, allowed lossaudit.Allowed, keys resultKeys) error {
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
		Encode: func(w io.Writer) error { return gateway.Write(w, merged, keys.format, keys.keys) },
		ReadBack: func(temp string) (err error) {
			written, err = gateway.ReadBack(temp, merged, keys.format, keys.keys)
			return err
		},
		Audit: func() error {
			if losses := lossaudit.Audit(lossaudit.Input{Copies: sources, Result: written, Allowed: allowed}); len(losses) > 0 {
				return &lossesError{losses: losses}
			}
			return nil
		},
		Judge: outsideCheck(env, merged, keys.keys),
		Interrupted: func(left safewriter.Interruption) {
			if left.TempLeft != "" {
				fmt.Fprintln(env.Stderr, text.InterruptedTempLeft(left.TempLeft))
			}
			if left.Published {
				fmt.Fprintln(env.Stderr, text.InterruptedAfterPublish(output))
			}
		},
	})
}

// outsideCheck is step 6 of the protocol: the KeePassXC Judge compares what the real program sees
// in the file with the expected result. Without a program that can do that the work goes on, and
// the person has to know that no one outside looked at the file.
func outsideCheck(env Env, expected *snapshot.Snapshot, keys gateway.Credentials) func(temp string) error {
	return func(temp string) error {
		program, err := keepassxc.Find()
		if err != nil {
			_, printErr := fmt.Fprintln(env.Stdout, describeMissingJudge(err))
			return printErr
		}
		if _, err := fmt.Fprintln(env.Stderr, text.JudgeWorking); err != nil {
			return err
		}
		if err := program.Check(temp, expected, keys); err != nil {
			return err
		}
		_, err = fmt.Fprintln(env.Stdout, text.JudgeAgrees)
		return err
	}
}

func describeMissingJudge(err error) string {
	var unusable *keepassxc.UnusableError
	switch {
	case !errors.As(err, &unusable):
		return text.JudgeNotFound
	case unusable.Version != "":
		return text.JudgeTooOld(unusable.Path, unusable.Version)
	}
	return text.JudgeDoesNotRun(unusable.Path)
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
	var judged *keepassxc.MismatchError
	switch {
	case errors.As(err, &judged):
		return text.JudgeDiffers(judged.Place)
	case errors.Is(err, keepassxc.ErrCannotRead):
		return text.JudgeCannotRead
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

package cli

import (
	"crypto/sha256"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/report"
	"github.com/yberkut/cleanpass/internal/safewriter"
	"github.com/yberkut/cleanpass/internal/session"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/text"
)

func init() { register("status", runStatus) }

// The exit codes only status has.
const (
	exitLags     = 1
	exitDiverged = 2
)

// runStatus says whether the copies are the same, which one lags or that they diverged.
// It only reads, and the only other process it can start is ykman, for a copy with a slot.
func runStatus(env Env, args []string) int {
	copies, problem := parseCopies(args)
	if problem != "" {
		return fail(env, problem)
	}
	if len(copies) < 2 {
		return fail(env, text.StatusNeedsTwoCopies)
	}
	if problem := checkCopies(copies); problem != "" {
		return fail(env, problem)
	}
	files, problem := distinctFiles(copies)
	if problem != "" {
		return fail(env, problem)
	}
	if len(files) == 1 {
		return say(env, exitOK, text.StatusSameBytes)
	}

	dialog := session.New(env.Stdin, env.Stderr)
	for _, file := range files {
		c := file.toOpen()
		snap, err := dialog.Open(c)
		if err != nil {
			return fail(env, describeFileError(c.Path, err))
		}
		file.snap = snap
	}
	if _, err := findWhoHoldsAll(files, time.Now()); err != nil {
		return fail(env, text.StatusFailed)
	}
	if err := writeFieldLosses(env.Stdout, files); err != nil {
		return exitError
	}
	code, lines := verdict(files, args, copies[0].Path)
	return say(env, code, lines...)
}

// say writes the lines to stdout and gives the exit code, or the code of an error if the
// lines did not get out.
func say(env Env, code int, lines ...string) int {
	for _, line := range lines {
		if _, err := fmt.Fprintln(env.Stdout, line); err != nil {
			return exitError
		}
	}
	return code
}

// writeFieldLosses warns about each copy that holds data the tool does not see. The verdict
// rests on what the tool sees, so the warning says that too.
func writeFieldLosses(w io.Writer, files []*distinctFile) error {
	for _, file := range files {
		if len(file.snap.FieldLosses) == 0 {
			continue
		}
		for _, c := range file.copies {
			if _, err := fmt.Fprintln(w, text.StatusCopyHeader(c.Path)); err != nil {
				return err
			}
			if err := report.WriteFieldLosses(w, file.snap.FieldLosses); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w, "  "+text.StatusVerdictIsBlind); err != nil {
				return err
			}
		}
	}
	return nil
}

// verdict gives the exit code and the text after findWhoHoldsAll has run.
//
// Exit code 1 means no merge is needed: a copy holds all that the others hold, so any
// number of the others may lag, also in different ways. Exit code 2 means only a merge
// can bring together what the copies hold.
func verdict(files []*distinctFile, args []string, firstPath string) (int, []string) {
	code, lines := standing(files)
	if code == exitDiverged {
		lines = append(lines, text.StatusNextCommand, "  "+mergeCommand(args, firstPath))
	}
	return code, lines
}

// standing says how the copies stand to each other after findWhoHoldsAll has run, and what its
// exit code is. The commands that use it decide what to offer next.
func standing(files []*distinctFile) (int, []string) {
	var ahead, behind []session.Copy
	for _, file := range files {
		if file.holdsAll {
			ahead = append(ahead, file.copies...)
		} else {
			behind = append(behind, file.copies...)
		}
	}
	switch {
	case len(behind) == 0:
		return exitOK, []string{text.StatusSameContent}
	case len(ahead) == 0:
		return exitDiverged, []string{text.StatusDiverged}
	}
	var lines []string
	for _, c := range ahead {
		lines = append(lines, text.StatusAhead(c.Path))
	}
	for _, c := range behind {
		lines = append(lines, text.StatusBehind(c.Path))
	}
	for _, c := range behind {
		lines = append(lines, text.StatusDirection(ahead[0].Path, c.Path))
	}
	return exitLags, append(lines, text.StatusNoMergeNeeded)
}

// distinctFile is one content among the input files, with the copies that hold it.
type distinctFile struct {
	copies []session.Copy
	snap   *snapshot.Snapshot
	// holdsAll says the file already holds all that the copies hold together.
	holdsAll bool
}

// toOpen gives the copy to open for all of them: the same bytes need one open. A key file or a
// slot given beside any of them counts.
func (f *distinctFile) toOpen() session.Copy {
	c := f.copies[0]
	for _, other := range f.copies {
		if c.KeyFile == "" {
			c.KeyFile = other.KeyFile
		}
		if c.Slot == 0 {
			c.Slot = other.Slot
		}
	}
	return c
}

// distinctFiles sorts the copies by the bytes of their files.
func distinctFiles(copies []session.Copy) ([]*distinctFile, string) {
	var files []*distinctFile
	byContent := map[[sha256.Size]byte]*distinctFile{}
	for _, c := range copies {
		content, err := safewriter.HashFile(c.Path)
		if err != nil {
			return nil, describeFileError(c.Path, err)
		}
		file := byContent[content]
		if file == nil {
			file = &distinctFile{}
			byContent[content] = file
			files = append(files, file)
		}
		file.copies = append(file.copies, c)
	}
	return files, ""
}

// findWhoHoldsAll finds the files that already hold all that the copies hold together, and gives
// the merge of all of them. The Merge Engine decides that, so a new rule of the engine counts here
// with no change.
//
// A file holds all when a merge of all copies would bring it nothing:
//   - no data: the merge of all copies holds the same as the merge of this file alone;
//   - no decision: the merge of all copies asks nothing this file alone does not ask. A
//     question both raise, such as two similar entries inside one file, is no difference
//     between the copies. A question only the merge of all raises is one.
func findWhoHoldsAll(files []*distinctFile, now time.Time) (*merge.Result, error) {
	key := merge.NewKey()
	run := func(files ...*distinctFile) (*merge.Result, error) {
		input := merge.Input{Now: now, Key: key}
		for _, file := range files {
			input.Copies = append(input.Copies, merge.Copy{Name: file.copies[0].Path, Snapshot: file.snap})
		}
		return merge.Run(input)
	}
	all, err := run(files...)
	if err != nil {
		return nil, err
	}
	together := holdingsOf(all.Merged)
	for _, file := range files {
		alone, err := run(file)
		if err != nil {
			return nil, err
		}
		file.holdsAll = reflect.DeepEqual(holdingsOf(alone.Merged), together) &&
			asksNothingNew(all.Questions, alone.Questions) &&
			keepsMainCustomData(file.snap, all.Merged)
	}
	return all, nil
}

// asksNothingNew says that every question of the merge of all copies is also a question of
// the merge of one file.
func asksNothingNew(all, alone []merge.Question) bool {
	asked := make(map[merge.QuestionID]bool, len(alone))
	for i := range alone {
		asked[alone[i].ID] = true
	}
	for i := range all {
		if !asked[all[i].ID] {
			return false
		}
	}
	return true
}

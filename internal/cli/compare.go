package cli

import (
	"os"
	"slices"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/report"
	"github.com/yberkut/cleanpass/internal/session"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/text"
)

func init() { register("compare", runCompare) }

// runCompare shows the state of the copies: the passport of each one, what differs between them
// and the next step. It only reads. Each input is one file for the judge, also when two of them
// hold the same bytes, because the report names every copy.
func runCompare(env Env, args []string) int {
	copies, problem := parseCopies(args)
	if problem != "" {
		return fail(env, problem)
	}
	if problem := checkCopies(copies); problem != "" {
		return fail(env, problem)
	}

	dialog := session.New(env.Stdin, env.Stderr)
	passports := make([]report.Passport, 0, len(copies))
	files := make([]*distinctFile, 0, len(copies))
	for _, c := range copies {
		snap, err := dialog.Open(c)
		if err != nil {
			return fail(env, describeFileError(c.Path, err))
		}
		info, err := os.Stat(c.Path)
		if err != nil {
			return fail(env, describeFileError(c.Path, err))
		}
		passports = append(passports, report.Passport{
			Path:       c.Path,
			Size:       info.Size(),
			FileDate:   info.ModTime(),
			LastChange: snap.LastChange(),
			Stats:      snap.Stats(),
			Format:     snap.Format,

			FieldLosses: snap.FieldLosses,
		})
		files = append(files, &distinctFile{copies: []session.Copy{c}, snap: snap})
	}
	markFreshest(passports)

	now := time.Now()
	snapshots := make([]*snapshot.Snapshot, len(files))
	for i, file := range files {
		snapshots[i] = file.snap
		passports[i].FromTheFuture = stampFromTheFuture(file.snap, now)
	}
	merged, err := judge(files, now)
	if err != nil {
		return fail(env, text.StatusFailed)
	}
	facts := merged.Facts()
	related := merge.RelatedGroups(snapshots)
	if err := report.WritePassports(env.Stdout, passports); err != nil {
		return exitError
	}
	if err := report.WriteDifferences(env.Stdout, facts); err != nil {
		return exitError
	}
	if len(related) > 1 {
		if err := report.WriteUnrelated(env.Stdout, pathsOfGroups(related, copies)); err != nil {
			return exitError
		}
	}
	return say(env, exitOK, append([]string{""}, nextStep(files, args, facts, len(related) > 1)...)...)
}

// allowUnrelatedOption takes off the stop of merge on copies of different databases.
const allowUnrelatedOption = "--allow-unrelated"

// nextStep ends the report: how the copies stand, and the exact command to run next. It offers
// no command that would not work, and none when there is nothing to do.
func nextStep(files []*distinctFile, args []string, facts merge.Facts, unrelated bool) []string {
	if len(files) == 1 {
		return []string{text.CompareOneCopy}
	}
	first := files[0].copies[0].Path
	code, lines := standing(files)
	if code != exitOK && len(facts.Entries)+len(facts.Deletions)+len(facts.Pairs)+len(facts.RememberedPairs) == 0 {
		lines = append([]string{text.CompareOtherDifferences}, lines...)
	}
	switch {
	case slices.ContainsFunc(files, func(f *distinctFile) bool { return len(f.snap.FieldLosses) > 0 }):
		return append(lines, text.StatusVerdictIsBlind, text.CompareMergeWouldStop)
	case code == exitOK:
		return append(lines, text.CompareNoCommandNeeded)
	case unrelated:
		return append(lines, text.CompareOnlyIfOneDatabase, text.StatusNextCommand, "  "+mergeCommand(args, first, allowUnrelatedOption))
	case code == exitLags:
		lines = append(lines, text.CompareMergeAnyway)
	}
	return append(lines, text.StatusNextCommand, "  "+mergeCommand(args, first))
}

func pathsOfGroups(groups [][]int, copies []session.Copy) [][]string {
	result := make([][]string, len(groups))
	for i, group := range groups {
		for _, at := range group {
			result[i] = append(result[i], copies[at].Path)
		}
	}
	return result
}

// stampFromTheFuture gives the newest time stamp of the copy if it lies after now, and zero
// otherwise. A clock leaves its mark on all stamps, so this looks further than LastChange: also
// at a read and at the service data of a save, which are no change. An expiry date does not
// count, because a person sets it in the future on purpose.
func stampFromTheFuture(s *snapshot.Snapshot, now time.Time) time.Time {
	newest := s.LastChange()
	see := func(t time.Time) {
		if t.After(newest) {
			newest = t
		}
	}
	seeData := func(items []snapshot.CustomDataItem) {
		for i := range items {
			see(items[i].LastModification)
		}
	}
	seeVersion := func(v *snapshot.EntryVersion) {
		see(v.Times.LastAccess)
		seeData(v.CustomData)
	}
	seeData(s.Meta.CustomData)
	s.Root.Walk(func(g *snapshot.Group) {
		see(g.Times.LastAccess)
		seeData(g.CustomData)
		for i := range g.Entries {
			seeVersion(&g.Entries[i].EntryVersion)
			for j := range g.Entries[i].History {
				seeVersion(&g.Entries[i].History[j])
			}
		}
	})
	if newest.After(now) {
		return newest
	}
	return time.Time{}
}

// markFreshest marks every copy with the newest change inside the database. Copies
// with the same newest time are all marked. One copy has nothing to compare with.
func markFreshest(passports []report.Passport) {
	if len(passports) < 2 {
		return
	}
	var newest time.Time
	for i := range passports {
		if passports[i].LastChange.After(newest) {
			newest = passports[i].LastChange
		}
	}
	for i := range passports {
		passports[i].Freshest = !newest.IsZero() && passports[i].LastChange.Equal(newest)
	}
}

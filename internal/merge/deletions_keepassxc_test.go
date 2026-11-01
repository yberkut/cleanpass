package merge_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/testkit"
)

// These tests let the real KeePassXC do the deletion, so that the moves of the generator and
// the rules of the engine rest on what it really writes. In the rich fixture the entry Друге
// lies in the group Робота, and the group Кошик is the recycle bin.
var (
	secondEntryID = snapshot.UUID{14: 1, 15: 1}
	workGroupID   = snapshot.UUID{15: 0x20}
)

const (
	entryPath        = "/Робота/Друге"
	entryInBinPath   = "/Кошик/Друге"
	groupPath        = "/Робота"
	groupInBinPath   = "/Кошик/Робота"
	originalCopyName = "original"
	deletedCopyName  = "deleted"
)

// forkedFiles gives two files with the same bytes, as a copy to a flash drive does.
func forkedFiles(t *testing.T) (original, forked testkit.DB) {
	t.Helper()
	dir := t.TempDir()
	original = testkit.DB{Path: filepath.Join(dir, "original.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	forked = testkit.DB{Path: filepath.Join(dir, "forked.kdbx"), Password: testkit.Password}
	data, err := os.ReadFile(original.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(forked.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return original, forked
}

func mergeInput(t *testing.T, original, deleted testkit.DB) merge.Input {
	t.Helper()
	return merge.Input{
		Copies: []merge.Copy{
			{Name: originalCopyName, Snapshot: openCopy(t, original)},
			{Name: deletedCopyName, Snapshot: openCopy(t, deleted)},
		},
		// KeePassXC stamps its moves with the clock of this run. A fixed time would make them
		// come from the future.
		Now: time.Now().Add(time.Hour),
		Key: testKey,
	}
}

// keepassxc-cli 2.7.12 gives the entry a new LastModificationTime on this move, with the same
// content and no item in the history. The spec expected the time to stay. The question must
// come either way, and it must still tell the last real change.
func TestARealMoveToTheBinAsksAndTellsTheLastRealChange(t *testing.T) {
	original, deleted := forkedFiles(t)
	lastChange := allEntries(openCopy(t, original))[secondEntryID].Times.LastModification
	deleted.CLI(t, "rm", entryPath)
	in := mergeInput(t, original, deleted)

	result := mustRun(t, in)

	_, d := theDeletion(t, result)
	if d.Entry != secondEntryID {
		t.Errorf("the question is about %s, want %s", d.Entry, secondEntryID)
	}
	if !slices.Equal(d.Recycled, []string{deletedCopyName}) || !slices.Equal(d.Held, []string{originalCopyName}) || len(d.Recorded) != 0 {
		t.Errorf("recycled in %q, held in %q, recorded in %q", d.Recycled, d.Held, d.Recorded)
	}
	if !d.Modified.Equal(lastChange) {
		t.Errorf("modified = %s, want the last real change %s", d.Modified, lastChange)
	}
	if !d.Deleted.After(d.Modified) {
		t.Errorf("deleted = %s, modified = %s: the time of the move must be the later one", d.Deleted, d.Modified)
	}
	if got := groupsWithEntry(result.Merged, secondEntryID)[0].UUID; got != workGroupID {
		t.Errorf("without an answer the entry lies in %s, want %s", got, workGroupID)
	}
}

// The same answer in the real flow: the entry lies in the bin of the result.
func TestARealMoveToTheBinWithTheAnswerDeleteLeavesTheEntryInTheBin(t *testing.T) {
	original, deleted := forkedFiles(t)
	deleted.CLI(t, "rm", entryPath)
	in := mergeInput(t, original, deleted)
	q, _ := theDeletion(t, mustRun(t, in))
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: merge.AnswerDelete}}

	merged := mustRun(t, in).Merged

	if got := groupsWithEntry(merged, secondEntryID)[0].UUID; got != merged.Meta.RecycleBin {
		t.Errorf("the entry lies in %s, want the bin %s", got, merged.Meta.RecycleBin)
	}
}

func TestARealDeletionFromTheBinGivesARecordAndAsks(t *testing.T) {
	original, deleted := forkedFiles(t)
	deleted.CLI(t, "rm", entryPath)
	deleted.CLI(t, "rm", entryInBinPath)
	in := mergeInput(t, original, deleted)

	q, d := theDeletion(t, mustRun(t, in))

	if d.Entry != secondEntryID {
		t.Errorf("the question is about %s, want %s", d.Entry, secondEntryID)
	}
	if !slices.Equal(d.Recorded, []string{deletedCopyName}) || !slices.Equal(d.Held, []string{originalCopyName}) || len(d.Recycled) != 0 {
		t.Errorf("recorded in %q, held in %q, recycled in %q", d.Recorded, d.Held, d.Recycled)
	}
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: merge.AnswerDelete}}
	if allEntries(mustRun(t, in).Merged)[secondEntryID] != nil {
		t.Error("the entry stayed after the answer")
	}
}

func TestARealMoveOfAGroupToTheBinAsksAboutItsEntries(t *testing.T) {
	original, deleted := forkedFiles(t)
	deleted.CLI(t, "rmdir", groupPath)
	in := mergeInput(t, original, deleted)
	moved := openCopy(t, deleted)
	if _, parent := groupOf(moved, workGroupID); parent == nil || parent.UUID != moved.Meta.RecycleBin {
		t.Fatalf("KeePassXC did not move the group to the bin: %s", printed(parent))
	}

	result := mustRun(t, in)

	_, d := theDeletion(t, result)
	if d.Entry != secondEntryID || !slices.Equal(d.Recycled, []string{deletedCopyName}) || !slices.Equal(d.Held, []string{originalCopyName}) {
		t.Errorf("entry %s, recycled in %q, held in %q", d.Entry, d.Recycled, d.Held)
	}
	if !d.Deleted.After(d.Modified) {
		t.Errorf("deleted = %s, modified = %s: the time of the move of the group must be the later one", d.Deleted, d.Modified)
	}
	if _, parent := groupOf(result.Merged, workGroupID); parent == nil || parent.UUID != result.Merged.Root.UUID {
		t.Errorf("without an answer the group lies under %s, want the root", printed(parent))
	}
}

func TestARealDeletionOfAGroupFromTheBinGivesRecordsAboutAllItHolds(t *testing.T) {
	original, deleted := forkedFiles(t)
	deleted.CLI(t, "rmdir", groupPath)
	deleted.CLI(t, "rmdir", groupInBinPath)
	gone := openCopy(t, deleted)

	recordsAbout := func(id snapshot.UUID) bool {
		return slices.ContainsFunc(gone.Deletions, func(r snapshot.Deletion) bool { return r.UUID == id })
	}

	if !recordsAbout(workGroupID) || !recordsAbout(secondEntryID) {
		t.Errorf("records = %s, want the group and its entry", printed(gone.Deletions))
	}
	if _, d := theDeletion(t, mustRun(t, mergeInput(t, original, deleted))); d.Entry != secondEntryID {
		t.Errorf("the question is about %s, want %s", d.Entry, secondEntryID)
	}
}

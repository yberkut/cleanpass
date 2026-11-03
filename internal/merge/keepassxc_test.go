package merge_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/testkit"
)

func openCopy(t *testing.T, db testkit.DB) *snapshot.Snapshot {
	t.Helper()
	s, err := gateway.Open(db.Path, gateway.Credentials{Password: []byte(db.Password)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// countVersions counts the main versions and the history of a database.
func countVersions(s *snapshot.Snapshot) int {
	count := 0
	s.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			count += 1 + len(g.Entries[i].History)
		}
	})
	return count
}

// A plain save by the real KeePassXC renews its service data, also on entries. That must
// not look like an edit: no new version and no question.
func TestAFileSavedAgainByKeePassXCHasTheSameVersions(t *testing.T) {
	dir := t.TempDir()
	original := testkit.DB{Path: filepath.Join(dir, "original.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	saved := testkit.DB{Path: filepath.Join(dir, "saved.kdbx"), Password: testkit.Password}
	data, err := os.ReadFile(original.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(saved.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	saved.Resave(t)

	before, after := openCopy(t, original), openCopy(t, saved)
	result := mustRun(t, merge.Input{
		Copies: []merge.Copy{{Name: "original", Snapshot: before}, {Name: "saved", Snapshot: after}},
		Now:    now,
		Key:    testKey,
	})

	wantNoQuestions(t, result)
	for _, event := range result.Events {
		t.Errorf("a plain save gave the report event %s", printed(event))
	}
	if got, want := countVersions(result.Merged), countVersions(before); got != want {
		t.Errorf("the merge has %d versions, the original has %d", got, want)
	}
	if got, want := result.Merged.Stats().Groups, before.Stats().Groups+1; got != want {
		t.Errorf("the merge has %d groups, want %d with the new one", got, want)
	}
}

// These let the real KeePassXC do the edit. In the rich fixture only this entry holds custom data.
const entryWithCustomData = "/Пошта <&> \"quoted\" 😀"

var entryWithCustomDataID = snapshot.UUID{14: 1}

// The finding of ticket #10: KeePassXC leaves the custom data of the entry out of the history
// version an edit makes. If that ever changes, this test says so.
func TestARealEditLeavesCustomDataOutOfTheHistory(t *testing.T) {
	original, edited := forkedFiles(t)
	before := allEntries(openCopy(t, original))[entryWithCustomDataID]
	edited.CLI(t, "edit", "--notes", "нова нотатка", entryWithCustomData)

	after := allEntries(openCopy(t, edited))[entryWithCustomDataID]

	last := after.History[len(after.History)-1]
	if !last.Times.LastModification.Equal(before.Times.LastModification) {
		t.Fatalf("the last history version has time %s, want %s", last.Times.LastModification, before.Times.LastModification)
	}
	if got := customDataItems(&last); len(got) != 0 {
		t.Errorf("the history version has %d items of custom data, want none", len(got))
	}
	if got := customDataItems(&after.EntryVersion); len(got) != 1 {
		t.Errorf("the main version has %d items of custom data, want 1", len(got))
	}
}

func TestARealEditOfAnEntryWithCustomDataIsALag(t *testing.T) {
	original, edited := forkedFiles(t)
	edited.CLI(t, "edit", "--notes", "нова нотатка", entryWithCustomData)
	in := mergeInput(t, original, edited)
	holdings := heldEntries(in.Copies)[entryWithCustomDataID]

	result := mustRun(t, in)

	wantNoQuestions(t, result)
	merged := allEntries(result.Merged)[entryWithCustomDataID]
	wantSetOfVersions(t, merged, holdings)
	last := merged.History[len(merged.History)-1]
	if got := customDataItems(&last); len(got) != 1 {
		t.Errorf("the old version has %d items of custom data, want its 1 item", len(got))
	}
	copies := []*snapshot.Snapshot{in.Copies[0].Snapshot, in.Copies[1].Snapshot}
	if losses := lossaudit.Audit(lossaudit.Input{Copies: copies, Result: result.Merged}); len(losses) != 0 {
		t.Errorf("the audit found losses: %+v", losses)
	}
}

// Two edits in a row: the history versions of both lack the custom data, and one copy is ahead.
// They often land in the same second. Then the key puts one of them on top, and if that is the
// older one the merge asks, as it does about any main version a copy has replaced. It is never a
// conflict, because the history tells the order.
func TestTwoRealEditsInARowAreALag(t *testing.T) {
	once, _ := forkedFiles(t)
	once.CLI(t, "edit", "--notes", "перша", entryWithCustomData)
	twice := testkit.DB{Path: once.Path + ".twice.kdbx", Password: once.Password}
	data, err := os.ReadFile(once.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(twice.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	twice.CLI(t, "edit", "--notes", "друга", entryWithCustomData)
	in := mergeInput(t, once, twice)
	first := allEntries(in.Copies[0].Snapshot)[entryWithCustomDataID].Times.LastModification
	second := allEntries(in.Copies[1].Snapshot)[entryWithCustomDataID].Times.LastModification

	result := mustRun(t, in)

	if !first.Equal(second) {
		wantNoQuestions(t, result)
	}
	for _, conflict := range conflictQuestions(result.Questions) {
		if !conflict.Replaced || conflict.Close || conflict.Future {
			t.Errorf("a conflict about two edits in a row: %s", printed(conflict))
		}
	}
}

// A move to the bin by the real keepassxc-cli gives a new time on the same content. That raises
// the question about the deletion and nothing else.
func TestARealMoveToTheBinIsNoConflict(t *testing.T) {
	for name, path := range map[string]string{"without custom data": entryPath, "with custom data": entryWithCustomData} {
		t.Run(name, func(t *testing.T) {
			original, deleted := forkedFiles(t)
			deleted.CLI(t, "rm", path)

			result := mustRun(t, mergeInput(t, original, deleted))

			if conflicts := conflictQuestions(result.Questions); len(conflicts) != 0 {
				t.Errorf("a question about a conflict: %s", printed(conflicts))
			}
			if deletions := deletionQuestions(result.Questions); len(deletions) != 1 {
				t.Errorf("questions about deletions: %s", printed(deletions))
			}
			for _, event := range result.Events {
				if change, ok := event.(merge.EntryChange); ok && (change.Outcome == merge.OutcomeConflict || change.Outcome == merge.OutcomeAsked) {
					t.Errorf("the report tells a conflict: %s", printed(change))
				}
			}
		})
	}
}

func wantSetOfVersions(t *testing.T, merged *snapshot.Entry, holdings []held) {
	t.Helper()
	want := standing(versionsOfHoldings(holdings))
	got := idsOf(versionsOfEntry(merged))
	if len(got) != len(want) {
		t.Fatalf("the entry has %d versions, want %d", len(got), len(want))
	}
	for id := range want {
		if !got[id] {
			t.Errorf("the entry lacks the version %s", id)
		}
	}
}

package merge_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

var (
	goneID  = snapshot.UUID{15: 0x31}
	otherID = snapshot.UUID{15: 0x32}
)

// recorded adds records about deleted objects to a copy.
func recorded(s *snapshot.Snapshot, records ...snapshot.Deletion) *snapshot.Snapshot {
	s.Deletions = append(s.Deletions, records...)
	return s
}

// theDeletion returns the only question of the result, which must be about a deletion.
func theDeletion(t *testing.T, result *merge.Result) (merge.Question, merge.Deletion) {
	t.Helper()
	if len(result.Questions) != 1 {
		t.Fatalf("got %d questions, want 1: %s", len(result.Questions), printed(result.Questions))
	}
	q := result.Questions[0]
	detail, ok := q.Detail.(merge.Deletion)
	if !ok {
		t.Fatalf("the question has detail %T, want merge.Deletion", q.Detail)
	}
	return q, detail
}

// A record and an entry, in any order and whichever is newer: the human decides.
func TestARecordAgainstAnEntryAsksWithBothDates(t *testing.T) {
	for name, tc := range map[string]struct{ deleted, modified float64 }{
		"deleted after the last change":  {9, 2},
		"deleted before the last change": {2, 9},
	} {
		t.Run(name, func(t *testing.T) {
			deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(tc.deleted)})
			held := database(entry(version(hours(tc.modified), "kept")))

			for _, copies := range [][]merge.Copy{
				{{Name: "a", Snapshot: deleted}, {Name: "b", Snapshot: held}},
				{{Name: "b", Snapshot: held}, {Name: "a", Snapshot: deleted}},
			} {
				q, d := theDeletion(t, mustRun(t, input(copies...)))

				if q.Choices != 2 {
					t.Errorf("choices = %d, want 2", q.Choices)
				}
				if d.Entry != mailID || d.Label != (merge.Label{Title: "Mail", UserName: "user"}) {
					t.Errorf("entry = %s, label = %+v", d.Entry, d.Label)
				}
				if !d.Deleted.Equal(hours(tc.deleted)) || !d.Modified.Equal(hours(tc.modified)) {
					t.Errorf("deleted = %s, modified = %s, want %s and %s", d.Deleted, d.Modified, hours(tc.deleted), hours(tc.modified))
				}
				if !slices.Equal(d.Recorded, []string{"a"}) || !slices.Equal(d.Held, []string{"b"}) || len(d.Recycled) != 0 {
					t.Errorf("recorded in %q, held in %q, recycled in %q", d.Recorded, d.Held, d.Recycled)
				}
			}
		})
	}
}

func TestARecordInTheSameCopyAsTheEntryAsksToo(t *testing.T) {
	both := recorded(database(entry(version(hours(2), "kept"))), snapshot.Deletion{UUID: mailID, Time: hours(9)})

	theDeletion(t, mergeOf(t, both))
}

// A record that stayed next to a kept entry would ask again in the next merge, and KeePassXC
// itself would apply it in its own merge.
func TestWithoutAnAnswerTheEntryStaysAndNoRecordStandsAgainstIt(t *testing.T) {
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)}, snapshot.Deletion{UUID: goneID, Time: hours(3)})
	held := database(entry(version(hours(2), "kept"), version(hours(1), "old")))

	merged := mergeOf(t, deleted, held).Merged

	wantVersions(t, onlyEntry(t, merged), "kept", "old")
	if want := []snapshot.Deletion{{UUID: goneID, Time: hours(3)}}; !reflect.DeepEqual(merged.Deletions, want) {
		t.Errorf("records = %+v, want %+v", merged.Deletions, want)
	}
}

func TestAnEntryNoCopyHasARecordAboutIsNewAndAsksNothing(t *testing.T) {
	result := mergeOf(t, recorded(database(), snapshot.Deletion{UUID: goneID, Time: hours(9)}), database(entry(version(hours(2), "new"))))

	wantNoQuestions(t, result)
	wantVersions(t, onlyEntry(t, result.Merged), "new")
	if got := theEntryChange(t, result).Outcome; got != merge.OutcomeNew {
		t.Errorf("outcome = %v, want %v", got, merge.OutcomeNew)
	}
}

// answering runs the merge again with one answer to the only question about a deletion.
func answering(t *testing.T, choice int, copies ...merge.Copy) *merge.Result {
	t.Helper()
	in := input(copies...)
	q, _ := theDeletion(t, mustRun(t, in))
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: choice}}
	return mustRun(t, in)
}

func TestTheAnswerDeleteRemovesTheEntryWithItsAttachmentsAndKeepsTheRecord(t *testing.T) {
	content := []byte("bytes of the deleted entry")
	id := snapshot.NewAttachmentID(content)
	held := version(hours(2), "gone")
	held.Attachments = []snapshot.AttachmentRef{{Name: "a.bin", ID: id}}
	withEntry := database(entry(held))
	withEntry.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{id: {Content: content}}

	merged := answering(t, merge.AnswerDelete, named(recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)}), withEntry)...).Merged

	if stats := merged.Stats(); stats.Entries != 0 || stats.Attachments != 0 {
		t.Errorf("stats = %+v, want no entries and no attachments", stats)
	}
	if len(merged.Attachments) != 0 {
		t.Errorf("the bytes of %d attachments stayed", len(merged.Attachments))
	}
	if want := []snapshot.Deletion{{UUID: mailID, Time: hours(9)}}; !reflect.DeepEqual(merged.Deletions, want) {
		t.Errorf("records = %+v, want %+v", merged.Deletions, want)
	}
}

func TestTheAnswerKeepGivesWhatNoAnswerGives(t *testing.T) {
	copies := named(recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)}), database(entry(version(hours(2), "kept"))))

	kept := answering(t, merge.AnswerKeep, copies...).Merged
	silent := mustRun(t, input(copies...)).Merged

	if !reflect.DeepEqual(kept, silent) {
		t.Errorf("the answer changed the result:\n%s\n%s", printed(kept), printed(silent))
	}
}

// The memory of decisions finds a question by its ID in a later run, with another key, other
// copies and another time.
func TestTheQuestionKeepsItsIDInEveryRun(t *testing.T) {
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)})
	held := database(entry(version(hours(2), "kept")))
	want := merge.QuestionID("deletion:" + mailID.String())

	for _, in := range []merge.Input{
		{Copies: named(deleted, held), Now: now, Key: testKey},
		{Copies: named(held, deleted), Now: now.Add(day), Key: merge.Key{9}},
		{Copies: named(database(entry(version(hours(50), "edited"))), recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(70)})), Now: now, Key: testKey},
	} {
		if q, _ := theDeletion(t, mustRun(t, in)); q.ID != want {
			t.Errorf("ID = %q, want %q", q.ID, want)
		}
	}
}

// The Loss Audit reads what the human said from the decisions, not from what the engine did.
func TestOnlyTheAnswerDeleteAllowsAnEntryToGo(t *testing.T) {
	copies := named(recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)}), database(entry(version(hours(2), "gone"))))
	q, _ := theDeletion(t, mustRun(t, input(copies...)))

	if got, ok := (merge.Decision{Question: q.ID, Choice: merge.AnswerDelete}).DeletesEntry(); !ok || got != mailID {
		t.Errorf("the answer delete allows %s, %v, want %s", got, ok, mailID)
	}
	conflict, _ := theConflict(t, mergeOf(t, twoDivergedCopies(0)...))
	for name, d := range map[string]merge.Decision{
		"the answer keep":        {Question: q.ID, Choice: merge.AnswerKeep},
		"a choice out of range":  {Question: q.ID, Choice: 2},
		"an answer on a version": {Question: conflict.ID, Choice: merge.AnswerDelete},
		"a short ID":             {Question: "deletion:0102", Choice: merge.AnswerDelete},
		"a long ID":              {Question: q.ID + "00", Choice: merge.AnswerDelete},
		"an ID that is no UUID":  {Question: "deletion:zz000000000000000000000000000000", Choice: merge.AnswerDelete},
	} {
		if got, ok := d.DeletesEntry(); ok {
			t.Errorf("%s allows %s to go", name, got)
		}
	}
}

// An entry with a record is not new. An entry that is gone has only the event about that.
func TestTheReportTellsWhatHappenedAndNothingElse(t *testing.T) {
	older := version(hours(1), "older")
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)})

	for name, tc := range map[string]struct {
		copies []*snapshot.Snapshot
		answer int
		want   merge.DeletionOutcome
	}{
		"no answer": {[]*snapshot.Snapshot{deleted, database(entry(older))}, -1, merge.DeletionOpen},
		"keep":      {[]*snapshot.Snapshot{deleted, database(entry(older))}, merge.AnswerKeep, merge.DeletionRejected},
		"delete":    {[]*snapshot.Snapshot{deleted, database(entry(older))}, merge.AnswerDelete, merge.DeletionApplied},
		"delete an entry another copy has updated": {
			[]*snapshot.Snapshot{deleted, database(entry(older)), database(entry(version(hours(2), "newer"), older))},
			merge.AnswerDelete, merge.DeletionApplied,
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := input(named(tc.copies...)...)
			if tc.answer >= 0 {
				q, _ := theDeletion(t, mustRun(t, in))
				in.Decisions = []merge.Decision{{Question: q.ID, Choice: tc.answer}}
			}

			result := mustRun(t, in)

			if len(result.Report) != 1 {
				t.Fatalf("got %d events, want 1: %s", len(result.Report), printed(result.Report))
			}
			event, ok := result.Report[0].(merge.DeletionChange)
			if !ok {
				t.Fatalf("the event is %T, want merge.DeletionChange", result.Report[0])
			}
			if event.Outcome != tc.want || event.Entry != mailID || event.Label.Title != "Mail" {
				t.Errorf("event = %s, want outcome %v", printed(event), tc.want)
			}
		})
	}
}

// A real change of a kept entry stays in the report next to the question about it.
func TestAKeptEntryKeepsItsChangeInTheReport(t *testing.T) {
	older := version(hours(1), "older")
	result := mergeOf(t,
		recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)}),
		database(entry(older)),
		database(entry(version(hours(2), "newer"), older)),
	)

	if len(result.Report) != 2 {
		t.Fatalf("got %d events, want 2: %s", len(result.Report), printed(result.Report))
	}
	for _, event := range result.Report {
		if change, ok := event.(merge.EntryChange); ok && change.Outcome != merge.OutcomeUpdated {
			t.Errorf("outcome = %v, want %v", change.Outcome, merge.OutcomeUpdated)
		}
	}
}

// Both questions come with their own answers. A conflict about an entry that goes away is
// still a question with an answer: the decision about it is not unknown.
func TestAnEntryInConflictAndDeletedHasBothQuestions(t *testing.T) {
	copies := []*snapshot.Snapshot{
		recorded(database(entry(version(hours(1), "one"))), snapshot.Deletion{UUID: mailID, Time: hours(9)}),
		database(entry(version(hours(2), "two"))),
	}
	in := input(named(copies...)...)
	asked := mustRun(t, in).Questions
	if len(asked) != 2 {
		t.Fatalf("got %d questions, want 2: %s", len(asked), printed(asked))
	}
	for _, q := range asked {
		in.Decisions = append(in.Decisions, merge.Decision{Question: q.ID, Choice: 1})
	}

	result := mustRun(t, in)

	if got := result.Merged.Stats().Entries; got != 0 {
		t.Errorf("%d entries left, want none", got)
	}
	if len(result.Questions) != 2 {
		t.Errorf("got %d questions after the answers, want the same 2", len(result.Questions))
	}
}

// A decision on another question adds a version. The question about the deletion stays what it
// was, because a question is the same whether answered or not.
func TestADecisionOnAConflictDoesNotChangeTheQuestionAboutTheDeletion(t *testing.T) {
	copies := []*snapshot.Snapshot{
		recorded(database(entry(version(hours(1), "one"))), snapshot.Deletion{UUID: mailID, Time: hours(9)}),
		database(entry(version(hours(2), "two"))),
	}
	in := input(named(copies...)...)
	before := mustRun(t, in).Questions
	for _, q := range before {
		if _, isConflict := q.Detail.(merge.Conflict); isConflict {
			in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}
		}
	}

	after := mustRun(t, in).Questions

	if !reflect.DeepEqual(after, before) {
		t.Errorf("the questions changed:\n%s\n%s", printed(after), printed(before))
	}
}

var binID = snapshot.UUID{15: 0xB1}

// withBin is a copy with the groups Home and Work, a recycle bin and one entry in the given
// group, if it has an entry.
func withBin(group snapshot.UUID, entries ...snapshot.Entry) *snapshot.Snapshot {
	groups := []snapshot.Group{{UUID: homeID, Name: "Home"}, {UUID: workID, Name: "Work"}, {UUID: binID, Name: "Bin"}}
	for i := range groups {
		if groups[i].UUID == group {
			groups[i].Entries = entries
		}
	}
	return &snapshot.Snapshot{
		Meta: snapshot.Meta{RecycleBinEnabled: true, RecycleBin: binID, RecycleBinChanged: hours(0)},
		Root: snapshot.Group{UUID: rootID, Name: "Root", Groups: groups},
	}
}

// movedAt is the one version of the entry, last moved at the given time. A move to the recycle
// bin changes this time and not the time of the last change.
func movedAt(located float64) snapshot.Entry {
	return movedFrom(located, nil)
}

func movedFrom(located float64, previous *snapshot.UUID) snapshot.Entry {
	v := version(hours(1), "same")
	v.Times.LocationChanged = hours(located)
	v.PreviousParentGroup = previous
	return entry(v)
}

func TestAnEntryInTheBinAgainstAnEntryOutsideItAsks(t *testing.T) {
	result := mustRun(t, input(
		merge.Copy{Name: "a", Snapshot: withBin(binID, movedFrom(9, &homeID))},
		merge.Copy{Name: "b", Snapshot: withBin(homeID, movedAt(3))},
	))

	q, d := theDeletion(t, result)
	if q.Choices != 2 || q.ID != merge.QuestionID("deletion:"+mailID.String()) {
		t.Errorf("choices = %d, ID = %q", q.Choices, q.ID)
	}
	if !d.Deleted.Equal(hours(9)) || !d.Modified.Equal(hours(1)) {
		t.Errorf("deleted = %s, modified = %s, want %s and %s", d.Deleted, d.Modified, hours(9), hours(1))
	}
	if !slices.Equal(d.Recycled, []string{"a"}) || !slices.Equal(d.Held, []string{"b"}) || len(d.Recorded) != 0 {
		t.Errorf("recycled in %q, held in %q, recorded in %q", d.Recycled, d.Held, d.Recorded)
	}
}

func TestWithoutAnAnswerAnEntryInTheBinStaysOutsideIt(t *testing.T) {
	inBin := withBin(binID, movedFrom(9, &homeID))
	outside := withBin(homeID, movedAt(3))

	for _, order := range [][]*snapshot.Snapshot{{inBin, outside}, {outside, inBin}} {
		merged := mergeOf(t, order...).Merged

		if got := groupOfOnlyEntry(t, merged); got != homeID {
			t.Errorf("the entry lies in %s, want %s", got, homeID)
		}
		kept := onlyEntry(t, merged)
		if !kept.Times.LocationChanged.Equal(hours(3)) || kept.PreviousParentGroup != nil {
			t.Errorf("location changed = %s, previous parent = %v, want the place outside the bin", kept.Times.LocationChanged, kept.PreviousParentGroup)
		}
		if len(merged.Deletions) != 0 {
			t.Errorf("records = %+v, want none", merged.Deletions)
		}
	}
}

// The move to the bin must leave no trace in a kept entry. Otherwise its time would beat a later
// merge with a copy that has really moved the entry, and the result would depend on the parts.
func TestAKeptEntryIsPlacedAsIfTheMoveToTheBinNeverHappened(t *testing.T) {
	inBin := withBin(binID, movedFrom(9, &homeID))
	inHome := withBin(homeID, movedAt(3))
	inWork := withBin(workID, movedFrom(5, &homeID))

	atOnce := mergeOf(t, inBin, inHome, inWork).Merged
	inParts := mergeOf(t, mergeOf(t, inBin, inHome).Merged, inWork).Merged

	if got := groupOfOnlyEntry(t, atOnce); got != workID {
		t.Errorf("at once the entry lies in %s, want %s", got, workID)
	}
	if !reflect.DeepEqual(inParts.Root, atOnce.Root) {
		t.Errorf("in parts the tree differs:\n%s\n%s", printed(inParts.Root), printed(atOnce.Root))
	}
}

// A version only the copy with the bin has stays, but it has no say about the place.
func TestAVersionOnlyInTheBinKeepsNoPlaceOfItsOwn(t *testing.T) {
	base := version(hours(1), "base")
	base.Times.LocationChanged = hours(3)
	edited := version(hours(2), "edited in the bin")
	edited.Times.LocationChanged = hours(9)

	merged := onlyEntry(t, mergeOf(t, withBin(binID, entry(edited, base)), withBin(homeID, entry(base))).Merged)

	wantVersions(t, merged, "edited in the bin", "base")
	if !merged.Times.LocationChanged.Equal(hours(3)) {
		t.Errorf("location changed = %s, want %s", merged.Times.LocationChanged, hours(3))
	}
}

func TestTheAnswerDeleteLeavesAnEntryFromTheBinInTheBinWithAllItsVersions(t *testing.T) {
	old := version(hours(1), "old")
	inBin := version(hours(2), "in the bin")
	inBin.Times.LocationChanged = hours(5)
	outside := version(hours(40), "outside") // a day later: no question about the conflict
	outside.Times.LocationChanged = hours(48)
	copies := named(withBin(binID, entry(inBin, old)), withBin(homeID, entry(outside, old)))

	merged := answering(t, merge.AnswerDelete, copies...).Merged

	if got := groupOfOnlyEntry(t, merged); got != binID {
		t.Errorf("the entry lies in %s, want the bin", got)
	}
	wantVersions(t, onlyEntry(t, merged), "outside", "old", "in the bin")
	if len(merged.Deletions) != 0 {
		t.Errorf("records = %+v, want none: the entry still exists", merged.Deletions)
	}
	if got := onlyEntry(t, merged).Times.LocationChanged; !got.Equal(hours(5)) {
		t.Errorf("location changed = %s, want the move to the bin %s", got, hours(5))
	}
}

func TestAnEntryInTheBinOfEveryCopyThatHoldsItAsksNothing(t *testing.T) {
	result := mergeOf(t, withBin(binID, movedAt(9)), withBin(binID, movedAt(5)), withBin(homeID))

	wantNoQuestions(t, result)
	if got := groupOfOnlyEntry(t, result.Merged); got != binID {
		t.Errorf("the entry lies in %s, want the bin", got)
	}
}

func TestARecordAgainstAnEntryInTheBinAsksAndWithoutAnAnswerItStaysInTheBin(t *testing.T) {
	copies := named(recorded(withBin(homeID), snapshot.Deletion{UUID: mailID, Time: hours(9)}), withBin(binID, movedAt(5)))

	result := mustRun(t, input(copies...))

	_, d := theDeletion(t, result)
	if !slices.Equal(d.Recorded, []string{"copy-1"}) || !slices.Equal(d.Recycled, []string{"copy-2"}) || len(d.Held) != 0 {
		t.Errorf("recorded in %q, recycled in %q, held in %q", d.Recorded, d.Recycled, d.Held)
	}
	if got := groupOfOnlyEntry(t, result.Merged); got != binID {
		t.Errorf("the entry lies in %s, want the bin", got)
	}
	if got := answering(t, merge.AnswerDelete, copies...).Merged.Stats().Entries; got != 0 {
		t.Errorf("%d entries after the answer, want none", got)
	}
}

// A record, a copy with the entry in the bin and a copy with the entry outside: one entry, one
// question, and the deletion that went furthest counts.
func TestARecordAndAMoveToTheBinGiveOneQuestion(t *testing.T) {
	copies := named(
		recorded(withBin(homeID), snapshot.Deletion{UUID: mailID, Time: hours(4)}),
		withBin(binID, movedAt(9)),
		withBin(workID, movedAt(3)),
	)

	result := mustRun(t, input(copies...))

	_, d := theDeletion(t, result)
	if !d.Deleted.Equal(hours(9)) {
		t.Errorf("deleted = %s, want the newest of the record and the move: %s", d.Deleted, hours(9))
	}
	if got := groupOfOnlyEntry(t, result.Merged); got != workID {
		t.Errorf("without an answer the entry lies in %s, want %s", got, workID)
	}
	if got := answering(t, merge.AnswerDelete, copies...).Merged.Stats().Entries; got != 0 {
		t.Errorf("%d entries after the answer, want none", got)
	}
}

// KeePassXC itself looks at the group of the bin alone. Switching the bin off keeps what
// was deleted before.
func TestABinThatIsSwitchedOffStillCounts(t *testing.T) {
	off := withBin(binID, movedAt(9))
	off.Meta.RecycleBinEnabled = false

	theDeletion(t, mergeOf(t, off, withBin(homeID, movedAt(3))))
}

// withHomeInBin is a copy that has moved the group Home with its entries to the recycle bin.
func withHomeInBin(located float64, entries ...snapshot.Entry) *snapshot.Snapshot {
	s := withBin(workID)
	s.Root.Groups[2].Groups = []snapshot.Group{{
		UUID: homeID, Name: "Home", Entries: entries,
		Times:               snapshot.Times{LocationChanged: hours(located)},
		PreviousParentGroup: &rootID,
	}}
	s.Root.Groups = s.Root.Groups[1:]
	return s
}

// groupOf finds a group of a merged tree and its parent.
func groupOf(s *snapshot.Snapshot, id snapshot.UUID) (group, parent *snapshot.Group) {
	s.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Groups {
			if g.Groups[i].UUID == id {
				group, parent = &g.Groups[i], g
			}
		}
	})
	return group, parent
}

// Deleting a group in KeePassXC moves the group. The entry itself keeps its time and its place.
func TestAnEntryInAGroupInTheBinCountsAsInTheBin(t *testing.T) {
	result := mergeOf(t, withHomeInBin(9, movedAt(3)), withBin(homeID, movedAt(3)))

	_, d := theDeletion(t, result)
	if !d.Deleted.Equal(hours(9)) {
		t.Errorf("deleted = %s, want the move of the group %s", d.Deleted, hours(9))
	}
	if !slices.Equal(d.Recycled, []string{"copy-1"}) || !slices.Equal(d.Held, []string{"copy-2"}) {
		t.Errorf("recycled in %q, held in %q", d.Recycled, d.Held)
	}
}

func TestAGroupACopyHoldsOutsideTheBinStaysOutsideIt(t *testing.T) {
	inBin := withHomeInBin(9, movedAt(3))
	outside := withBin(homeID, movedAt(3))

	for _, order := range [][]*snapshot.Snapshot{{inBin, outside}, {outside, inBin}} {
		merged := mergeOf(t, order...).Merged

		home, parent := groupOf(merged, homeID)
		if home == nil || parent.UUID != rootID {
			t.Fatalf("Home lies under %+v, want the root", parent)
		}
		if !home.Times.LocationChanged.IsZero() || home.PreviousParentGroup != nil {
			t.Errorf("Home has the place of the copy with the bin: %s, %v", home.Times.LocationChanged, home.PreviousParentGroup)
		}
		if got := groupOfOnlyEntry(t, merged); got != homeID {
			t.Errorf("the entry lies in %s, want Home", got)
		}
	}
}

// The fields of the group still come from its newest version.
func TestAGroupInTheBinStillGivesItsFields(t *testing.T) {
	inBin := withHomeInBin(9)
	home, _ := groupOf(inBin, homeID)
	home.Name = "Renamed"
	home.Times.LastModification = hours(4)

	merged := mergeOf(t, inBin, withBin(homeID)).Merged

	if got, _ := groupOf(merged, homeID); got.Name != "Renamed" {
		t.Errorf("Home has the name %q", got.Name)
	}
}

func TestAGroupInTheBinOfEveryCopyThatHoldsItStaysThere(t *testing.T) {
	for name, other := range map[string]*snapshot.Snapshot{
		"also in the bin": withHomeInBin(5),
		"not in the copy": database(),
	} {
		t.Run(name, func(t *testing.T) {
			result := mergeOf(t, withHomeInBin(9, movedAt(3)), other)

			wantNoQuestions(t, result)
			if _, parent := groupOf(result.Merged, homeID); parent == nil || parent.UUID != binID {
				t.Errorf("Home lies under another group, want the bin: %s", printed(parent))
			}
			if got := groupOfOnlyEntry(t, result.Merged); got != homeID {
				t.Errorf("the entry lies in %s, want Home in the bin", got)
			}
		})
	}
}

// The group stays, so the entry has to leave it to be in the bin.
func TestTheAnswerDeletePutsTheEntryInTheBinWhenItsGroupStaysOutside(t *testing.T) {
	merged := answering(t, merge.AnswerDelete, named(withHomeInBin(9, movedAt(3)), withBin(homeID, movedAt(3)))...).Merged

	if got := groupOfOnlyEntry(t, merged); got != binID {
		t.Errorf("the entry lies in %s, want the bin", got)
	}
	if _, parent := groupOf(merged, homeID); parent == nil || parent.UUID != rootID {
		t.Errorf("Home lies under %+v, want the root", parent)
	}
}

// One copy deleted the group with an entry only it has, another copy keeps the group. Nobody
// wanted that entry back, and nobody needs to answer: it stays deleted.
func TestAnEntryOnlyInADeletedGroupStaysInTheBinWhenTheGroupStaysOutside(t *testing.T) {
	inBin := withHomeInBin(9, movedAt(3))
	outside := withBin(homeID)

	for _, order := range [][]*snapshot.Snapshot{{inBin, outside}, {outside, inBin}} {
		result := mergeOf(t, order...)

		wantNoQuestions(t, result)
		if got := groupOfOnlyEntry(t, result.Merged); got != binID {
			t.Errorf("the entry lies in %s, want the bin", got)
		}
	}
}

// A settled place holds: the old copy with the deleted group does not move the entry back.
func TestAnEntryMovedOutOfADeletedGroupStaysWhereItIsInTheNextMerge(t *testing.T) {
	inBin := withHomeInBin(9, movedAt(3))
	merged := mergeOf(t, inBin, withBin(homeID)).Merged

	again := mergeOf(t, merged, inBin)

	wantNoQuestions(t, again)
	if !reflect.DeepEqual(again.Merged, merged) {
		t.Errorf("the merge with the old copy changed the result:\n%s\n%s", printed(again.Merged), printed(merged))
	}
}

func TestARecordAboutAGroupThatStaysIsGone(t *testing.T) {
	deleted := recorded(database(), snapshot.Deletion{UUID: homeID, Time: hours(9)}, snapshot.Deletion{UUID: workID, Time: hours(9)})
	held := &snapshot.Snapshot{Root: snapshot.Group{UUID: rootID, Name: "Root", Groups: []snapshot.Group{{UUID: homeID, Name: "Home"}}}}

	result := mergeOf(t, deleted, held)

	wantNoQuestions(t, result)
	if home, _ := groupOf(result.Merged, homeID); home == nil {
		t.Error("the group is gone")
	}
	if want := []snapshot.Deletion{{UUID: workID, Time: hours(9)}}; !reflect.DeepEqual(result.Merged.Deletions, want) {
		t.Errorf("records = %+v, want %+v", result.Merged.Deletions, want)
	}
}

func TestTheBinOfTheResult(t *testing.T) {
	other := snapshot.UUID{15: 0xB2}
	bin := func(id snapshot.UUID, changed float64) snapshot.Meta {
		return snapshot.Meta{RecycleBin: id, RecycleBinChanged: hours(changed)}
	}
	withMeta := func(meta snapshot.Meta, groups ...snapshot.UUID) *snapshot.Snapshot {
		s := database()
		s.Meta = meta
		for _, id := range groups {
			s.Root.Groups = append(s.Root.Groups, snapshot.Group{UUID: id, Name: "Bin"})
		}
		return s
	}

	for name, tc := range map[string]struct {
		copies      []*snapshot.Snapshot
		want        snapshot.UUID
		wantChanged float64
	}{
		"the same bin takes the newest time": {
			[]*snapshot.Snapshot{withMeta(bin(binID, 1), binID), withMeta(bin(binID, 2), binID)}, binID, 2,
		},
		"the newest of two bins": {
			[]*snapshot.Snapshot{withMeta(bin(binID, 1), binID), withMeta(bin(other, 2), other)}, other, 2,
		},
		"the same time takes the smallest UUID": {
			[]*snapshot.Snapshot{withMeta(bin(other, 2), other), withMeta(bin(binID, 2), binID)}, binID, 2,
		},
		"a copy with newer settings and no bin takes nothing away": {
			[]*snapshot.Snapshot{withMeta(bin(binID, 1), binID), withMeta(snapshot.Meta{SettingsChanged: hours(5), RecycleBinChanged: hours(7)})}, binID, 1,
		},
		"a bin no copy holds loses to a bin that exists": {
			[]*snapshot.Snapshot{withMeta(bin(binID, 1), binID), withMeta(bin(other, 7))}, binID, 1,
		},
		"without a bin in the tree the newest time decides": {
			[]*snapshot.Snapshot{withMeta(bin(binID, 1)), withMeta(bin(other, 7))}, other, 7,
		},
		"no copy has a bin": {
			[]*snapshot.Snapshot{withMeta(snapshot.Meta{}), database()}, snapshot.UUID{}, -1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, copies := range [][]*snapshot.Snapshot{tc.copies, {tc.copies[1], tc.copies[0]}} {
				meta := mergeOf(t, copies...).Merged.Meta

				if meta.RecycleBin != tc.want {
					t.Errorf("bin = %s, want %s", meta.RecycleBin, tc.want)
				}
				if tc.wantChanged >= 0 && !meta.RecycleBinChanged.Equal(hours(tc.wantChanged)) {
					t.Errorf("bin changed = %s, want %s", meta.RecycleBinChanged, hours(tc.wantChanged))
				}
			}
		})
	}
}

// Two copies that made their own bin. Joining them into one group comes with the rules of the
// groups. Until then, nothing in a bin comes back to life.
func TestAnEntryInTwoDifferentBinsStaysInTheBinOfTheResult(t *testing.T) {
	other := snapshot.UUID{15: 0xB2}
	second := withBin(workID)
	second.Meta.RecycleBin, second.Meta.RecycleBinChanged = other, hours(5)
	second.Root.Groups[2] = snapshot.Group{UUID: other, Name: "Bin", Entries: []snapshot.Entry{movedAt(1)}}

	for _, order := range [][]*snapshot.Snapshot{{withBin(binID, movedAt(3)), second}, {second, withBin(binID, movedAt(3))}} {
		result := mergeOf(t, order...)

		wantNoQuestions(t, result)
		if result.Merged.Meta.RecycleBin != other {
			t.Fatalf("bin = %s, want %s", result.Merged.Meta.RecycleBin, other)
		}
		if got := groupOfOnlyEntry(t, result.Merged); got != other {
			t.Errorf("the entry lies in %s, want the bin of the result %s", got, other)
		}
	}
}

// A root group is no bin, whatever a damaged file says: everything would count as deleted.
func TestARootGroupIsNoBin(t *testing.T) {
	broken := withBin(homeID, movedAt(3))
	broken.Meta.RecycleBin = rootID

	result := mergeOf(t, broken, withBin(homeID, movedAt(3)))

	wantNoQuestions(t, result)
	if got := groupOfOnlyEntry(t, result.Merged); got != homeID {
		t.Errorf("the entry lies in %s, want Home", got)
	}
}

func TestRecordsJoinByUUIDWithTheNewestTime(t *testing.T) {
	older := recorded(database(), snapshot.Deletion{UUID: otherID, Time: hours(1)}, snapshot.Deletion{UUID: goneID, Time: hours(3)})
	newer := recorded(database(), snapshot.Deletion{UUID: goneID, Time: hours(7)})

	for _, order := range [][]*snapshot.Snapshot{{older, newer}, {newer, older}} {
		got := mergeOf(t, order...).Merged.Deletions

		want := []snapshot.Deletion{{UUID: goneID, Time: hours(7)}, {UUID: otherID, Time: hours(1)}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("records = %+v, want %+v", got, want)
		}
	}
}

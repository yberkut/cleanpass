package merge_test

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// memoryItems gives the items of the memory of decisions: all custom data a test did not set.
func memoryItems(s *snapshot.Snapshot) []snapshot.CustomDataItem {
	var items []snapshot.CustomDataItem
	for _, item := range s.Meta.CustomData {
		if strings.HasPrefix(item.Key, "cleanpass/") {
			items = append(items, item)
		}
	}
	return items
}

// A UUID is 32 hexadecimal digits. A date is RFC 3339 in UTC.
var (
	uuidOrDate = regexp.MustCompile(`^([0-9a-f]{32}|\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z)$`)
	date       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$`)
)

// reporter is what a plain test and a property have in common.
type reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// wantOnlyUUIDsAndDates checks each item: a kind of decision, UUIDs and dates in the key, and a
// date as the value.
func wantOnlyUUIDsAndDates(t reporter, s *snapshot.Snapshot) {
	t.Helper()
	kinds := []string{"entries", "groups", "different", "rejected", "accepted", "replaced"}
	for _, item := range memoryItems(s) {
		parts := strings.Split(item.Key, "/")
		if len(parts) < 3 || !slices.Contains(kinds, parts[1]) {
			t.Errorf("the key %q is no decision", item.Key)
			continue
		}
		for _, part := range parts[2:] {
			if !uuidOrDate.MatchString(part) {
				t.Errorf("the key %q holds %q, which is no UUID and no date", item.Key, part)
			}
		}
		if !date.MatchString(item.Value) {
			t.Errorf("the item %q has the value %q, which is no date", item.Key, item.Value)
		}
	}
}

// answerTheOnly merges the copies with an answer to the one question with this kind of Detail.
func answerTheOnly[D any](t *testing.T, choice int, copies ...merge.Copy) *merge.Result {
	t.Helper()
	in := input(copies...)
	for _, q := range mustRun(t, in).Questions {
		if _, ok := q.Detail.(D); ok {
			in.Decisions = append(in.Decisions, merge.Decision{Question: q.ID, Choice: choice})
		}
	}
	if len(in.Decisions) != 1 {
		t.Fatalf("%d questions of this kind, want 1", len(in.Decisions))
	}
	return mustRun(t, in)
}

// againWith merges the result with the old copy, later, and in both orders.
func againWith(t *testing.T, result *merge.Result, old *snapshot.Snapshot, check func(t *testing.T, again *merge.Result)) {
	t.Helper()
	own, other := merge.Copy{Name: "result", Snapshot: result.Merged}, merge.Copy{Name: "old", Snapshot: old}
	for _, copies := range [][]merge.Copy{{own, other}, {other, own}} {
		in := input(copies...)
		in.Now = now.Add(24 * time.Hour)
		check(t, mustRun(t, in))
	}
}

func wantSameData(t *testing.T, got, want *snapshot.Snapshot) {
	t.Helper()
	if !reflect.DeepEqual(got.Root, want.Root) || !reflect.DeepEqual(got.Deletions, want.Deletions) || !reflect.DeepEqual(got.Meta, want.Meta) {
		t.Errorf("the data changed:\n got %s\nwant %s", printed(got), printed(want))
	}
}

func TestAPairTheHumanMergedDoesNotComeBackWithAnOldCopy(t *testing.T) {
	old := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))

	result := answerTheOnlyPair(t, 1, old)
	wantOnlyUUIDsAndDates(t, result.Merged)

	againWith(t, result, old, func(t *testing.T, again *merge.Result) {
		wantNoQuestions(t, again)
		wantSameData(t, again.Merged, result.Merged)
		if want := []merge.Absorption{{Absorbed: secondID, Into: firstID}}; !reflect.DeepEqual(again.Absorbed, want) {
			t.Errorf("absorbed = %+v, want %+v", again.Absorbed, want)
		}
	})
}

// A copy that still holds an entry under its old UUID gets a word in the report: the block of the
// entry that took it in would have no reason otherwise. Its line about that copy tells what the
// copy holds, because a copy with the old UUID does hold the account.
func TestAnOldCopyWithAMergedEntryGetsAnEventAndARightLine(t *testing.T) {
	old := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))
	result := answerTheOnlyPair(t, 1, old)
	onlySecond := database(account(secondID, "Mail", "user", "", "two"))

	for _, copies := range [][]merge.Copy{
		{{Name: "result", Snapshot: result.Merged}, {Name: "old", Snapshot: onlySecond}},
		{{Name: "old", Snapshot: onlySecond}, {Name: "result", Snapshot: result.Merged}},
	} {
		again := mustRun(t, input(copies...))

		want := merge.PairRemembered{
			Entry:    firstID,
			Label:    merge.Label{Title: "Mail", UserName: "user"},
			Absorbed: []snapshot.UUID{secondID},
			Copies:   []string{"old"},
		}
		if !slices.ContainsFunc(again.Events, func(e merge.Event) bool { return reflect.DeepEqual(e, want) }) {
			t.Errorf("report = %s, want %+v", printed(again.Events), want)
		}
		for _, event := range again.Events {
			change, ok := event.(merge.EntryChange)
			if !ok {
				continue
			}
			for _, c := range change.Copies {
				if !c.Holds {
					t.Errorf("the report says %s does not hold the entry: %+v", c.Name, change)
				}
			}
			if change.Outcome == merge.OutcomeNew {
				t.Errorf("the report calls the entry new in a part of the copies: %+v", change)
			}
		}
	}
}

// With no old UUID in any copy there is nothing to say: the memory alone is no news.
func TestAPairOfAnEarlierMergeIsNoNewsWithoutAnOldCopy(t *testing.T) {
	old := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))
	result := answerTheOnlyPair(t, 1, old)

	again := mergeOf(t, result.Merged)

	for _, event := range again.Events {
		t.Errorf("the report tells %s", printed(event))
	}
}

// An entry the memory removes takes its event with it: the event about the deletion tells enough.
func TestAPairOfADeletedEntryGivesNoEvent(t *testing.T) {
	both := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))
	merged := answerTheOnlyPair(t, 1, both).Merged
	deleted := recorded(database(), snapshot.Deletion{UUID: firstID, Time: hours(9)})
	result := answerTheOnly[merge.Deletion](t, merge.AnswerDelete, named(merged, deleted)...)

	again := mergeOf(t, result.Merged, database(account(secondID, "Mail", "user", "", "two")))

	for _, event := range again.Events {
		if _, ok := event.(merge.PairRemembered); ok {
			t.Errorf("the report tells %+v", event)
		}
	}
}

// answerTheOnlyPair answers the only question about one pair of these copies.
func answerTheOnlyPair(t *testing.T, choice int, snapshots ...*snapshot.Snapshot) *merge.Result {
	t.Helper()
	return answerTheOnly[merge.SimilarPair](t, choice, named(snapshots...)...)
}

// An old copy with an edit after the merge brings a version the result has not seen. It goes under
// the entry that stays, and nothing is lost.
func TestAnEditOfAMergedEntryInAnOldCopyGoesUnderTheEntryThatStays(t *testing.T) {
	old := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))
	result := answerTheOnlyPair(t, 1, old)

	edited := account(secondID, "Mail", "user", "", "after the merge")
	edited.Times.LastModification = hours(5)

	merged := mergeOf(t, result.Merged, database(edited)).Merged

	if got := fieldValue(&onlyEntry(t, merged).EntryVersion, "Password"); got != "after the merge" {
		t.Errorf("the main version has the password %q", got)
	}
}

// What an old copy does to an entry that is gone says nothing about the entry that took it in.
func TestAMergedEntryInTheBinOfAnOldCopySaysNothingAboutTheEntryThatStays(t *testing.T) {
	old := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))
	result := answerTheOnlyPair(t, 1, old)

	moved := account(secondID, "Mail", "user", "", "two")
	moved.Times.LocationChanged = hours(9)
	inBin := tree(snapshot.Group{UUID: groupA, Name: "Bin", Entries: []snapshot.Entry{moved}})
	inBin.Meta.RecycleBin = groupA

	again := mergeOf(t, result.Merged, inBin)

	wantNoQuestions(t, again)
	if got := groupOfOnlyEntry(t, again.Merged); got != again.Merged.Root.UUID {
		t.Errorf("the entry lies in %s, want the root", got)
	}
}

func TestAPairTheHumanKeptApartDoesNotAskAgain(t *testing.T) {
	old := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))

	result := answerTheOnlyPair(t, 0, old)
	wantOnlyUUIDsAndDates(t, result.Merged)

	againWith(t, result, old, func(t *testing.T, again *merge.Result) {
		wantNoQuestions(t, again)
		wantSameData(t, again.Merged, result.Merged)
	})
}

// No answer is no decision: the question comes again.
func TestAPairWithoutAnAnswerAsksAgain(t *testing.T) {
	old := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))

	merged := mergeOf(t, old).Merged

	if len(memoryItems(merged)) != 0 {
		t.Errorf("the memory holds %s", printed(memoryItems(merged)))
	}
	if got := pairQuestions(mergeOf(t, merged, old)); len(got) != 1 {
		t.Errorf("%d questions about pairs, want 1", len(got))
	}
}

// The memory also stops a merge through a third entry that does not look similar anymore.
func TestAPairKeptApartStaysApartAlsoThroughAThirdEntry(t *testing.T) {
	third := snapshot.UUID{15: 0x09}
	old := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))
	result := answerTheOnlyPair(t, 0, old)

	// Now the second one has another title, and a third one is similar to both.
	renamed := account(secondID, "Other", "user", "https://example.com", "two")
	renamed.Times.LastModification = hours(5)
	both := account(third, "Mail", "user", "https://example.com", "three")
	next := input(named(result.Merged, database(renamed, both))...)
	var decisions []merge.Decision
	for _, q := range mustRun(t, next).Questions {
		if _, ok := q.Detail.(merge.SimilarPair); ok {
			decisions = append(decisions, merge.Decision{Question: q.ID, Choice: 1})
		}
	}
	if len(decisions) != 2 {
		t.Fatalf("%d questions about pairs, want 2", len(decisions))
	}
	next.Decisions = decisions

	merged := mustRun(t, next)

	if got := len(allEntries(merged.Merged)); got != 2 {
		t.Errorf("%d entries, want 2: the first two stay apart", got)
	}
	notMerged := 0
	for _, event := range merged.Events {
		if _, ok := event.(merge.PairNotMerged); ok {
			notMerged++
		}
	}
	if notMerged != 1 {
		t.Errorf("%d pairs not merged, want 1", notMerged)
	}
}

// A kept entry waits for no deletion anymore, so it can make a pair in the next merge.
func TestAnEntryTheHumanKeptMakesAPairInTheNextMerge(t *testing.T) {
	both := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))
	deleted := recorded(database(), snapshot.Deletion{UUID: firstID, Time: hours(9)})
	copies := named(both, deleted)

	silent := mustRun(t, input(copies...))
	if got := pairQuestions(silent); len(got) != 0 {
		t.Fatalf("%d questions about pairs while the deletion is open", len(got))
	}

	result := answerTheOnly[merge.Deletion](t, merge.AnswerKeep, copies...)

	if got := pairQuestions(mergeOf(t, result.Merged)); len(got) != 1 {
		t.Errorf("%d questions about pairs, want 1", len(got))
	}
}

func TestARejectedDeletionDoesNotAskAgain(t *testing.T) {
	held := database(entry(version(hours(2), "kept")))
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)})

	result := answerTheOnly[merge.Deletion](t, merge.AnswerKeep, named(deleted, held)...)
	wantOnlyUUIDsAndDates(t, result.Merged)

	for _, old := range []*snapshot.Snapshot{deleted, held} {
		againWith(t, result, old, func(t *testing.T, again *merge.Result) {
			wantNoQuestions(t, again)
			wantSameData(t, again.Merged, result.Merged)
			if change := theDeletionChange(t, again); change.Outcome != merge.DeletionRejected || !change.Remembered {
				t.Errorf("outcome = %v, remembered = %v", change.Outcome, change.Remembered)
			}
		})
	}
}

func theDeletionChange(t *testing.T, result *merge.Result) merge.DeletionChange {
	t.Helper()
	for _, event := range result.Events {
		if change, ok := event.(merge.DeletionChange); ok {
			return change
		}
	}
	t.Fatalf("no event about a deletion: %s", printed(result.Events))
	return merge.DeletionChange{}
}

// A newer deletion is new to the human.
func TestANewerDeletionAsksAfterARejection(t *testing.T) {
	held := database(entry(version(hours(2), "kept")))
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)})
	result := answerTheOnly[merge.Deletion](t, merge.AnswerKeep, named(deleted, held)...)

	newer := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(10)})

	theDeletion(t, mergeOf(t, result.Merged, newer))
}

func TestAnAcceptedDeletionDoesNotBringTheEntryBackFromAnOldCopy(t *testing.T) {
	held := database(entry(version(hours(2), "gone"), version(hours(1), "older")))
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)})

	result := answerTheOnly[merge.Deletion](t, merge.AnswerDelete, named(deleted, held)...)
	wantOnlyUUIDsAndDates(t, result.Merged)

	againWith(t, result, held, func(t *testing.T, again *merge.Result) {
		wantNoQuestions(t, again)
		wantSameData(t, again.Merged, result.Merged)
		if change := theDeletionChange(t, again); change.Outcome != merge.DeletionApplied || !change.Remembered {
			t.Errorf("outcome = %v, remembered = %v", change.Outcome, change.Remembered)
		}
	})
}

// The answer of this merge is in the decisions, so the report does not call it remembered.
func TestAnAnswerOfThisMergeIsNotCalledRemembered(t *testing.T) {
	result := answerTheOnly[merge.Deletion](t, merge.AnswerDelete, named(
		database(entry(version(hours(2), "gone"))),
		recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)}),
	)...)

	if change := theDeletionChange(t, result); change.Outcome != merge.DeletionApplied || change.Remembered {
		t.Errorf("outcome = %v, remembered = %v", change.Outcome, change.Remembered)
	}
}

// The memory names each version the human knew, with one key for each time.
func TestAnAcceptedDeletionNamesEachVersionTheHumanSaw(t *testing.T) {
	first := database(entry(version(hours(2), "main"), version(hours(1), "older")))
	second := database(entry(version(hours(30), "the newest"), version(hours(1), "older")))
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(99)})

	in := input(named(first, second, deleted)...)
	q, _ := theDeletion(t, mustRun(t, in))
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: merge.AnswerDelete}}
	merged := mustRun(t, in).Merged

	var got []string
	for _, item := range memoryItems(merged) {
		got = append(got, item.Key)
	}
	var want []string
	for _, at := range []float64{1, 2, 30} {
		want = append(want, "cleanpass/accepted/"+mailID.String()+"/"+hours(at).Format(time.RFC3339Nano))
	}
	if !slices.Equal(got, want) {
		t.Errorf("the memory holds %q, want %q", got, want)
	}
}

// The human has deleted each version with the time it had then, whichever is the newest.
func TestAnAcceptedDeletionHoldsForEachVersionTheHumanKnew(t *testing.T) {
	held := database(entry(version(hours(20), "edited after the deletion")))
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)})

	result := answerTheOnly[merge.Deletion](t, merge.AnswerDelete, named(deleted, held)...)

	wantNoQuestions(t, mergeOf(t, result.Merged, held))
}

// An old copy with a version of another time holds something the human has not seen, whatever that
// time is. It comes back with the question, and without an answer the entry stays with it. A date
// could not tell this: a system with a clock that runs behind writes an old time on a new edit.
func TestAVersionOfAnotherTimeAsksAfterAnAcceptedDeletion(t *testing.T) {
	for name, at := range map[string]float64{
		"after the deletion":                    10,
		"between the versions the human saw":    3,
		"behind all the versions the human saw": -5,
	} {
		t.Run(name, func(t *testing.T) {
			held := database(entry(version(hours(4), "gone"), version(hours(1), "older"), version(hours(2), "other")))
			deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)})
			result := answerTheOnly[merge.Deletion](t, merge.AnswerDelete, named(deleted, held)...)

			edited := database(entry(version(hours(at), "an edit nobody saw"), version(hours(4), "gone")))

			again := mergeOf(t, result.Merged, edited)

			if _, asked := deletionQuestions(again.Questions)[mailID]; !asked {
				t.Errorf("no question about the deletion: %s", printed(again.Questions))
			}
			if change := theDeletionChange(t, again); change.Outcome != merge.DeletionOpen || change.Remembered {
				t.Errorf("outcome = %v, remembered = %v", change.Outcome, change.Remembered)
			}
			e := onlyEntry(t, again.Merged)
			if !slices.ContainsFunc(versionsOfEntry(e), func(v *snapshot.EntryVersion) bool {
				return fieldValue(v, "Password") == "an edit nobody saw"
			}) {
				t.Errorf("the new edit is gone: %s", printed(e.Fields))
			}
		})
	}
}

// keepassxc-cli gives an entry a new time when it moves it to the bin, with the same content. A
// time says nothing about content, so this asks too: one question more, and nothing silent.
func TestANewTimeOnTheSameContentAsksAfterAnAcceptedDeletion(t *testing.T) {
	held := database(entry(version(hours(2), "gone")))
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)})
	result := answerTheOnly[merge.Deletion](t, merge.AnswerDelete, named(deleted, held)...)

	moved := version(hours(10), "gone")
	inBin := withBin(binID, entry(moved, version(hours(2), "gone")))

	theDeletion(t, mergeOf(t, result.Merged, inBin))
}

// An old copy may hold only an entry that went into the deleted one. The human saw its versions
// under the entry that took them.
func TestAnAcceptedDeletionCoversAnEntryThatWentIntoIt(t *testing.T) {
	both := database(account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"))
	merged := answerTheOnlyPair(t, 1, both).Merged

	deleted := recorded(database(), snapshot.Deletion{UUID: firstID, Time: hours(9)})
	result := answerTheOnly[merge.Deletion](t, merge.AnswerDelete, named(merged, deleted)...)
	if got := len(allEntries(result.Merged)); got != 0 {
		t.Fatalf("%d entries after the answer, want none", got)
	}

	onlySecond := database(account(secondID, "Mail", "user", "", "two"))
	again := mergeOf(t, result.Merged, onlySecond)

	wantNoQuestions(t, again)
	if got := len(allEntries(again.Merged)); got != 0 {
		t.Errorf("%d entries after the merge with the old copy, want none", got)
	}
}

// Two systems, two answers: the newer decision counts, whichever copy holds it.
func TestTheNewerDecisionAboutADeletionCounts(t *testing.T) {
	held := database(entry(version(hours(2), "x")))
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)})
	answered := func(choice int, at time.Time) *snapshot.Snapshot {
		in := input(named(deleted, held)...)
		in.Now = at
		q, _ := theDeletion(t, mustRun(t, in))
		in.Decisions = []merge.Decision{{Question: q.ID, Choice: choice}}
		return mustRun(t, in).Merged
	}
	for name, tc := range map[string]struct {
		first, second int
		wantEntry     bool
	}{
		"delete, then keep": {merge.AnswerDelete, merge.AnswerKeep, true},
		"keep, then delete": {merge.AnswerKeep, merge.AnswerDelete, false},
	} {
		t.Run(name, func(t *testing.T) {
			older, newer := answered(tc.first, now), answered(tc.second, now.Add(time.Hour))

			for _, order := range [][]*snapshot.Snapshot{{older, newer, held}, {held, newer, older}} {
				result := mergeOf(t, order...)

				wantNoQuestions(t, result)
				if _, got := allEntries(result.Merged)[mailID]; got != tc.wantEntry {
					t.Errorf("the entry stays = %v, want %v", got, tc.wantEntry)
				}
			}
		})
	}
}

// The human has answered about two old copies, so the result holds both behind a new version.
// Neither of them is news, also when both come along in one merge.
func TestAnAnsweredConflictDoesNotAskAgainWithTwoOldCopiesAtOnce(t *testing.T) {
	first, second := database(entry(version(hours(1), "one"))), database(entry(version(hours(2), "two")))
	third := database(entry(version(hours(3), "three")))

	for name, copies := range map[string][]*snapshot.Snapshot{
		"two copies":   {first, second},
		"three copies": {first, second, third},
	} {
		t.Run(name, func(t *testing.T) {
			// The last answer takes the oldest version, so the human makes a new version with it.
			in := input(named(copies...)...)
			q, conflict := theConflict(t, mustRun(t, in))
			in.Decisions = []merge.Decision{{Question: q.ID, Choice: len(conflict.Versions) - 1}}
			result := mustRun(t, in)

			again := mergeOf(t, append([]*snapshot.Snapshot{result.Merged}, copies...)...)

			wantNoQuestions(t, again)
			wantSameData(t, again.Merged, result.Merged)
		})
	}
}

// The same two old copies without a result beside them are a true conflict, as they always were.
func TestTwoCopiesInConflictStillAskWithAnUnrelatedThirdCopy(t *testing.T) {
	first, second := database(entry(version(hours(1), "one"))), database(entry(version(hours(2), "two")))
	onlyOne := database(entry(version(hours(3), "three"), version(hours(1), "one")))

	theConflict(t, mergeOf(t, first, second, onlyOne))
}

// The memory holds with answers about an entry in the bin the same way.
func TestAnAnswerAboutAnEntryInTheBinDoesNotAskAgain(t *testing.T) {
	inBin, outside := withBin(binID, movedAt(9)), withBin(homeID, movedAt(3))

	for name, choice := range map[string]int{"keep": merge.AnswerKeep, "delete": merge.AnswerDelete} {
		t.Run(name, func(t *testing.T) {
			result := answerTheOnly[merge.Deletion](t, choice, named(inBin, outside)...)

			for _, old := range []*snapshot.Snapshot{inBin, outside} {
				againWith(t, result, old, func(t *testing.T, again *merge.Result) {
					wantNoQuestions(t, again)
					wantSameData(t, again.Merged, result.Merged)
				})
			}
		})
	}
}

// The clock has put a replaced version on top, and the human lets it stay.
func TestAnAnswerAboutAReplacedMainVersionDoesNotAskAgain(t *testing.T) {
	behind, ahead := clockAnomaly()
	result := answerTheOnly[merge.Conflict](t, 0, named(behind, ahead)...)
	wantOnlyUUIDsAndDates(t, result.Merged)

	for _, old := range []*snapshot.Snapshot{behind, ahead} {
		againWith(t, result, old, func(t *testing.T, again *merge.Result) {
			wantNoQuestions(t, again)
			wantSameData(t, again.Merged, result.Merged)
		})
	}
}

// A new edit of the old copy is something the human has not seen.
func TestANewEditBehindTheClockAsksAfterAnAnswerAboutAReplacedMainVersion(t *testing.T) {
	behind, ahead := clockAnomaly()
	result := answerTheOnly[merge.Conflict](t, 0, named(behind, ahead)...)

	// The anomaly: the main version of the result sits in the history of this copy, under a
	// newer edit that still has an older time than that main version.
	main := onlyEntry(t, result.Merged).EntryVersion
	newest := onlyEntry(t, ahead)
	edited := version(main.Times.LastModification.Add(-time.Minute), "a newer edit behind the clock")
	if !edited.Times.LastModification.After(newest.Times.LastModification) {
		t.Fatalf("the fixture has the edit at %s, the old one at %s", edited.Times.LastModification, newest.Times.LastModification)
	}
	again := mergeOf(t, result.Merged, database(entry(edited, main)))

	theConflict(t, again)
}

// Without an answer nothing goes to the memory.
func TestAReplacedMainVersionWithoutAnAnswerAsksAgain(t *testing.T) {
	behind, ahead := clockAnomaly()
	merged := mergeOf(t, behind, ahead).Merged

	if len(memoryItems(merged)) != 0 {
		t.Errorf("the memory holds %s", printed(memoryItems(merged)))
	}
	theConflict(t, mergeOf(t, merged, ahead))
}

// The groups stay one, also when the group that stays moved away from their old path.
func TestGroupsThatJoinedStayOneWithAnOldCopy(t *testing.T) {
	old := tree(node(groupB, "Work", 1))
	old.Root.Groups[0].Entries = []snapshot.Entry{entry(version(hours(1), "x"))}

	result := mergeOf(t, old, tree(node(groupA, "Work", 1)))
	wantOnlyUUIDsAndDates(t, result.Merged)

	againWith(t, result, old, func(t *testing.T, again *merge.Result) {
		wantNoQuestions(t, again)
		wantSameData(t, again.Merged, result.Merged)
		if want := map[snapshot.UUID]snapshot.UUID{groupB: groupA}; !reflect.DeepEqual(again.MergedGroups, want) {
			t.Errorf("merged groups = %v, want %v", again.MergedGroups, want)
		}
	})

	moved := node(groupA, "Work", 1)
	moved.Times.LocationChanged = hours(5)
	away := tree(node(groupC, "Other", 1, moved))
	merged := mergeOf(t, result.Merged, away, old)
	if got := groupOfOnlyEntry(t, merged.Merged); got != groupA {
		t.Errorf("the entry lies in %s, want %s", got, groupA)
	}
	wantShape(t, merged.Merged, "/a3", "/a3/a1")
}

// The next merge names no new join in its report: it is old news.
func TestAnOldJoinIsNoNewsInTheReport(t *testing.T) {
	old := tree(node(groupB, "Work", 1))
	result := mergeOf(t, old, tree(node(groupA, "Work", 1)))

	again := mergeOf(t, result.Merged, old)

	for _, event := range again.Events {
		if _, ok := event.(merge.GroupsJoined); ok {
			t.Errorf("the report tells %+v", event)
		}
	}
}

func TestAnItemTheMemoryCannotReadStaysAsCustomData(t *testing.T) {
	for _, key := range []string{
		"cleanpass/", "cleanpass/entries", "cleanpass/entries/zz/yy", "cleanpass/entries/" + firstID.String(),
		"cleanpass/accepted/" + mailID.String() + "/not a date", "cleanpass/replaced/" + mailID.String(),
		"cleanpass/unknown/" + mailID.String(),
	} {
		t.Run(key, func(t *testing.T) {
			s := database(entry(version(hours(1), "x")))
			s.Meta.CustomData = []snapshot.CustomDataItem{{Key: key, Value: "value"}}

			merged := mergeOf(t, s).Merged

			if !reflect.DeepEqual(merged.Meta.CustomData, s.Meta.CustomData) {
				t.Errorf("custom data = %+v", merged.Meta.CustomData)
			}
			if onlyEntry(t, merged).UUID != mailID {
				t.Errorf("the entry changed its UUID")
			}
		})
	}
}

// A secret or a name in the memory would be in the file in a new place.
func TestTheMemoryHoldsNoNameAndNoSecret(t *testing.T) {
	secret, name := "SECRET-VALUE", "Назва групи"
	both := database(account(firstID, "Mail", "user", "", secret), account(secondID, "Mail", "user", "", secret))
	both.Root.Groups = []snapshot.Group{{UUID: groupB, Name: name}}
	other := tree(node(groupA, name, 1))

	result := answerTheOnly[merge.SimilarPair](t, 1, named(both, other)...)

	text := printed(memoryItems(result.Merged))
	for _, word := range []string{secret, name, "Mail", "user"} {
		if strings.Contains(text, word) {
			t.Errorf("the memory holds %q: %s", word, text)
		}
	}
	if len(memoryItems(result.Merged)) < 2 {
		t.Errorf("the memory holds too little: %s", text)
	}
}

// The date of a decision is in the value, so a save that changes the time of an item changes nothing.
func TestTheMemoryWorksWithoutTheTimesOfItsItems(t *testing.T) {
	held := database(entry(version(hours(2), "gone")))
	deleted := recorded(database(), snapshot.Deletion{UUID: mailID, Time: hours(9)})
	result := answerTheOnly[merge.Deletion](t, merge.AnswerDelete, named(deleted, held)...)

	saved := *result.Merged
	saved.Meta.CustomData = slices.Clone(result.Merged.Meta.CustomData)
	for i := range saved.Meta.CustomData {
		saved.Meta.CustomData[i].LastModification = time.Time{}
	}

	wantNoQuestions(t, mergeOf(t, &saved, held))
}

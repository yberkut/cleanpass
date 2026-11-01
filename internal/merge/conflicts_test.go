package merge_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

const day = 24 * time.Hour

// twoDivergedCopies holds "older" and "newer" in two copies, with no trace of each other
// in any history: a true conflict.
func twoDivergedCopies(gap time.Duration) []*snapshot.Snapshot {
	return []*snapshot.Snapshot{
		database(entry(version(day0, "older"))),
		database(entry(version(day0.Add(gap), "newer"))),
	}
}

// theConflict returns the only question of the result, which must be about a conflict.
func theConflict(t *testing.T, result *merge.Result) (merge.Question, merge.Conflict) {
	t.Helper()
	if len(result.Questions) != 1 {
		t.Fatalf("got %d questions, want 1", len(result.Questions))
	}
	q := result.Questions[0]
	detail, ok := q.Detail.(merge.Conflict)
	if !ok {
		t.Fatalf("the question has detail %T, want merge.Conflict", q.Detail)
	}
	return q, detail
}

func TestConflictAtLeastOneDayApartTakesTheNewerWithoutQuestion(t *testing.T) {
	for name, gap := range map[string]time.Duration{"exactly a day": day, "ten days": 10 * day} {
		t.Run(name, func(t *testing.T) {
			result := mergeOf(t, twoDivergedCopies(gap)...)

			wantNoQuestions(t, result)
			wantVersions(t, onlyEntry(t, result.Merged), "newer", "older")
		})
	}
}

func TestConflictLessThanOneDayApartAsks(t *testing.T) {
	for name, gap := range map[string]time.Duration{
		"a second less than a day": day - time.Second,
		"an hour":                  time.Hour,
		"the same second":          0,
	} {
		t.Run(name, func(t *testing.T) {
			result := mergeOf(t, twoDivergedCopies(gap)...)

			_, conflict := theConflict(t, result)
			if !conflict.Close || conflict.Future {
				t.Errorf("close = %v, future = %v, want true and false", conflict.Close, conflict.Future)
			}
		})
	}
}

func TestConflictWithoutAnAnswerKeepsTheRuleOfTheNewer(t *testing.T) {
	result := mergeOf(t, twoDivergedCopies(time.Hour)...)

	wantVersions(t, onlyEntry(t, result.Merged), "newer", "older")
}

func TestStampFromTheFutureAsksEvenWithALongGap(t *testing.T) {
	result := mergeOf(t,
		database(entry(version(now.Add(-10*day), "older"))),
		database(entry(version(now.Add(time.Second), "newer"))),
	)

	_, conflict := theConflict(t, result)
	if !conflict.Future || conflict.Close {
		t.Errorf("future = %v, close = %v, want true and false", conflict.Future, conflict.Close)
	}
	wantVersions(t, onlyEntry(t, result.Merged), "newer", "older")
}

func TestStampExactlyNowIsNotFromTheFuture(t *testing.T) {
	result := mergeOf(t,
		database(entry(version(now.Add(-10*day), "older"))),
		database(entry(version(now, "newer"))),
	)

	wantNoQuestions(t, result)
}

func TestStampFromTheFutureWithoutAConflictDoesNotAsk(t *testing.T) {
	older := version(now.Add(-time.Hour), "older")
	future := version(now.Add(10*day), "future")

	lagging := mergeOf(t, database(entry(older)), database(entry(future, older)))
	alone := mergeOf(t, database(entry(future)))

	wantNoQuestions(t, lagging)
	wantNoQuestions(t, alone)
}

func TestChoosingTheOlderVersionMakesANewVersionWithItsContentAndTheCurrentTime(t *testing.T) {
	copies := named(twoDivergedCopies(time.Hour)...)
	asked := mustRun(t, input(copies...))
	q, conflict := theConflict(t, asked)
	if got := conflict.Versions[1].Modified; !got.Equal(day0) {
		t.Fatalf("second answer is the version of %s, want the older one", got)
	}

	in := input(copies...)
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}
	result := mustRun(t, in)

	merged := onlyEntry(t, result.Merged)
	if !merged.Times.LastModification.Equal(now) {
		t.Errorf("main version has time %s, want the current time %s", merged.Times.LastModification, now)
	}
	wantVersions(t, merged, "older", "older", "newer")
	if !merged.History[0].Times.LastModification.Equal(day0) {
		t.Errorf("the original older version has time %s, want %s", merged.History[0].Times.LastModification, day0)
	}
}

func TestTheDecisionIsNotUndoneByTheNextMerge(t *testing.T) {
	originals := twoDivergedCopies(time.Hour)
	in := input(named(originals...)...)
	q, _ := theConflict(t, mustRun(t, in))
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}
	decided := mustRun(t, in).Merged

	for _, original := range originals {
		again := mergeOf(t, decided, original)

		wantNoQuestions(t, again)
		if !reflect.DeepEqual(again.Merged, decided) {
			t.Errorf("merging with an old copy changed the result:\n%+v\n%+v", again.Merged, decided)
		}
	}
}

// With the newer version from the future, the current time would not be the newest. The
// decision must win over it anyway, or the next merge would undo it.
func TestChoosingOlderBeatsAVersionFromTheFuture(t *testing.T) {
	future := now.Add(10 * day)
	copies := named(database(entry(version(now.Add(-time.Hour), "older"))), database(entry(version(future, "future"))))
	in := input(copies...)
	q, _ := theConflict(t, mustRun(t, in))
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}

	merged := onlyEntry(t, mustRun(t, in).Merged)

	wantVersions(t, merged, "older", "older", "future")
	if !merged.Times.LastModification.After(future) {
		t.Errorf("main version has time %s, want after %s", merged.Times.LastModification, future)
	}
}

// The file stores whole seconds. A time with more digits would not survive the write.
func TestTheNewVersionHasAWholeSecond(t *testing.T) {
	in := input(named(twoDivergedCopies(time.Hour)...)...)
	in.Now = now.Add(123 * time.Millisecond)
	q, _ := theConflict(t, mustRun(t, in))
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}

	merged := onlyEntry(t, mustRun(t, in).Merged)

	if !merged.Times.LastModification.Equal(now) {
		t.Errorf("main version has time %s, want %s", merged.Times.LastModification, now)
	}
}

func TestChoosingTheNewerChangesNothing(t *testing.T) {
	in := input(named(twoDivergedCopies(time.Hour)...)...)
	without := mustRun(t, in)
	q, _ := theConflict(t, without)
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 0}}

	with := mustRun(t, in)

	if !reflect.DeepEqual(with.Merged, without.Merged) {
		t.Errorf("the answer changed the result:\n%+v\n%+v", with.Merged, without.Merged)
	}
}

// With the same time on both versions, the key picks the main version. Another run has
// another key, so only a new version keeps what the human said.
func TestAnAnswerAboutVersionsWithTheSameTimeLastsInEveryRun(t *testing.T) {
	copies := named(twoDivergedCopies(0)...)
	in := input(copies...)
	q, conflict := theConflict(t, mustRun(t, in))
	chosen := fieldValue(&onlyEntry(t, mustRun(t, in).Merged).EntryVersion, "Password")
	if len(conflict.Versions) != 2 {
		t.Fatalf("got %d answers, want 2", len(conflict.Versions))
	}
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 0}}
	decided := mustRun(t, in).Merged

	for i := range 32 {
		next := input(append(copies, merge.Copy{Name: "decided", Snapshot: decided})...)
		next.Key = merge.Key{31: byte(i + 1)}
		merged := onlyEntry(t, mustRun(t, next).Merged)
		if got := fieldValue(&merged.EntryVersion, "Password"); got != chosen {
			t.Fatalf("run %d: main version has password %q, want the chosen %q", i, got, chosen)
		}
	}
}

func TestDecisionForAQuestionNobodyAskedIsAnError(t *testing.T) {
	in := input(named(database(entry(version(day0, "only"))))...)
	in.Decisions = []merge.Decision{{Question: "conflict:nothing", Choice: 0}}

	if _, err := merge.Run(in); !errors.Is(err, merge.ErrUnknownQuestion) {
		t.Errorf("error = %v, want %v", err, merge.ErrUnknownQuestion)
	}
}

func TestDecisionWithAChoiceTheQuestionDoesNotHaveIsAnError(t *testing.T) {
	in := input(named(twoDivergedCopies(time.Hour)...)...)
	q, _ := theConflict(t, mustRun(t, in))

	for _, choice := range []int{-1, q.Choices} {
		in.Decisions = []merge.Decision{{Question: q.ID, Choice: choice}}
		if _, err := merge.Run(in); !errors.Is(err, merge.ErrBadChoice) {
			t.Errorf("choice %d: error = %v, want %v", choice, err, merge.ErrBadChoice)
		}
	}
}

// The human sees the data of the versions next to each other, without secret values.
func TestTheQuestionShowsBothVersionsWithoutSecrets(t *testing.T) {
	older := version(day0, "SECRET-OLD")
	newer := version(day0.Add(time.Hour), "SECRET-NEW")
	newer.Fields = append(newer.Fields,
		snapshot.Field{Key: "Custom", Value: "SECRET-CUSTOM"},
		snapshot.Field{Key: "token", Value: "SECRET-TOKEN", Protected: true},
	)
	newer.Tags = "changed"
	older.Attachments = []snapshot.AttachmentRef{{Name: "a.bin", ID: snapshot.NewAttachmentID([]byte("SECRET-BYTES"))}}

	result := mergeOf(t, database(entry(older)), database(entry(newer)))

	q, conflict := theConflict(t, result)
	if q.Choices != 2 || len(conflict.Versions) != 2 {
		t.Fatalf("choices = %d, versions = %d, want 2 of each", q.Choices, len(conflict.Versions))
	}
	if conflict.Entry != mailID {
		t.Errorf("entry = %s, want %s", conflict.Entry, mailID)
	}
	first, second := conflict.Versions[0], conflict.Versions[1]
	if !first.Modified.Equal(day0.Add(time.Hour)) || !second.Modified.Equal(day0) {
		t.Errorf("versions have times %s and %s, want the newest first", first.Modified, second.Modified)
	}
	if !slices.Equal(first.Copies, []string{"copy-2"}) || !slices.Equal(second.Copies, []string{"copy-1"}) {
		t.Errorf("copies = %q and %q", first.Copies, second.Copies)
	}
	wantLabel := merge.Label{Title: "Mail", UserName: "user"}
	if first.Label != wantLabel || second.Label != wantLabel {
		t.Errorf("labels = %+v and %+v, want %+v", first.Label, second.Label, wantLabel)
	}
	if len(first.Differences.Fields) != 0 || len(first.Differences.Attributes) != 0 {
		t.Errorf("the first answer differs from itself: %+v", first.Differences)
	}
	if want := []string{"Custom", "Password", "token"}; !slices.Equal(second.Differences.Fields, want) {
		t.Errorf("changed fields = %q, want %q", second.Differences.Fields, want)
	}
	if want := []merge.Attribute{merge.AttributeTags, merge.AttributeAttachments}; !slices.Equal(second.Differences.Attributes, want) {
		t.Errorf("changed attributes = %v, want %v", second.Differences.Attributes, want)
	}
	wantNoSecrets(t, result, "SECRET-")
}

func TestALabelHidesAProtectedTitle(t *testing.T) {
	older := version(day0, "old")
	older.Fields[0].Protected = true
	older.Fields[0].Value = "SECRET-TITLE"
	newer := version(day0.Add(time.Hour), "new")

	result := mergeOf(t, database(entry(older)), database(entry(newer)))

	wantNoSecrets(t, result, "SECRET-")
}

func wantNoSecrets(t *testing.T, result *merge.Result, marker string) {
	t.Helper()
	if printed := printed(result.Questions) + printed(result.Report); strings.Contains(printed, marker) {
		t.Errorf("a secret value reached the questions or the report:\n%s", printed)
	}
}

// With three copies, the copy that lags does not count, and the two that diverged do.
func TestThreeCopiesWithOneLaggingAndTwoInConflict(t *testing.T) {
	base := version(hours(0), "base")
	left := version(hours(1), "left")
	right := version(hours(2), "right")

	result := mergeOf(t,
		database(entry(base)),
		database(entry(left, base)),
		database(entry(right, base)),
	)

	_, conflict := theConflict(t, result)
	if len(conflict.Versions) != 2 {
		t.Errorf("got %d answers, want the 2 versions in conflict", len(conflict.Versions))
	}
	wantVersions(t, onlyEntry(t, result.Merged), "right", "base", "left")
}

// Each copy has replaced the main version of the other. That is no conflict, but the clock
// cannot be right in both copies, so the human says which order holds.
func TestCopiesThatReplacedEachOthersVersionAskOnlyAboutTheClock(t *testing.T) {
	one := version(hours(1), "one")
	two := version(hours(2), "two")

	result := mergeOf(t, database(entry(one, two)), database(entry(two, one)))

	_, conflict := theConflict(t, result)
	if !conflict.Replaced || conflict.Close || conflict.Future {
		t.Errorf("replaced = %v, close = %v, future = %v, want only replaced", conflict.Replaced, conflict.Close, conflict.Future)
	}
	wantVersions(t, onlyEntry(t, result.Merged), "two", "one")
}

func TestTwoCopiesWithTheSameMainVersionDoNotConflict(t *testing.T) {
	same := version(hours(1), "same")

	result := mergeOf(t, database(entry(same)), database(entry(same, version(hours(0), "old"))))

	wantNoQuestions(t, result)
	wantVersions(t, onlyEntry(t, result.Merged), "same", "old")
}

// clockAnomaly gives a copy whose clock ran behind: its latest edit has an older time than
// the version it replaced, which the other copy still holds as main.
func clockAnomaly() (behind, ahead *snapshot.Snapshot) {
	replaced := version(hours(10), "replaced")
	edited := version(hours(5), "edited")
	return database(entry(replaced)), database(entry(edited, replaced))
}

func TestAMainVersionThatACopyReplacedAsks(t *testing.T) {
	behind, ahead := clockAnomaly()

	result := mergeOf(t, behind, ahead)

	q, conflict := theConflict(t, result)
	if !conflict.Replaced || conflict.Close || conflict.Future {
		t.Errorf("replaced = %v, close = %v, future = %v, want only replaced", conflict.Replaced, conflict.Close, conflict.Future)
	}
	if q.Choices != 2 || len(conflict.Versions) != 2 {
		t.Fatalf("choices = %d, versions = %d, want 2 of each", q.Choices, len(conflict.Versions))
	}
	first, second := conflict.Versions[0], conflict.Versions[1]
	if !first.Modified.Equal(hours(10)) || !slices.Equal(first.Copies, []string{"copy-1"}) {
		t.Errorf("first answer = %+v, want the main version of the rule", first)
	}
	if !second.Modified.Equal(hours(5)) || !slices.Equal(second.Copies, []string{"copy-2"}) {
		t.Errorf("second answer = %+v, want the main version of the copy that replaced it", second)
	}
}

// Without an answer nothing changes: the rule of the maximum stays, and the report says it
// put a replaced version on top.
func TestAReplacedMainVersionStaysWithoutAnAnswer(t *testing.T) {
	behind, ahead := clockAnomaly()

	result := mergeOf(t, behind, ahead)

	wantVersions(t, onlyEntry(t, result.Merged), "replaced", "edited")
	change := theEntryChange(t, result)
	if change.Outcome != merge.OutcomeAsked || !change.Replaced {
		t.Errorf("outcome = %v, replaced = %v, want asked and true", change.Outcome, change.Replaced)
	}
}

func TestAnswerZeroKeepsTheRuleOfTheMaximum(t *testing.T) {
	behind, ahead := clockAnomaly()
	in := input(named(behind, ahead)...)
	without := mustRun(t, in)
	q, _ := theConflict(t, without)
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 0}}

	with := mustRun(t, in)

	if !reflect.DeepEqual(with.Merged, without.Merged) {
		t.Errorf("the answer changed the result:\n%+v\n%+v", with.Merged, without.Merged)
	}
	if change := theEntryChange(t, with); change.Outcome != merge.OutcomeAsked || !change.Replaced {
		t.Errorf("outcome = %v, replaced = %v, want asked and true", change.Outcome, change.Replaced)
	}
}

func TestTheOtherAnswerMakesTheMainVersionOfThatCopyLast(t *testing.T) {
	behind, ahead := clockAnomaly()
	in := input(named(behind, ahead)...)
	q, _ := theConflict(t, mustRun(t, in))
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}

	result := mustRun(t, in)

	merged := onlyEntry(t, result.Merged)
	wantVersions(t, merged, "edited", "edited", "replaced")
	if !merged.Times.LastModification.Equal(now) {
		t.Errorf("main version has time %s, want the current time %s", merged.Times.LastModification, now)
	}
	if change := theEntryChange(t, result); change.Outcome != merge.OutcomeChosen || change.Replaced {
		t.Errorf("outcome = %v, replaced = %v, want chosen and false", change.Outcome, change.Replaced)
	}
	for _, old := range []*snapshot.Snapshot{behind, ahead} {
		again := mergeOf(t, result.Merged, old)

		wantNoQuestions(t, again)
		if !reflect.DeepEqual(again.Merged, result.Merged) {
			t.Errorf("merging with an old copy changed the result:\n%+v\n%+v", again.Merged, result.Merged)
		}
	}
}

// Nothing has settled the order yet, so the question comes back while a copy says otherwise
// and goes away with that copy. The result stays the same either way.
func TestARepeatedMergeWithoutAnAnswerAsksAgainOnlyWithTheCopyThatReplacedIt(t *testing.T) {
	behind, ahead := clockAnomaly()
	first := mergeOf(t, behind, ahead).Merged

	withAhead := mergeOf(t, first, ahead)
	_, conflict := theConflict(t, withAhead)
	if !conflict.Replaced {
		t.Errorf("replaced = %v, want true", conflict.Replaced)
	}
	if !reflect.DeepEqual(withAhead.Merged, first) {
		t.Errorf("the merge changed the result:\n%+v\n%+v", withAhead.Merged, first)
	}
	wantNoQuestions(t, mergeOf(t, first, behind))
	wantNoQuestions(t, mergeOf(t, first))
}

// The gap does not matter here: the history says the clock is wrong, however far.
func TestAReplacedMainVersionAsksWhateverTheGap(t *testing.T) {
	replaced := version(hours(600), "replaced")

	result := mergeOf(t, database(entry(replaced)), database(entry(version(hours(1), "edited"), replaced)))

	if _, conflict := theConflict(t, result); !conflict.Replaced {
		t.Errorf("replaced = %v, want true", conflict.Replaced)
	}
}

func TestOneCopyWithANewerStampInItsOwnHistoryAsks(t *testing.T) {
	result := mergeOf(t, database(entry(version(hours(5), "main"), version(hours(10), "history"))))

	if _, conflict := theConflict(t, result); !conflict.Replaced {
		t.Errorf("replaced = %v, want true", conflict.Replaced)
	}
	wantVersions(t, onlyEntry(t, result.Merged), "history", "main")
}

// A plain lag with clocks that agree stays silent, as the spec says.
func TestALagWithClocksThatAgreeIsNoQuestionAndNoReplacedVersion(t *testing.T) {
	older := version(hours(1), "old")

	result := mergeOf(t, database(entry(older)), database(entry(version(hours(2), "new"), older)))

	wantNoQuestions(t, result)
	if change := theEntryChange(t, result); change.Outcome != merge.OutcomeUpdated || change.Replaced {
		t.Errorf("outcome = %v, replaced = %v, want updated and false", change.Outcome, change.Replaced)
	}
}

// A true conflict with a long gap asks nothing by itself. With a replaced main version on top
// the same question also shows the other versions, so one entry never has two questions.
func TestAReplacedMainVersionAndAConflictWithALongGapGiveOneQuestion(t *testing.T) {
	replaced := version(hours(100), "replaced")
	edited := version(hours(50), "edited")
	other := version(hours(10), "other")

	result := mergeOf(t, database(entry(replaced)), database(entry(edited, replaced)), database(entry(other)))

	q, conflict := theConflict(t, result)
	if !conflict.Replaced || conflict.Close || conflict.Future {
		t.Errorf("replaced = %v, close = %v, future = %v, want only replaced", conflict.Replaced, conflict.Close, conflict.Future)
	}
	if q.Choices != 3 {
		t.Fatalf("choices = %d, want 3", q.Choices)
	}
	for i, want := range []time.Time{hours(100), hours(50), hours(10)} {
		if got := conflict.Versions[i].Modified; !got.Equal(want) {
			t.Errorf("answer %d has time %s, want %s", i, got, want)
		}
	}
}

// Two versions with the same time: a merge settled them with its key, and each later run has
// another key. A run that moves the main version must ask, and a run that keeps it must not.
func TestAMainVersionThatMovesWithTheKeyAlwaysAsks(t *testing.T) {
	settled := mergeOf(t, twoDivergedCopies(0)...).Merged
	settledMain := fieldValue(&onlyEntry(t, settled).EntryVersion, "Password")

	moved, kept := 0, 0
	for i := range 64 {
		in := input(named(settled)...)
		in.Key = merge.Key{31: byte(i + 1)}
		result := mustRun(t, in)
		if fieldValue(&onlyEntry(t, result.Merged).EntryVersion, "Password") == settledMain {
			kept++
			wantNoQuestions(t, result)
			continue
		}
		moved++
		q, conflict := theConflict(t, result)
		if !conflict.Replaced {
			t.Errorf("key %d: replaced = %v, want true", i, conflict.Replaced)
		}

		// The other answer brings the settled version back, for good.
		in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}
		decided := mustRun(t, in).Merged
		if got := fieldValue(&onlyEntry(t, decided).EntryVersion, "Password"); got != settledMain {
			t.Errorf("key %d: the answer gives %q, want %q", i, got, settledMain)
		}
		for j := range 8 {
			next := input(named(decided)...)
			next.Key = merge.Key{30: 1, 31: byte(j + 1)}
			after := mustRun(t, next)
			wantNoQuestions(t, after)
			if got := fieldValue(&onlyEntry(t, after.Merged).EntryVersion, "Password"); got != settledMain {
				t.Errorf("key %d, then key %d: main version is %q, want %q", i, j, got, settledMain)
			}
		}
	}
	if moved == 0 || kept == 0 {
		t.Errorf("moved %d times and kept %d times, want some of each", moved, kept)
	}
}

// The clock can put a version on top that is in conflict with nothing, because every copy
// with another main version has replaced it. The first answer still is what happens
// without an answer, so the human sees that version too.
func TestTheFirstAnswerIsWhatHappensWithoutAnAnswer(t *testing.T) {
	replaced := version(hours(10), "replaced")
	base := database(entry(replaced))
	one := database(entry(version(hours(5), "one"), replaced))
	other := database(entry(version(hours(6), "other"), replaced))

	result := mergeOf(t, base, one, other)

	q, conflict := theConflict(t, result)
	wantVersions(t, onlyEntry(t, result.Merged), "replaced", "one", "other")
	if q.Choices != 3 || len(conflict.Versions) != 3 {
		t.Fatalf("choices = %d, versions = %d, want 3 of each", q.Choices, len(conflict.Versions))
	}
	for i, want := range []time.Time{hours(10), hours(6), hours(5)} {
		if got := conflict.Versions[i].Modified; !got.Equal(want) {
			t.Errorf("answer %d has time %s, want %s", i, got, want)
		}
	}
}

func TestInputIsNotChanged(t *testing.T) {
	older := version(day0, "older")
	first := database(entry(older))
	second := database(entry(version(day0.Add(time.Hour), "newer")))
	wantFirst, wantSecond := printed(first.Root), printed(second.Root)

	mergeOf(t, first, second)

	if printed(first.Root) != wantFirst || printed(second.Root) != wantSecond {
		t.Error("the merge changed its input")
	}
}

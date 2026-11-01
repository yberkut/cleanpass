package merge_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// withCustomData gives the version the custom data, written as key, value, key, value.
func withCustomData(v snapshot.EntryVersion, pairs ...string) snapshot.EntryVersion {
	v.CustomData = nil
	for i := 0; i < len(pairs); i += 2 {
		v.CustomData = append(v.CustomData, snapshot.CustomDataItem{Key: pairs[i], Value: pairs[i+1]})
	}
	return v
}

// customDataOfVersion gives the custom data as key, value, key, value, in the order of the file.
func customDataOfVersion(v *snapshot.EntryVersion) []string {
	var pairs []string
	for _, item := range v.CustomData {
		pairs = append(pairs, item.Key, item.Value)
	}
	return pairs
}

func wantCustomData(t *testing.T, what string, v *snapshot.EntryVersion, want ...string) {
	t.Helper()
	got := customDataOfVersion(v)
	if len(got) != len(want) {
		t.Errorf("%s has custom data %q, want %q", what, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s has custom data %q, want %q", what, got, want)
			return
		}
	}
}

// KeePassXC does not carry the custom data of an entry into the history version an edit makes.
// So the copy that went on holds the old version without that data.
func keepassxcEdit() (old, edited *snapshot.Snapshot) {
	before := withCustomData(version(hours(1), "old"), "browser", "settings")
	after := withCustomData(version(hours(2), "new"), "browser", "settings")
	return database(entry(before)), database(entry(after, withCustomData(before)))
}

func TestAnEditThatLeftCustomDataOutOfTheHistoryIsALag(t *testing.T) {
	old, edited := keepassxcEdit()

	for name, order := range map[string][]*snapshot.Snapshot{"old first": {old, edited}, "edited first": {edited, old}} {
		t.Run(name, func(t *testing.T) {
			result := mergeOf(t, order...)

			wantNoQuestions(t, result)
			merged := onlyEntry(t, result.Merged)
			wantVersions(t, merged, "new", "old")
			wantCustomData(t, "the old version", &merged.History[0], "browser", "settings")
		})
	}
}

func TestAnEditThatLeftCustomDataOutOfTheHistoryIsReportedAsALag(t *testing.T) {
	old, edited := keepassxcEdit()

	result := mustRun(t, merge.Input{
		Copies: []merge.Copy{{Name: "old", Snapshot: old}, {Name: "edited", Snapshot: edited}},
		Now:    now,
		Key:    testKey,
	})

	if len(result.Report) != 1 {
		t.Fatalf("report = %s, want one event", printed(result.Report))
	}
	change := result.Report[0].(merge.EntryChange)
	if change.Outcome != merge.OutcomeUpdated {
		t.Errorf("outcome = %d, want updated", change.Outcome)
	}
	for _, state := range change.Copies {
		if want := state.Name == "old"; state.Behind != want {
			t.Errorf("copy %s: behind = %v, want %v", state.Name, state.Behind, want)
		}
	}
}

func theChange(t *testing.T, result *merge.Result) merge.EntryChange {
	t.Helper()
	var changes []merge.EntryChange
	for _, event := range result.Report {
		if change, ok := event.(merge.EntryChange); ok {
			changes = append(changes, change)
		}
	}
	if len(changes) != 1 {
		t.Fatalf("report = %s, want one change of an entry", printed(result.Report))
	}
	return changes[0]
}

// A criterion of the ticket: custom data where neither holds the other is a true difference.
func TestTwoMainVersionsWhereNeitherHoldsTheOtherAreATrueConflict(t *testing.T) {
	for name, pairs := range map[string][2][]string{
		"different keys":    {{"browser", "a"}, {"plugin", "b"}},
		"different values":  {{"browser", "a"}, {"browser", "b"}},
		"an item in common": {{"common", "x", "browser", "a"}, {"common", "x", "plugin", "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			first := withCustomData(version(hours(1), "same"), pairs[0]...)
			second := withCustomData(version(hours(1), "same"), pairs[1]...)

			result := mergeOf(t, database(entry(first)), database(entry(second)))

			_, conflict := theConflict(t, result)
			if !conflict.Close {
				t.Errorf("close = %v, want true", conflict.Close)
			}
			if got := 1 + len(onlyEntry(t, result.Merged).History); got != 2 {
				t.Errorf("the result has %d versions, want both", got)
			}
		})
	}
}

func TestAHistoryVersionWhereNeitherHoldsTheOtherIsNoProofOfALag(t *testing.T) {
	first := withCustomData(version(hours(1), "old"), "browser", "a")
	other := withCustomData(version(hours(1), "old"), "plugin", "b")
	newer := version(hours(2), "new")

	result := mergeOf(t, database(entry(first)), database(entry(newer, other)))

	_, conflict := theConflict(t, result)
	if !conflict.Close || conflict.Replaced {
		t.Errorf("close = %v, replaced = %v, want true and false", conflict.Close, conflict.Replaced)
	}
	if got := 1 + len(onlyEntry(t, result.Merged).History); got != 3 {
		t.Errorf("the result has %d versions, want all 3", got)
	}
}

func TestAHistoryVersionWhereNeitherHoldsTheOtherFollowsTheRuleOfTheLongGap(t *testing.T) {
	first := withCustomData(version(hours(1), "old"), "browser", "a")
	other := withCustomData(version(hours(1), "old"), "plugin", "b")
	newer := version(hours(100), "new")

	result := mergeOf(t, database(entry(first)), database(entry(newer, other)))

	wantNoQuestions(t, result)
	if got := theChange(t, result).Outcome; got != merge.OutcomeConflict {
		t.Errorf("outcome = %d, want a conflict", got)
	}
	wantVersions(t, onlyEntry(t, result.Merged), "new", "old", "old")
}

// A version without custom data is part of two others. It proves nothing about those two.
func TestAVersionThatIsPartOfTwoOthersLeavesThoseInConflict(t *testing.T) {
	first := withCustomData(version(hours(1), "same"), "browser", "a")
	second := withCustomData(version(hours(1), "same"), "plugin", "b")
	bare := version(hours(1), "same")

	for name, copies := range map[string][]*snapshot.Snapshot{
		"three main versions":      {database(entry(first)), database(entry(second)), database(entry(bare))},
		"in the history of a copy": {database(entry(first, bare)), database(entry(second))},
	} {
		t.Run(name, func(t *testing.T) {
			result := mergeOf(t, copies...)

			_, conflict := theConflict(t, result)
			if !conflict.Close {
				t.Errorf("close = %v, want true", conflict.Close)
			}
			merged := onlyEntry(t, result.Merged)
			if len(merged.History) != 1 {
				t.Fatalf("the result has %d versions, want the two larger ones", 1+len(merged.History))
			}
			if len(merged.CustomData) != 1 || len(merged.History[0].CustomData) != 1 {
				t.Errorf("the versions have custom data %q and %q", customDataOfVersion(&merged.EntryVersion), customDataOfVersion(&merged.History[0]))
			}
		})
	}
}

// That version says nothing against either of them, so one copy alone cannot make them disagree:
// here the copy that went on already holds both behind its main version.
func TestAVersionThatIsPartOfTwoOthersMakesNoConflictOfItsOwn(t *testing.T) {
	first := withCustomData(version(hours(1), "same"), "browser", "a")
	second := withCustomData(version(hours(1), "same"), "plugin", "b")
	bare := version(hours(1), "same")
	went := database(entry(version(hours(2), "new"), first, second))

	result := mergeOf(t, went, database(entry(bare)))

	wantNoQuestions(t, result)
	if !reflect.DeepEqual(result.Merged.Root.Entries[0].History, mergeOf(t, went).Merged.Root.Entries[0].History) {
		t.Errorf("the merge changed the history: %s", printed(result.Merged.Root.Entries[0].History))
	}
}

// The ticket speaks of a version in a history. A main version with less custom data ends up
// there too, so it goes the same way, and no key of a run can put the smaller version on top.
func TestTheSmallerOfTwoMainVersionsGoesIntoTheLargerWithAnyKey(t *testing.T) {
	larger := withCustomData(version(hours(1), "same"), "browser", "a", "plugin", "b")
	smaller := withCustomData(version(hours(1), "same"), "plugin", "b")

	for i := range 16 {
		in := input(named(database(entry(smaller)), database(entry(larger)))...)
		in.Key = merge.Key{0: 7, 1: byte(i)}

		result := mustRun(t, in)

		wantNoQuestions(t, result)
		merged := onlyEntry(t, result.Merged)
		wantCustomData(t, "the main version", &merged.EntryVersion, "browser", "a", "plugin", "b")
		if len(merged.History) != 0 {
			t.Errorf("key %d: the result has %d versions in its history, want none", i, len(merged.History))
		}
		if got := theChange(t, result); got.Outcome != merge.OutcomeUpdated {
			t.Errorf("key %d: outcome = %d, want updated", i, got.Outcome)
		}
	}
}

func TestAVersionThatWentIntoALargerOneLeavesItsUsage(t *testing.T) {
	smaller := version(hours(1), "same")
	smaller.Times.UsageCount, smaller.Times.LastAccess = 40, hours(50)
	smaller.Times.LocationChanged = hours(60)
	larger := withCustomData(version(hours(1), "same"), "browser", "a")
	larger.Times.UsageCount, larger.Times.LastAccess = 2, hours(3)

	for _, order := range [][]*snapshot.Snapshot{{database(entry(smaller)), database(entry(larger))}, {database(entry(larger)), database(entry(smaller))}} {
		merged := onlyEntry(t, mergeOf(t, order...).Merged)

		if merged.Times.UsageCount != 40 || !merged.Times.LastAccess.Equal(hours(50)) || !merged.Times.LocationChanged.Equal(hours(60)) {
			t.Errorf("times = %+v, want the largest usage of both", merged.Times)
		}
	}
}

func TestARepeatMergeWithEitherCopyChangesNothingAndAsksNothing(t *testing.T) {
	old, edited := keepassxcEdit()
	merged := mergeOf(t, old, edited).Merged

	for name, other := range map[string]*snapshot.Snapshot{"old": old, "edited": edited, "itself": merged} {
		t.Run(name, func(t *testing.T) {
			again := mergeOf(t, merged, other)

			wantNoQuestions(t, again)
			if !reflect.DeepEqual(again.Merged.Root, merged.Root) {
				t.Errorf("the merge changed the result:\n got %s\nwant %s", printed(again.Merged.Root), printed(merged.Root))
			}
		})
	}
}

// KeePassXC edits the result of a merge: that history version lacks the custom data too.
func TestAnEditOfTheResultOfAMergeIsALagAgain(t *testing.T) {
	old, edited := keepassxcEdit()
	merged := onlyEntry(t, mergeOf(t, old, edited).Merged)
	next := withCustomData(version(hours(3), "newest"), "browser", "settings")
	editedAgain := entry(next, merged.History[0], withCustomData(merged.EntryVersion))

	result := mergeOf(t, database(*merged), database(editedAgain))

	wantNoQuestions(t, result)
	wantVersions(t, onlyEntry(t, result.Merged), "newest", "old", "new")
	wantCustomData(t, "the version before", &onlyEntry(t, result.Merged).History[1], "browser", "settings")
}

// The clock anomaly of ticket #4 with the new match: the history of the copy that went on lacks
// the custom data of the version the clock puts on top. It is the same version, so this is the
// anomaly and not a conflict, whatever the gap.
func TestAClockAnomalySeesAVersionThroughMissingCustomData(t *testing.T) {
	for name, gap := range map[string]float64{"a close one": 1, "a long gap": 100} {
		t.Run(name, func(t *testing.T) {
			replaced := withCustomData(version(hours(gap), "replaced"), "browser", "a")
			behind := withCustomData(version(hours(0), "behind the clock"), "browser", "a")

			result := mergeOf(t, database(entry(replaced)), database(entry(behind, withCustomData(replaced))))

			_, conflict := theConflict(t, result)
			if !conflict.Replaced || conflict.Close || conflict.Future {
				t.Errorf("replaced = %v, close = %v, future = %v, want true, false and false", conflict.Replaced, conflict.Close, conflict.Future)
			}
			if len(conflict.Versions) != 2 || !conflict.Versions[0].Modified.Equal(hours(gap)) || !conflict.Versions[1].Modified.Equal(hours(0)) {
				t.Errorf("answers = %s", printed(conflict.Versions))
			}
			merged := onlyEntry(t, result.Merged)
			wantVersions(t, merged, "replaced", "behind the clock")
			wantCustomData(t, "the main version", &merged.EntryVersion, "browser", "a")
			if !theChange(t, result).Replaced {
				t.Error("the report does not say the version was replaced")
			}
		})
	}
}

// The answer to that question makes a new version, and a later merge does not undo it.
func TestAnAnswerToThatAnomalyHolds(t *testing.T) {
	replaced := withCustomData(version(hours(1), "replaced"), "browser", "a")
	behind := withCustomData(version(hours(0), "behind the clock"), "browser", "a")
	copies := []*snapshot.Snapshot{database(entry(replaced)), database(entry(behind, withCustomData(replaced)))}
	in := input(named(copies...)...)
	q, _ := theConflict(t, mustRun(t, in))
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}

	decided := mustRun(t, in).Merged

	if got := fieldValue(&onlyEntry(t, decided).EntryVersion, "Password"); got != "behind the clock" {
		t.Errorf("the main version has password %q", got)
	}
	for _, original := range copies {
		again := mergeOf(t, decided, original)
		wantNoQuestions(t, again)
		if !reflect.DeepEqual(again.Merged.Root, decided.Root) {
			t.Errorf("a later merge changed the decision:\n got %s\nwant %s", printed(again.Merged.Root), printed(decided.Root))
		}
	}
}

// keepassxc-cli gives an entry a new time without an edit, for example when it moves it to the
// bin. Such two main versions hold the same, so there is nothing to ask and nothing to report
// as a conflict.
func TestTheSameContentWithAnotherTimeIsNoConflict(t *testing.T) {
	for name, tc := range map[string]struct {
		first, second time.Time
	}{
		"close":               {hours(1), hours(2)},
		"the limit":           {hours(1), hours(25)},
		"a long gap":          {hours(1), hours(100)},
		"from the future":     {hours(1), now.Add(time.Hour)},
		"both are the future": {now.Add(time.Hour), now.Add(2 * time.Hour)},
	} {
		t.Run(name, func(t *testing.T) {
			first, second := version(tc.first, "same"), version(tc.second, "same")

			for _, order := range [][]*snapshot.Snapshot{{database(entry(first)), database(entry(second))}, {database(entry(second)), database(entry(first))}} {
				result := mergeOf(t, order...)

				wantNoQuestions(t, result)
				if got := theChange(t, result).Outcome; got != merge.OutcomeUpdated {
					t.Errorf("outcome = %d, want updated", got)
				}
				merged := onlyEntry(t, result.Merged)
				wantVersions(t, merged, "same", "same")
				if !merged.Times.LastModification.Equal(tc.second) || !merged.History[0].Times.LastModification.Equal(tc.first) {
					t.Errorf("main has time %s and history %s, want the newest on top", merged.Times.LastModification, merged.History[0].Times.LastModification)
				}
			}
		})
	}
}

// Custom data is content, so a new time on other custom data is a conflict as before.
func TestOtherCustomDataWithAnotherTimeIsAConflict(t *testing.T) {
	result := mergeOf(t, database(entry(version(hours(1), "same"))), database(entry(withCustomData(version(hours(2), "same"), "browser", "a"))))

	_, conflict := theConflict(t, result)
	if !conflict.Close {
		t.Errorf("close = %v, want true", conflict.Close)
	}
}

// The rule looks at each pair, so a third copy with other content is still a conflict.
func TestTheSameContentWithAnotherTimeHidesNoConflictWithAThirdCopy(t *testing.T) {
	result := mergeOf(t,
		database(entry(version(hours(1), "same"))),
		database(entry(version(hours(50), "same"))),
		database(entry(version(hours(51), "other"))),
	)

	_, conflict := theConflict(t, result)
	if !conflict.Close {
		t.Errorf("close = %v, want true", conflict.Close)
	}
	if len(conflict.Versions) != 3 {
		t.Errorf("answers = %s, want the other content and both times of the same one", printed(conflict.Versions))
	}
}

// A clock that puts the same content back on top changes nothing a person could want to undo.
func TestAClockAnomalyOnTheSameContentDoesNotAsk(t *testing.T) {
	result := mergeOf(t,
		database(entry(version(hours(5), "same"))),
		database(entry(version(hours(1), "same"), version(hours(5), "same"))),
	)

	wantNoQuestions(t, result)
	wantVersions(t, onlyEntry(t, result.Merged), "same", "same")
}

// A criterion of the ticket: the values of custom data stay out of the questions and the report.
func TestValuesOfCustomDataStayOutOfQuestionsAndReport(t *testing.T) {
	const firstValue, secondValue = "FIRST-CUSTOM-VALUE", "SECOND-CUSTOM-VALUE"
	first := withCustomData(version(hours(1), "same"), "browser", firstValue)
	second := withCustomData(version(hours(1), "same"), "browser", secondValue)
	in := input(named(database(entry(first)), database(entry(second)))...)
	q, _ := theConflict(t, mustRun(t, in))
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}

	for _, result := range []*merge.Result{mustRun(t, input(in.Copies...)), mustRun(t, in)} {
		_, conflict := theConflict(t, result)
		if !slices.Contains(conflict.Versions[1].Differences.Attributes, merge.AttributeCustomData) {
			t.Errorf("the answer does not say that the custom data differs: %s", printed(conflict.Versions[1]))
		}
		text := printed(result.Questions) + printed(result.Report)
		if strings.Contains(text, firstValue) || strings.Contains(text, secondValue) {
			t.Errorf("a value of custom data reached the questions or the report:\n%s", text)
		}
	}
}

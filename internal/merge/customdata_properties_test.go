package merge_test

import (
	"maps"
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// TestPropertyAnEditAsKeePassXCDoesIsALag: take any copy and edit any entry the way KeePassXC
// does, with the newest time: the history version lacks the custom data of the entry. The merge
// of the two asks nothing about that entry, the edit is main, and the old version keeps its data.
func TestPropertyAnEditAsKeePassXCDoesIsALag(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		old := rapid.SampledFrom(s.Copies).Draw(t, "copy").Snapshot
		edited := mergetest.Clone(old)
		var entries []*snapshot.Entry
		edited.Root.Walk(func(g *snapshot.Group) {
			for i := range g.Entries {
				entries = append(entries, &g.Entries[i])
			}
		})
		if len(entries) == 0 {
			t.Skip("the copy has no entries")
		}
		e := rapid.SampledFrom(entries).Draw(t, "entry")
		before := e.EntryVersion
		newest := time.Time{}
		for _, v := range versionsOfEntry(e) {
			if v.Times.LastModification.After(newest) {
				newest = v.Times.LastModification
			}
		}
		if !before.Times.LastModification.Equal(newest) {
			t.Skip("the entry has an anomaly of the clock of its own")
		}
		inHistory := before
		inHistory.CustomData = nil
		e.History = append(slices.Clone(e.History), inHistory)
		e.Fields = append(slices.Clone(e.Fields), snapshot.Field{Key: "Edited", Value: "by KeePassXC"})
		e.Times.LastModification = newest.Add(time.Second)

		result := runOn(t, merge.Input{
			Copies: []merge.Copy{{Name: "old", Snapshot: old}, {Name: "edited", Snapshot: edited}},
			Now:    s.Now,
			Key:    drawKey(t),
		})

		if conflict, asked := conflictQuestions(result.Questions)[e.UUID]; asked {
			t.Errorf("the edit of entry %s asks: %s", e.UUID, printed(conflict))
		}
		merged := allEntries(result.Merged)[e.UUID]
		if !merged.Times.LastModification.Equal(e.Times.LastModification) {
			t.Errorf("entry %s: the main version has time %s, want the edit", e.UUID, merged.Times.LastModification)
		}
		if !idsOf(versionsOfEntry(merged))[identify(&before)] {
			t.Errorf("entry %s lost the old version with its custom data", e.UUID)
		}
	})
}

// TestPropertyTheSetOfVersionsDoesNotDependOnTheKey: each run of the tool has its own key, and a
// key may only decide which of two equally new versions is on top. If it decided what stays, the
// same files would give another result, and another exit code of status, on each run.
func TestPropertyTheSetOfVersionsDoesNotDependOnTheKey(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		first := drawKey(t)
		second := merge.Key{0: 2, 1: rapid.Byte().Draw(t, "other key")}

		one := allEntries(runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: first}).Merged)
		other := allEntries(runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: second}).Merged)

		for uuid, e := range one {
			if got, want := idsOf(versionsOfEntry(other[uuid])), idsOf(versionsOfEntry(e)); !maps.Equal(got, want) {
				t.Errorf("entry %s holds other versions with another key:\n got %d\nwant %d", uuid, len(got), len(want))
			}
		}
	})
}

// TestPropertyAnOldCopyInALaterRunChangesNoVersion: the result of one run, merged in a later run
// with another key with any of its copies, holds the same versions.
func TestPropertyAnOldCopyInALaterRunChangesNoVersion(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		settled := runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}).Merged
		other := rapid.SampledFrom(s.Copies).Draw(t, "old copy")

		later := allEntries(runOn(t, merge.Input{
			Copies: []merge.Copy{{Name: "result", Snapshot: settled}, other},
			Now:    s.Now,
			Key:    merge.Key{0: 2, 1: rapid.Byte().Draw(t, "other key")},
		}).Merged)

		for uuid, e := range allEntries(settled) {
			if got, want := idsOf(versionsOfEntry(later[uuid])), idsOf(versionsOfEntry(e)); !maps.Equal(got, want) {
				t.Errorf("entry %s holds other versions in the later run:\n got %d\nwant %d", uuid, len(got), len(want))
			}
		}
	})
}

// TestPropertyTheSameContentIsNoConflict: when all main versions hold the same content, and the
// clock puts no other content on top, no time makes that a conflict, neither as a question nor in
// the report. A main version counts as the version that absorbs it.
func TestPropertyTheSameContentIsNoConflict(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")

		result := runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)})

		told := map[snapshot.UUID]merge.EntryChange{}
		for _, event := range result.Report {
			if change, ok := event.(merge.EntryChange); ok {
				told[change.Entry] = change
			}
		}
		asked := conflictQuestions(result.Questions)
		merged := allEntries(result.Merged)
		for uuid, holdings := range heldEntries(s.Copies) {
			if !holdSameContent(holdings) || mainIsReplaced(holdings, identify(&merged[uuid].EntryVersion)) {
				continue
			}
			if conflict, got := asked[uuid]; got {
				t.Errorf("entry %s has one content in all copies and asks: %s", uuid, printed(conflict))
			}
			if change := told[uuid]; change.Outcome == merge.OutcomeConflict || change.Outcome == merge.OutcomeAsked {
				t.Errorf("entry %s has one content in all copies and is told as a conflict", uuid)
			}
		}
	})
}

func holdSameContent(holdings []held) bool {
	tops := standing(versionsOfHoldings(holdings))
	contents := map[versionID]bool{}
	for _, o := range ordersOf(holdings, tops) {
		for id := range o.mains {
			contents[identifyContent(tops[id])] = true
		}
	}
	return len(contents) == 1
}

// A property says nothing about a case the generator never makes.
func TestScenariosReachEveryCaseOfCustomData(t *testing.T) {
	seen := map[string]int{}
	for i := range 500 {
		s := mergetest.Scenarios().Example(i)
		result, err := merge.Run(merge.Input{Copies: s.Copies, Now: s.Now, Key: testKey})
		if err != nil {
			t.Fatal(err)
		}
		merged := allEntries(result.Merged)
		asked := conflictQuestions(result.Questions)
		for uuid, holdings := range heldEntries(s.Copies) {
			seeCustomDataCases(seen, holdings, merged[uuid], asked)
		}
	}
	t.Logf("cases in 500 scenarios: %v", seen)
	for _, want := range []string{
		"a history version that goes into a main version of another copy",
		"a main version that goes into a main version of another copy",
		"a main version that goes into a history version of another copy",
		"a version that goes into two others",
		"a version in its own copy that goes into its main version",
		"two versions with the same time where neither holds the other",
		"a question about custom data alone",
		"a lag only through missing custom data",
		"an anomaly of the clock only through missing custom data",
		"two main versions with the same content and a short gap",
		"two main versions with the same content and a long gap",
		"an anomaly of the clock on the same content",
	} {
		if seen[want] == 0 {
			t.Errorf("no scenario reaches the case %q", want)
		}
	}
}

func seeCustomDataCases(seen map[string]int, holdings []held, merged *snapshot.Entry, asked map[snapshot.UUID]merge.Conflict) {
	tops := standing(versionsOfHoldings(holdings))
	orders := ordersOf(holdings, tops)
	for i, a := range holdings {
		for j, b := range holdings {
			if i == j {
				continue
			}
			if goesInto(a.main, b.main) {
				seen["a main version that goes into a main version of another copy"]++
			}
			for _, old := range b.versions[1:] {
				if goesInto(old, a.main) {
					seen["a history version that goes into a main version of another copy"]++
				}
				if goesInto(a.main, old) {
					seen["a main version that goes into a history version of another copy"]++
				}
			}
			if i < j && identifyContent(a.main) == identifyContent(b.main) && !a.main.Times.LastModification.Equal(b.main.Times.LastModification) {
				gap := a.main.Times.LastModification.Sub(b.main.Times.LastModification).Abs()
				if gap < day {
					seen["two main versions with the same content and a short gap"]++
				} else {
					seen["two main versions with the same content and a long gap"]++
				}
			}
		}
		for _, old := range a.versions[1:] {
			if goesInto(old, a.main) {
				seen["a version in its own copy that goes into its main version"]++
			}
		}
	}
	for _, v := range versionsOfHoldings(holdings) {
		if tops[identify(v)] == nil && len(standIDs(v, tops)) > 1 {
			seen["a version that goes into two others"]++
		}
	}
	for id, v := range tops {
		for other, w := range tops {
			if id < other && v.Times.LastModification.Equal(w.Times.LastModification) && len(customDataItems(v))+len(customDataItems(w)) > 0 && sameBesideCustomData(v, w) {
				seen["two versions with the same time where neither holds the other"]++
			}
		}
	}
	if merged == nil {
		return
	}
	if conflict, got := asked[merged.UUID]; got {
		if isOnlyCustomData(conflict) {
			seen["a question about custom data alone"]++
		}
		if conflict.Replaced && !exactlyInAHistory(holdings, identify(&merged.EntryVersion)) {
			seen["an anomaly of the clock only through missing custom data"]++
		}
	} else if lagsOnlyThroughCustomData(holdings) {
		seen["a lag only through missing custom data"]++
	}
	main := identify(&merged.EntryVersion)
	for _, o := range orders {
		if o.behind[main] && !mainIsReplaced(holdings, main) {
			seen["an anomaly of the clock on the same content"]++
		}
	}
}

func sameBesideCustomData(a, b *snapshot.EntryVersion) bool {
	first, second := *a, *b
	first.CustomData, second.CustomData = nil, nil
	return identify(&first) == identify(&second)
}

func isOnlyCustomData(conflict merge.Conflict) bool {
	for _, v := range conflict.Versions[1:] {
		if len(v.Differences.Fields) != 0 || !slices.Equal(v.Differences.Attributes, []merge.Attribute{merge.AttributeCustomData}) {
			return false
		}
	}
	return true
}

func exactlyInAHistory(holdings []held, id versionID) bool {
	return slices.ContainsFunc(holdings, func(h held) bool { return idsOf(h.versions[1:])[id] })
}

// lagsOnlyThroughCustomData says a main version of one copy has a match in the history of another
// copy and no exact one, and a main version there is another one.
func lagsOnlyThroughCustomData(holdings []held) bool {
	for i, a := range holdings {
		for j, b := range holdings {
			if i != j && slices.ContainsFunc(b.versions[1:], func(old *snapshot.EntryVersion) bool { return goesInto(old, a.main) }) && !idsOf(b.versions[1:])[identify(a.main)] {
				return true
			}
		}
	}
	return false
}

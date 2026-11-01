package merge_test

import (
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// A property says nothing about a case the generator never makes. So this test counts the
// cases in a fixed set of scenarios: every case the rules know must occur.
func TestScenariosReachEveryCase(t *testing.T) {
	seen := map[string]int{}
	for i := range 500 {
		s := mergetest.Scenarios().Example(i)
		result, err := merge.Run(merge.Input{Copies: s.Copies, Now: s.Now, Key: testKey})
		if err != nil {
			t.Fatal(err)
		}
		seePairCases(seen, s.Copies, result)
		seeGroupCases(seen, s.Copies, result)
		for _, q := range result.Questions {
			conflict, ok := q.Detail.(merge.Conflict)
			if !ok {
				continue
			}
			seen["question"]++
			if conflict.Close {
				seen["close versions"]++
			}
			if conflict.Future {
				seen["version from the future"]++
			}
			if conflict.Future && !conflict.Close {
				seen["version from the future with a long gap"]++
			}
			if conflict.Replaced && !conflict.Close && !conflict.Future {
				seen["a question only about a replaced main version"]++
			}
			if conflict.Replaced && (conflict.Close || conflict.Future) {
				seen["a replaced main version in a conflict"]++
			}
			if len(conflict.Versions) > 2 {
				seen["more than two versions in a question"]++
			}
			if conflict.Versions[0].Modified.Equal(conflict.Versions[1].Modified) {
				seen["versions with the same time"]++
			}
			for _, v := range conflict.Versions {
				if gap := conflict.Versions[0].Modified.Sub(v.Modified); gap > 0 && gap < day && gap >= day-3600e9 {
					seen["a gap just below a day"]++
				}
			}
		}
		for _, event := range result.Report {
			change, ok := event.(merge.EntryChange)
			if !ok {
				continue
			}
			switch change.Outcome {
			case merge.OutcomeNew:
				seen["entry in part of the copies"]++
			case merge.OutcomeUpdated:
				seen["copy that lags"]++
			case merge.OutcomeConflict:
				seen["conflict with a long gap"]++
			case merge.OutcomeAsked:
				seen["conflict with a question"]++
			}
			if change.Replaced {
				seen["replaced version back on top"]++
			}
			if isExactlyOneDayApart(change) {
				seen["versions exactly a day apart"]++
			}
		}
		if movesWithTheKey(t, s.Now, result.Merged) {
			seen["a main version that moves with the key"]++
		}
	}
	t.Logf("cases in 500 scenarios: %v", seen)
	for _, want := range []string{
		"question", "close versions", "version from the future", "version from the future with a long gap",
		"more than two versions in a question", "versions with the same time", "a gap just below a day",
		"entry in part of the copies", "copy that lags", "conflict with a long gap", "conflict with a question",
		"replaced version back on top", "versions exactly a day apart",
		"a question only about a replaced main version", "a replaced main version in a conflict",
		"a main version that moves with the key",
		"pair by title and user name", "pair by user name and host only", "pair by both", "pair with the same password",
		"pair with different passwords", "pair with the same content", "pair with the same password and another content",
		"pair in another spelling", "pair in one copy", "pair over two copies", "entry in two pairs",
		"pair with a conflict about an entry", "question about several pairs", "copies with different root groups",
		"a cycle of groups", "groups joined by their path", "a joined group with an entry", "a group moved in a copy",
		"a group renamed in a copy", "two bins joined", "a plain group on the path of the bin", "a group in the bin moved",
		"a setting changed", "a custom icon in part of the copies", "the same custom icon changed in two copies",
		"custom data in part of the copies", "the same custom data changed in two copies", "a limit of the history raised",
		"no limit of the history",
	} {
		if seen[want] == 0 {
			t.Errorf("no scenario reaches the case %q", want)
		}
	}
}

// seeGroupCases counts the cases of the rules of groups and of the settings.
func seeGroupCases(seen map[string]int, copies []merge.Copy, result *merge.Result) {
	if len(result.MergedGroups) > 0 {
		seen["groups joined by their path"]++
	}
	for _, event := range result.Report {
		if _, ok := event.(merge.GroupCycleBroken); ok {
			seen["a cycle of groups"]++
		}
	}
	for id := range differentBins(copies) {
		if head, joined := result.MergedGroups[id]; joined && head == result.Merged.Meta.RecycleBin {
			seen["two bins joined"]++
		}
	}
	places := map[string][]snapshot.UUID{}
	var walk func(g *snapshot.Group, path string)
	walk = func(g *snapshot.Group, path string) {
		path += "/" + g.Name
		places[path] = append(places[path], g.UUID)
		for i := range g.Groups {
			walk(&g.Groups[i], path)
		}
	}
	walk(&result.Merged.Root, "")
	for _, ids := range places {
		if len(ids) > 1 {
			seen["a plain group on the path of the bin"]++
		}
	}
	for _, c := range copies {
		c.Snapshot.Root.Walk(func(g *snapshot.Group) {
			if !g.Times.LastModification.IsZero() && g.Times.LastModification.After(g.Times.Creation) {
				seen["a group renamed in a copy"]++
			}
			if g.PreviousParentGroup != nil {
				seen["a group moved in a copy"]++
			}
			if _, joined := result.MergedGroups[g.UUID]; joined && len(g.Entries) > 0 {
				seen["a joined group with an entry"]++
			}
		})
		binned := groupsInBin(c.Snapshot)
		c.Snapshot.Root.Walk(func(g *snapshot.Group) {
			if _, in := binned[g.UUID]; in && g.PreviousParentGroup != nil {
				seen["a group in the bin moved"]++
			}
		})
		meta := &c.Snapshot.Meta
		if !meta.DatabaseNameChanged.IsZero() || !meta.DatabaseDescriptionChanged.IsZero() || !meta.DefaultUserNameChanged.IsZero() {
			seen["a setting changed"]++
		}
		if meta.HistoryMaxItems < 0 || meta.HistoryMaxSize < 0 {
			seen["no limit of the history"]++
		}
		if limit := result.Merged.Meta.HistoryMaxItems; limit > meta.HistoryMaxItems && meta.HistoryMaxItems > 0 {
			seen["a limit of the history raised"]++
		}
	}
	icons, data := map[snapshot.UUID]int{}, map[string]int{}
	iconTexts, dataTexts := map[snapshot.UUID]map[string]bool{}, map[string]map[string]bool{}
	for _, c := range copies {
		for _, icon := range c.Snapshot.Meta.CustomIcons {
			icons[icon.UUID]++
			if iconTexts[icon.UUID] == nil {
				iconTexts[icon.UUID] = map[string]bool{}
			}
			iconTexts[icon.UUID][icon.Data] = true
		}
		for _, item := range c.Snapshot.Meta.CustomData {
			if !item.IsService() {
				data[item.Key]++
				if dataTexts[item.Key] == nil {
					dataTexts[item.Key] = map[string]bool{}
				}
				dataTexts[item.Key][item.Value] = true
			}
		}
	}
	for id, count := range icons {
		if count < len(copies) {
			seen["a custom icon in part of the copies"]++
		}
		if len(iconTexts[id]) > 1 {
			seen["the same custom icon changed in two copies"]++
		}
	}
	for key, count := range data {
		if count < len(copies) {
			seen["custom data in part of the copies"]++
		}
		if len(dataTexts[key]) > 1 {
			seen["the same custom data changed in two copies"]++
		}
	}
}

// movesWithTheKey says another run would put another main version on top of this result.
func movesWithTheKey(t *testing.T, now time.Time, settled *snapshot.Snapshot) bool {
	t.Helper()
	before := allEntries(settled)
	for i := range 4 {
		result, err := merge.Run(merge.Input{
			Copies: []merge.Copy{{Name: "result", Snapshot: settled}},
			Now:    now,
			Key:    merge.Key{31: byte(i + 1)},
		})
		if err != nil {
			t.Fatal(err)
		}
		for uuid, e := range allEntries(result.Merged) {
			if identify(&e.EntryVersion) != identify(&before[uuid].EntryVersion) {
				return true
			}
		}
	}
	return false
}

func isExactlyOneDayApart(change merge.EntryChange) bool {
	for i, a := range change.Copies {
		for _, b := range change.Copies[i+1:] {
			if a.Holds && b.Holds && (a.Modified.Sub(b.Modified) == day || b.Modified.Sub(a.Modified) == day) {
				return true
			}
		}
	}
	return false
}

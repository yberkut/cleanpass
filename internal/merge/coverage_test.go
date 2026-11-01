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
	} {
		if seen[want] == 0 {
			t.Errorf("no scenario reaches the case %q", want)
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

package merge_test

import (
	"reflect"
	"testing"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

func TestOneCopyComesBackWithItsEntry(t *testing.T) {
	want := entry(version(hours(2), "new"), version(hours(1), "old"))

	got := onlyEntry(t, mergeOf(t, database(want)).Merged)

	if !reflect.DeepEqual(*got, want) {
		t.Errorf("entry = %+v, want %+v", *got, want)
	}
}

func TestEntryChangedInOneCopyIsUpdatedWithoutQuestion(t *testing.T) {
	older := version(hours(1), "old")
	newer := version(hours(2), "new")
	behind := database(entry(older))
	ahead := database(entry(newer, older))

	for name, snapshots := range map[string][]*snapshot.Snapshot{
		"behind first": {behind, ahead},
		"ahead first":  {ahead, behind},
	} {
		t.Run(name, func(t *testing.T) {
			result := mergeOf(t, snapshots...)
			wantNoQuestions(t, result)
			wantVersions(t, onlyEntry(t, result.Merged), "new", "old")
		})
	}
}

func TestHistoryOfAllCopiesIsJoined(t *testing.T) {
	v0 := version(hours(0), "v0")
	v1 := version(hours(1), "v1")
	v2 := version(hours(2), "v2")
	v3 := version(hours(3), "v3")

	result := mergeOf(t, database(entry(v3, v1, v2)), database(entry(v3, v0, v2)))

	wantNoQuestions(t, result)
	wantVersions(t, onlyEntry(t, result.Merged), "v3", "v0", "v1", "v2")
}

func TestLaggingWithLongGapIsNotAConflictEither(t *testing.T) {
	older := version(hours(0), "old")
	newer := version(hours(24*10), "new")

	result := mergeOf(t, database(entry(older)), database(entry(newer, older)))

	wantNoQuestions(t, result)
	wantVersions(t, onlyEntry(t, result.Merged), "new", "old")
}

func TestVersionInMainOfOneCopyAndHistoryOfItselfCountsOnce(t *testing.T) {
	same := version(hours(1), "same")

	result := mergeOf(t, database(entry(same, same)))

	wantVersions(t, onlyEntry(t, result.Merged), "same")
}

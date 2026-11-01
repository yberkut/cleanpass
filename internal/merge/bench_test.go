package merge_test

import (
	"fmt"
	"testing"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The spec wants a comparison of two copies with 5 000 entries within a second, without the
// KDF. The merge runs on both sides of that comparison, so it must stay far below that.
func BenchmarkRunOnFiveThousandEntries(b *testing.B) {
	const entries, historyLength = 5000, 5
	newCopy := func(edit bool) *snapshot.Snapshot {
		root := snapshot.Group{UUID: rootID, Name: "Root"}
		for i := range entries {
			var id snapshot.UUID
			id[14], id[15] = byte(i>>8), byte(i)
			e := snapshot.Entry{UUID: id, EntryVersion: version(hours(float64(historyLength)), fmt.Sprint("main-", i))}
			for j := range historyLength {
				e.History = append(e.History, version(hours(float64(j)), fmt.Sprint("old-", i, "-", j)))
			}
			if edit {
				e.History = append(e.History, e.EntryVersion)
				e.EntryVersion = version(hours(float64(historyLength+1)), fmt.Sprint("new-", i))
			}
			root.Entries = append(root.Entries, e)
		}
		return &snapshot.Snapshot{Root: root}
	}
	in := merge.Input{Copies: named(newCopy(false), newCopy(true)), Now: now, Key: testKey}

	for b.Loop() {
		if _, err := merge.Run(in); err != nil {
			b.Fatal(err)
		}
	}
}

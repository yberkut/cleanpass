package merge_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The spec wants a comparison of two copies with 5 000 entries within a second, without the
// KDF. The merge runs on both sides of that comparison, so it must stay far below that.
func BenchmarkRunOnFiveThousandEntries(b *testing.B) {
	in := merge.Input{Copies: named(fiveThousandEntries(0, false), fiveThousandEntries(0, true)), Now: now, Key: testKey}

	for b.Loop() {
		if _, err := merge.Run(in); err != nil {
			b.Fatal(err)
		}
	}
}

// After an import each entry of one copy is a similar pair with one entry of the other copy.
func BenchmarkRunOnFiveThousandPairs(b *testing.B) {
	in := merge.Input{Copies: named(fiveThousandEntries(0, false), fiveThousandEntries(1, true)), Now: now, Key: testKey}

	for b.Loop() {
		if _, err := merge.Run(in); err != nil {
			b.Fatal(err)
		}
	}
}

// fiveThousandEntries gives a copy with entries of different accounts. Its entries have UUIDs
// of their own if the copies have different values of uuidByte.
func fiveThousandEntries(uuidByte byte, edit bool) *snapshot.Snapshot {
	const entries, historyLength = 5000, 5
	root := snapshot.Group{UUID: rootID, Name: "Root"}
	for i := range entries {
		of := func(modified time.Time, password string) snapshot.EntryVersion {
			v := version(modified, password)
			v.Fields[0].Value = fmt.Sprint("Account ", i)
			return v
		}
		var id snapshot.UUID
		id[13], id[14], id[15] = uuidByte, byte(i>>8), byte(i)
		e := snapshot.Entry{UUID: id, EntryVersion: of(hours(float64(historyLength)), fmt.Sprint("main-", i))}
		for j := range historyLength {
			e.History = append(e.History, of(hours(float64(j)), fmt.Sprint("old-", i, "-", j)))
		}
		if edit {
			e.History = append(e.History, e.EntryVersion)
			e.EntryVersion = of(hours(float64(historyLength+1)), fmt.Sprint("new-", i))
		}
		root.Entries = append(root.Entries, e)
	}
	return &snapshot.Snapshot{Root: root}
}

package merge_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// All example tests live on one calendar. Now is far after every time stamp, unless a
// test wants a stamp from the future.
var (
	day0 = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	now  = day0.Add(30 * 24 * time.Hour)

	testKey = merge.Key{1, 2, 3}

	rootID = snapshot.UUID{15: 0xF0}
	mailID = snapshot.UUID{15: 0x01}
)

// hours gives the time stamp that many hours after day0.
func hours(n float64) time.Time {
	return day0.Add(time.Duration(n * float64(time.Hour)))
}

// version is a version of an entry with a password. A different password means different
// content, so tests name versions by their passwords.
func version(modified time.Time, password string) snapshot.EntryVersion {
	return snapshot.EntryVersion{
		Times: snapshot.Times{LastModification: modified, Creation: day0.Add(-48 * time.Hour)},
		Fields: []snapshot.Field{
			{Key: "Title", Value: "Mail"},
			{Key: "UserName", Value: "user"},
			{Key: "Password", Value: password, Protected: true},
		},
	}
}

func entry(main snapshot.EntryVersion, history ...snapshot.EntryVersion) snapshot.Entry {
	return snapshot.Entry{UUID: mailID, EntryVersion: main, History: history}
}

// database is a copy with the given entries in its root group.
func database(entries ...snapshot.Entry) *snapshot.Snapshot {
	return &snapshot.Snapshot{
		Root: snapshot.Group{UUID: rootID, Name: "Root", Entries: entries},
	}
}

// named gives the copies the names copy-1, copy-2 and so on.
func named(snapshots ...*snapshot.Snapshot) []merge.Copy {
	copies := make([]merge.Copy, len(snapshots))
	for i, s := range snapshots {
		copies[i] = merge.Copy{Name: fmt.Sprintf("copy-%d", i+1), Snapshot: s}
	}
	return copies
}

func input(copies ...merge.Copy) merge.Input {
	return merge.Input{Copies: copies, Now: now, Key: testKey}
}

func mustRun(t *testing.T, in merge.Input) *merge.Result {
	t.Helper()
	result, err := merge.Run(in)
	if err != nil {
		t.Fatalf("merge.Run: %v", err)
	}
	return result
}

// mergeOf merges the snapshots without decisions.
func mergeOf(t *testing.T, snapshots ...*snapshot.Snapshot) *merge.Result {
	t.Helper()
	return mustRun(t, input(named(snapshots...)...))
}

// onlyEntry returns the single entry of a merged database.
func onlyEntry(t *testing.T, s *snapshot.Snapshot) *snapshot.Entry {
	t.Helper()
	var found []*snapshot.Entry
	s.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			found = append(found, &g.Entries[i])
		}
	})
	if len(found) != 1 {
		t.Fatalf("the database has %d entries, want 1", len(found))
	}
	return found[0]
}

func fieldValue(v *snapshot.EntryVersion, key string) string {
	for _, f := range v.Fields {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}

// wantVersions checks the passwords of an entry: the main version, then its history from
// the oldest to the newest.
func wantVersions(t *testing.T, e *snapshot.Entry, main string, history ...string) {
	t.Helper()
	if got := fieldValue(&e.EntryVersion, "Password"); got != main {
		t.Errorf("main version has password %q, want %q", got, main)
	}
	got := make([]string, len(e.History))
	for i := range e.History {
		got[i] = fieldValue(&e.History[i], "Password")
	}
	if len(history) == 0 {
		history = []string{}
	}
	if !slices.Equal(got, history) {
		t.Errorf("history has passwords %q, want %q", got, history)
	}
}

func wantNoQuestions(t *testing.T, result *merge.Result) {
	t.Helper()
	if len(result.Questions) != 0 {
		t.Errorf("got %d questions, want none: %+v", len(result.Questions), result.Questions)
	}
}

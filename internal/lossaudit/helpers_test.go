package lossaudit_test

import (
	"slices"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

var (
	day0 = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	rootID = snapshot.UUID{15: 0xF0}
	mailID = snapshot.UUID{15: 0x01}
	bankID = snapshot.UUID{15: 0x02}
)

// hours gives the time stamp that many hours after day0.
func hours(n int) time.Time { return day0.Add(time.Duration(n) * time.Hour) }

// version is a version of an entry. A different password means different content, so
// tests name versions by their passwords.
func version(modified time.Time, password string) snapshot.EntryVersion {
	return snapshot.EntryVersion{
		Times: snapshot.Times{Creation: hours(-48), LastModification: modified},
		Fields: []snapshot.Field{
			{Key: "Title", Value: "Mail"},
			{Key: "UserName", Value: "user"},
			{Key: "Password", Value: password, Protected: true},
		},
	}
}

func entry(id snapshot.UUID, main snapshot.EntryVersion, history ...snapshot.EntryVersion) snapshot.Entry {
	return snapshot.Entry{UUID: id, EntryVersion: main, History: history}
}

// database is a copy with the given entries in its root group.
func database(entries ...snapshot.Entry) *snapshot.Snapshot {
	return &snapshot.Snapshot{Root: snapshot.Group{UUID: rootID, Name: "Root", Entries: entries}}
}

// audit checks one result against its copies, with nothing allowed to go.
func audit(result *snapshot.Snapshot, copies ...*snapshot.Snapshot) []lossaudit.Loss {
	return lossaudit.Audit(lossaudit.Input{Copies: copies, Result: result})
}

func wantLosses(t *testing.T, got []lossaudit.Loss, want ...lossaudit.Loss) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("losses:\n got %+v\nwant %+v", got, want)
	}
}

func versionLost(entry snapshot.UUID, modified time.Time) lossaudit.Loss {
	return lossaudit.Loss{Kind: lossaudit.VersionLost, Object: entry, Time: modified}
}

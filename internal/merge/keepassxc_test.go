package merge_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/testkit"
)

func openCopy(t *testing.T, db testkit.DB) *snapshot.Snapshot {
	t.Helper()
	s, err := gateway.Open(db.Path, gateway.Credentials{Password: []byte(db.Password)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// countVersions counts the main versions and the history of a database.
func countVersions(s *snapshot.Snapshot) int {
	count := 0
	s.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			count += 1 + len(g.Entries[i].History)
		}
	})
	return count
}

// A plain save by the real KeePassXC renews its service data, also on entries. That must
// not look like an edit: no new version and no question.
func TestAFileSavedAgainByKeePassXCHasTheSameVersions(t *testing.T) {
	dir := t.TempDir()
	original := testkit.DB{Path: filepath.Join(dir, "original.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	saved := testkit.DB{Path: filepath.Join(dir, "saved.kdbx"), Password: testkit.Password}
	data, err := os.ReadFile(original.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(saved.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	saved.Resave(t)

	before, after := openCopy(t, original), openCopy(t, saved)
	result := mustRun(t, merge.Input{
		Copies: []merge.Copy{{Name: "original", Snapshot: before}, {Name: "saved", Snapshot: after}},
		Now:    now,
		Key:    testKey,
	})

	wantNoQuestions(t, result)
	for _, event := range result.Report {
		t.Errorf("a plain save gave the report event %s", printed(event))
	}
	if got, want := countVersions(result.Merged), countVersions(before); got != want {
		t.Errorf("the merge has %d versions, the original has %d", got, want)
	}
	if got, want := result.Merged.Stats().Groups, before.Stats().Groups+1; got != want {
		t.Errorf("the merge has %d groups, want %d with the new one", got, want)
	}
}

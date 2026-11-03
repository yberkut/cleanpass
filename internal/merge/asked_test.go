package merge_test

import (
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// A person who asks to see a value of version 2 must get the value of the version the question
// calls 2, so each version the question names is the one VersionsAsked gives at that place.
func TestPropertyAQuestionGivesTheVersionsItNames(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		result := runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)})

		for i := range result.Questions {
			q := &result.Questions[i]
			versions := result.VersionsAsked(q.ID)
			want := wantedVersions(q)
			if len(versions) != len(want) {
				t.Fatalf("%s gives %d versions, want %d", q.ID, len(versions), len(want))
			}
			for j := range want {
				if got := versions[j].Times.LastModification; !want[j].modified.IsZero() && !got.Equal(want[j].modified) {
					t.Errorf("%s: version %d has the time %v, want %v", q.ID, j, got, want[j].modified)
				}
				if got := titleOfVersion(&versions[j]); got != want[j].title {
					t.Errorf("%s: version %d has the title %q, want %q", q.ID, j, got, want[j].title)
				}
			}
		}
	})
}

type namedVersion struct {
	modified time.Time
	title    string
}

func wantedVersions(q *merge.Question) []namedVersion {
	switch d := q.Detail.(type) {
	case merge.Conflict:
		names := make([]namedVersion, len(d.Versions))
		for i, v := range d.Versions {
			names[i] = namedVersion{v.Modified, v.Label.Title}
		}
		return names
	case merge.SimilarPair:
		return []namedVersion{{d.First.Modified, d.First.Label.Title}, {d.Second.Modified, d.Second.Label.Title}}
	case merge.Deletion:
		// A question about a deletion says when the newest content appeared, and a move to the bin
		// can give the same content a newer time. So only the title follows from the question.
		return []namedVersion{{time.Time{}, d.Label.Title}}
	}
	return nil
}

func titleOfVersion(v *snapshot.EntryVersion) string {
	for i := range v.Fields {
		if v.Fields[i].Key == "Title" && !v.Fields[i].Protected {
			return v.Fields[i].Value
		}
	}
	return ""
}

func TestAConflictGivesEachVersionWithItsSecretValue(t *testing.T) {
	older := version(hours(1), "older")
	newer := version(hours(2), "newer")
	result := mergeOf(t, database(entry(older)), database(entry(newer)))

	if len(result.Questions) != 1 {
		t.Fatalf("got %d questions, want 1", len(result.Questions))
	}
	versions := result.VersionsAsked(result.Questions[0].ID)
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions))
	}
	if got := passwordIn(&versions[0]); got != "newer" {
		t.Errorf("version 1 holds %q, want the main one", got)
	}
	if got := passwordIn(&versions[1]); got != "older" {
		t.Errorf("version 2 holds %q, want the other one", got)
	}
	if got := result.VersionsAsked("no such question"); got != nil {
		t.Errorf("a question nobody asked gives %d versions", len(got))
	}
}

func passwordIn(v *snapshot.EntryVersion) string {
	for i := range v.Fields {
		if v.Fields[i].Key == "Password" {
			return v.Fields[i].Value
		}
	}
	return ""
}

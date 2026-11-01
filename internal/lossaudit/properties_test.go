package lossaudit_test

import (
	"bytes"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

func drawKey(t *rapid.T) merge.Key {
	return merge.Key{0: 1, 1: rapid.Byte().Draw(t, "key")}
}

func runEngine(t *rapid.T, in merge.Input) *merge.Result {
	t.Helper()
	result, err := merge.Run(in)
	if err != nil {
		t.Fatalf("merge.Run: %v", err)
	}
	return result
}

func snapshotsOf(copies []merge.Copy) []*snapshot.Snapshot {
	result := make([]*snapshot.Snapshot, len(copies))
	for i := range copies {
		result[i] = copies[i].Snapshot
	}
	return result
}

// mergeWithoutDecisions gives the copies and the result of the engine on a new scenario.
func mergeWithoutDecisions(t *rapid.T) (copies []*snapshot.Snapshot, merged *snapshot.Snapshot) {
	s := mergetest.Scenarios().Draw(t, "scenario")
	return snapshotsOf(s.Copies), runEngine(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}).Merged
}

// TestPropertyNoLoss is the property of the spec. Each version of each entry of the input is
// in the result of the Merge Engine, with or without answers, and so is all the rest.
//
// The answers here are about conflicts only, because they can make no data leave. The
// decisions that can reach this audit through Allowed come with the tickets that connect it.
func TestPropertyNoLoss(t *testing.T) {
	answered := 0
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		for _, q := range runEngine(t, in).Questions {
			if _, isConflict := q.Detail.(merge.Conflict); isConflict && rapid.Bool().Draw(t, "answer") {
				choice := rapid.IntRange(0, q.Choices-1).Draw(t, "choice")
				in.Decisions = append(in.Decisions, merge.Decision{Question: q.ID, Choice: choice})
				answered++
			}
		}

		merged := runEngine(t, in).Merged

		if losses := lossaudit.Audit(lossaudit.Input{Copies: snapshotsOf(s.Copies), Result: merged}); len(losses) != 0 {
			t.Errorf("the audit found losses: %+v", losses)
		}
	})
	if answered == 0 {
		t.Error("no scenario answered a question, so the property says nothing about answers")
	}
}

// A corruption is a bug a merge could have. Its apply says whether this result has anything
// to corrupt.
type corruption struct {
	name  string
	want  lossaudit.Kind
	apply func(t *rapid.T, result *snapshot.Snapshot) bool
}

// corruptions holds a damage to each thing the audit guards. Some need data the generator
// does not make yet: see reachedToday.
var corruptions = []corruption{
	{"a history version gone", lossaudit.VersionLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		e, ok := pickEntry(t, s, hasHistory)
		if ok {
			i := rapid.IntRange(0, len(e.History)-1).Draw(t, "version")
			e.History = slices.Delete(e.History, i, i+1)
		}
		return ok
	}},
	{"a main version gone", lossaudit.VersionLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		e, ok := pickEntry(t, s, hasHistory)
		if ok {
			last := len(e.History) - 1
			e.EntryVersion, e.History = e.History[last], e.History[:last]
		}
		return ok
	}},
	{"an entry gone", lossaudit.VersionLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		e, ok := pickEntry(t, s, anyEntry)
		if ok {
			s.Root.Walk(func(g *snapshot.Group) {
				g.Entries = slices.DeleteFunc(g.Entries, func(other snapshot.Entry) bool { return other.UUID == e.UUID })
			})
		}
		return ok
	}},
	{"a field of a history version changed", lossaudit.VersionLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		e, ok := pickEntry(t, s, hasHistory)
		if ok {
			v := &e.History[rapid.IntRange(0, len(e.History)-1).Draw(t, "version")]
			v.Fields[rapid.IntRange(0, len(v.Fields)-1).Draw(t, "field")].Value += "-changed"
		}
		return ok
	}},
	{"a field of a history version gone", lossaudit.VersionLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		e, ok := pickEntry(t, s, hasHistory)
		if ok {
			v := &e.History[rapid.IntRange(0, len(e.History)-1).Draw(t, "version")]
			i := rapid.IntRange(0, len(v.Fields)-1).Draw(t, "field")
			v.Fields = slices.Delete(v.Fields, i, i+1)
		}
		return ok
	}},
	{"the time of a version changed", lossaudit.VersionLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		e, ok := pickEntry(t, s, anyEntry)
		if ok {
			v := pickVersion(t, e)
			v.Times.LastModification = v.Times.LastModification.Add(time.Second)
		}
		return ok
	}},
	{"a reference to an attachment gone", lossaudit.VersionLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		e, ok := pickEntry(t, s, hasAttachment)
		if ok {
			versions := slices.DeleteFunc(allVersions(e), func(v *snapshot.EntryVersion) bool { return len(v.Attachments) == 0 })
			v := versions[rapid.IntRange(0, len(versions)-1).Draw(t, "version")]
			v.Attachments = v.Attachments[1:]
		}
		return ok
	}},
	{"the bytes of an attachment gone", lossaudit.AttachmentLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		id, ok := pickAttachment(t, s)
		if ok {
			delete(s.Attachments, id)
		}
		return ok
	}},
	{"the bytes of an attachment changed", lossaudit.AttachmentLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		id, ok := pickAttachment(t, s)
		if ok {
			stored := s.Attachments[id]
			stored.Content = append(slices.Clone(stored.Content), 0xFF)
			s.Attachments[id] = stored
		}
		return ok
	}},
	{"a deletion record gone", lossaudit.DeletionLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		if len(s.Deletions) == 0 {
			return false
		}
		i := rapid.IntRange(0, len(s.Deletions)-1).Draw(t, "record")
		s.Deletions = slices.Delete(s.Deletions, i, i+1)
		return true
	}},
	{"a deletion record older", lossaudit.DeletionLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		if len(s.Deletions) == 0 {
			return false
		}
		record := &s.Deletions[rapid.IntRange(0, len(s.Deletions)-1).Draw(t, "record")]
		record.Time = record.Time.Add(-time.Second)
		return true
	}},
	{"a group gone", lossaudit.GroupLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		var below []snapshot.UUID
		s.Root.Walk(func(g *snapshot.Group) {
			if g.UUID != s.Root.UUID {
				below = append(below, g.UUID)
			}
		})
		if len(below) == 0 {
			return false
		}
		gone := rapid.SampledFrom(below).Draw(t, "group")
		s.Root.Walk(func(g *snapshot.Group) {
			g.Groups = slices.DeleteFunc(g.Groups, func(child snapshot.Group) bool { return child.UUID == gone })
		})
		return true
	}},
	{"the notes of a group changed", lossaudit.GroupFieldsLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		var all []*snapshot.Group
		s.Root.Walk(func(g *snapshot.Group) { all = append(all, g) })
		rapid.SampledFrom(all).Draw(t, "group").Notes += "-changed"
		return true
	}},
	{"a custom icon gone", lossaudit.CustomIconLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		icons := s.Meta.CustomIcons
		if len(icons) == 0 {
			return false
		}
		i := rapid.IntRange(0, len(icons)-1).Draw(t, "icon")
		s.Meta.CustomIcons = slices.Delete(icons, i, i+1)
		return true
	}},
	{"custom data gone", lossaudit.CustomDataLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		var keys []string
		for i := range s.Meta.CustomData {
			if !s.Meta.CustomData[i].IsService() {
				keys = append(keys, s.Meta.CustomData[i].Key)
			}
		}
		if len(keys) == 0 {
			return false
		}
		gone := rapid.SampledFrom(keys).Draw(t, "key")
		s.Meta.CustomData = slices.DeleteFunc(s.Meta.CustomData, func(item snapshot.CustomDataItem) bool { return item.Key == gone })
		return true
	}},
	{"the name of the database changed", lossaudit.SettingLost, func(t *rapid.T, s *snapshot.Snapshot) bool {
		s.Meta.DatabaseName += "-changed"
		return true
	}},
}

// reachedToday lists the corruptions the generator must reach. The others wait for moves of
// tickets #6 and #8: deletions, custom icons, custom data. Their example tests are in this
// package, and these corruptions start to run as soon as the moves exist.
var reachedToday = []string{
	"a history version gone", "a main version gone", "an entry gone",
	"a field of a history version changed", "a field of a history version gone",
	"the time of a version changed", "a reference to an attachment gone",
	"the bytes of an attachment gone", "the bytes of an attachment changed",
	"a group gone", "the notes of a group changed", "the name of the database changed",
}

func anyEntry(*snapshot.Entry) bool { return true }

func hasHistory(e *snapshot.Entry) bool { return len(e.History) > 0 }

func hasAttachment(e *snapshot.Entry) bool {
	return slices.ContainsFunc(allVersions(e), func(v *snapshot.EntryVersion) bool { return len(v.Attachments) > 0 })
}

func allVersions(e *snapshot.Entry) []*snapshot.EntryVersion {
	result := []*snapshot.EntryVersion{&e.EntryVersion}
	for i := range e.History {
		result = append(result, &e.History[i])
	}
	return result
}

func pickVersion(t *rapid.T, e *snapshot.Entry) *snapshot.EntryVersion {
	versions := allVersions(e)
	return versions[rapid.IntRange(0, len(versions)-1).Draw(t, "version")]
}

// pickEntry draws an entry the filter likes, if there is one.
func pickEntry(t *rapid.T, s *snapshot.Snapshot, filter func(*snapshot.Entry) bool) (*snapshot.Entry, bool) {
	var matching []*snapshot.Entry
	s.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			if filter(&g.Entries[i]) {
				matching = append(matching, &g.Entries[i])
			}
		}
	})
	if len(matching) == 0 {
		return nil, false
	}
	return matching[rapid.IntRange(0, len(matching)-1).Draw(t, "entry")], true
}

// pickAttachment draws the bytes of an attachment, in an order a replay can repeat.
func pickAttachment(t *rapid.T, s *snapshot.Snapshot) (snapshot.AttachmentID, bool) {
	if len(s.Attachments) == 0 {
		return snapshot.AttachmentID{}, false
	}
	ids := slices.SortedFunc(maps.Keys(s.Attachments), func(a, b snapshot.AttachmentID) int { return bytes.Compare(a[:], b[:]) })
	return ids[rapid.IntRange(0, len(ids)-1).Draw(t, "attachment")], true
}

func hasKind(losses []lossaudit.Loss, kind lossaudit.Kind) bool {
	return slices.ContainsFunc(losses, func(l lossaudit.Loss) bool { return l.Kind == kind })
}

// TestPropertyACorruptedResultIsCaught: each damage to a result of the engine gives a loss of
// its kind. So TestPropertyNoLoss cannot pass because the audit sees nothing. The list of
// losses holds no secret value and does not depend on the order of the copies.
func TestPropertyACorruptedResultIsCaught(t *testing.T) {
	applied := map[string]int{}
	rapid.Check(t, func(t *rapid.T) {
		copies, merged := mergeWithoutDecisions(t)
		for _, c := range corruptions {
			corrupted := mergetest.Clone(merged)
			if !c.apply(t, corrupted) {
				continue
			}
			applied[c.name]++

			losses := lossaudit.Audit(lossaudit.Input{Copies: copies, Result: corrupted})

			if !hasKind(losses, c.want) {
				t.Errorf("%s: the audit gave %+v, want a loss of kind %d", c.name, losses, c.want)
			}
			if text := fmt.Sprintf("%+v", losses); strings.Contains(text, mergetest.SecretMarker) {
				t.Errorf("%s: a secret reached the list of losses: %s", c.name, text)
			}
			backward := slices.Clone(copies)
			slices.Reverse(backward)
			if other := lossaudit.Audit(lossaudit.Input{Copies: backward, Result: corrupted}); !slices.Equal(other, losses) {
				t.Errorf("%s: the order of the copies changed the list:\n%+v\n%+v", c.name, other, losses)
			}
		}
	})
	for _, name := range reachedToday {
		if applied[name] == 0 {
			t.Errorf("no scenario reaches the corruption %q", name)
		}
	}
}

// drawEntries gives the UUIDs of all entries in a random order.
func drawEntries(t *rapid.T, s *snapshot.Snapshot) []snapshot.UUID {
	var ids []snapshot.UUID
	s.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			ids = append(ids, g.Entries[i].UUID)
		}
	})
	return rapid.Permutation(ids).Draw(t, "entries")
}

func removeEntry(s *snapshot.Snapshot, id snapshot.UUID) (gone snapshot.Entry) {
	s.Root.Walk(func(g *snapshot.Group) {
		g.Entries = slices.DeleteFunc(g.Entries, func(e snapshot.Entry) bool {
			if e.UUID == id {
				gone = e
				return true
			}
			return false
		})
	})
	return gone
}

// TestPropertyOnlyADecisionAboutThatEntryLetsItGo: an entry may go with a decision about it,
// and no decision about another entry helps.
func TestPropertyOnlyADecisionAboutThatEntryLetsItGo(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		copies, merged := mergeWithoutDecisions(t)
		ids := drawEntries(t, merged)
		if len(ids) == 0 {
			t.Skip("the result has no entries")
		}
		gone, others := ids[0], ids[1:]
		removeEntry(merged, gone)

		allowed := func(ids ...snapshot.UUID) []lossaudit.Loss {
			return lossaudit.Audit(lossaudit.Input{Copies: copies, Result: merged, Allowed: lossaudit.Allowed{Deleted: ids}})
		}

		if losses := allowed(gone); len(losses) != 0 {
			t.Errorf("the human deleted the entry, and the audit gave %+v", losses)
		}
		if losses := allowed(others...); !hasKind(losses, lossaudit.VersionLost) {
			t.Errorf("no decision covers the entry, and the audit gave %+v", losses)
		}
	})
}

// TestPropertyAnAbsorbedEntryLivesOnUnderItsSurvivor: the versions of an entry may move under
// another entry with a decision about that pair, and they must all arrive.
func TestPropertyAnAbsorbedEntryLivesOnUnderItsSurvivor(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		copies, merged := mergeWithoutDecisions(t)
		ids := drawEntries(t, merged)
		if len(ids) < 2 {
			t.Skip("the result has no pair of entries")
		}
		absorbed, survivor := ids[0], ids[1]
		gone := removeEntry(merged, absorbed)
		pair := lossaudit.Allowed{Absorbed: map[snapshot.UUID]snapshot.UUID{absorbed: survivor}}

		merged.Root.Walk(func(g *snapshot.Group) {
			for i := range g.Entries {
				if e := &g.Entries[i]; e.UUID == survivor {
					e.History = append(e.History, gone.EntryVersion)
					e.History = append(e.History, gone.History...)
				}
			}
		})

		if losses := lossaudit.Audit(lossaudit.Input{Copies: copies, Result: merged, Allowed: pair}); len(losses) != 0 {
			t.Errorf("the versions moved under the survivor, and the audit gave %+v", losses)
		}
		if losses := lossaudit.Audit(lossaudit.Input{Copies: copies, Result: merged}); !hasKind(losses, lossaudit.VersionLost) {
			t.Errorf("no decision moved the versions, and the audit gave %+v", losses)
		}
	})
}

// TestPropertyTheAuditChangesNothing: the writer still needs the result after the audit.
func TestPropertyTheAuditChangesNothing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		copies, merged := mergeWithoutDecisions(t)
		copiesBefore := make([]*snapshot.Snapshot, len(copies))
		for i, c := range copies {
			copiesBefore[i] = mergetest.Clone(c)
		}
		mergedBefore := mergetest.Clone(merged)

		lossaudit.Audit(lossaudit.Input{Copies: copies, Result: merged})

		if !reflect.DeepEqual(merged, mergedBefore) {
			t.Error("the audit changed the result")
		}
		if !reflect.DeepEqual(copies, copiesBefore) {
			t.Error("the audit changed a copy")
		}
	})
}

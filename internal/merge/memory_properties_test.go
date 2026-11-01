package merge_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// allowedBy gives what a merge lets leave its place, the way the tool passes it to the Loss Audit.
func allowedBy(result *merge.Result, decisions []merge.Decision) lossaudit.Allowed {
	allowed := lossaudit.Allowed{
		Absorbed:     map[snapshot.UUID]snapshot.UUID{},
		Deleted:      slices.Concat(slices.Collect(maps.Keys(allowedToGo(decisions))), result.RememberedDeletions),
		MergedGroups: result.MergedGroups,
	}
	for _, a := range result.Absorbed {
		allowed.Absorbed[a.Absorbed] = a.Into
	}
	return allowed
}

// TestPropertyNoLossWithTheMemory: a merge of the result with an old copy loses nothing, and
// nothing stays out but what the human has decided. An entry goes through the memory only if the
// human said so in the first merge, and only one with a record: the other ones lie in the bin.
func TestPropertyNoLossWithTheMemory(t *testing.T) {
	remembered := 0
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)
		first := runOn(t, in)
		own := merge.Copy{Name: "result", Snapshot: first.Merged}
		other := rapid.SampledFrom(s.Copies).Draw(t, "old copy")

		again := runOn(t, merge.Input{Copies: []merge.Copy{own, other}, Now: s.Now, Key: in.Key})

		allowed := allowedBy(again, nil)
		losses := lossaudit.Audit(lossaudit.Input{Copies: []*snapshot.Snapshot{first.Merged, other.Snapshot}, Result: again.Merged, Allowed: allowed})
		if len(losses) != 0 {
			t.Errorf("the audit found losses: %+v", losses)
		}
		chosen := allowedToGo(in.Decisions)
		for _, id := range again.RememberedDeletions {
			remembered++
			if !chosen[id] {
				t.Errorf("entry %s went through the memory, and the human did not delete it", id)
			}
		}
	})
	if remembered == 0 {
		t.Error("no scenario removed an entry through the memory, so the property says nothing about it")
	}
}

// TestPropertyTheMemoryHoldsOnlyUUIDsAndDates: with any answers, each item the merge adds is a
// decision with UUIDs and dates and nothing else.
func TestPropertyTheMemoryHoldsOnlyUUIDsAndDates(t *testing.T) {
	seen := map[string]int{}
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)

		merged := runOn(t, in).Merged

		wantOnlyUUIDsAndDates(t, merged)
		for _, item := range memoryItems(merged) {
			seen[strings.Split(item.Key, "/")[1]]++
			if strings.Contains(item.Key+item.Value, mergetest.SecretMarker) {
				t.Errorf("a secret reached the memory: %s", item.Key)
			}
		}
	})
	t.Logf("items of the memory by kind: %v", seen)
	for _, kind := range []string{"entries", "groups", "different", "rejected", "accepted", "replaced"} {
		if seen[kind] == 0 {
			t.Errorf("no scenario gives a decision of the kind %q", kind)
		}
	}
}

// TestPropertyValidTree is the property of the spec: no cycles, and each entry in a group that
// exists. The result is a tree by its form, so what can go wrong is a group or an entry that is
// twice in it, is not in it, or lies in another place than a place a copy names. One bin holds all
// bins of the copies, and no two plain groups stand on one path.
func TestPropertyValidTree(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)

		result := runOn(t, in)

		merged := result.Merged
		groups := map[snapshot.UUID]int{}
		entries := map[snapshot.UUID]int{}
		merged.Root.Walk(func(g *snapshot.Group) {
			groups[g.UUID]++
			for i := range g.Entries {
				entries[g.Entries[i].UUID]++
			}
		})
		for id, count := range groups {
			if count != 1 {
				t.Errorf("group %s is %d times in the tree", id, count)
			}
		}
		for id, count := range entries {
			if count != 1 {
				t.Errorf("entry %s is %d times in the tree", id, count)
			}
		}

		gone := allowedToGo(in.Decisions)
		records := newestRecords(s.Copies)
		into := map[snapshot.UUID]snapshot.UUID{}
		for _, a := range result.Absorbed {
			into[a.Absorbed] = a.Into
		}
		for _, c := range s.Copies {
			c.Snapshot.Root.Walk(func(g *snapshot.Group) {
				if g != &c.Snapshot.Root && groups[placeInResult(g.UUID, result)] == 0 {
					t.Errorf("group %s of %s is not in the tree", g.UUID, c.Name)
				}
				for i := range g.Entries {
					id := g.Entries[i].UUID
					if head, absorbed := into[id]; absorbed {
						id = head
					}
					_, recorded := records[id]
					if entries[id] == 0 && !(gone[id] && recorded) {
						t.Errorf("entry %s of %s is not in the tree", g.Entries[i].UUID, c.Name)
					}
				}
			})
			if bin := c.Snapshot.Meta.RecycleBin; !bin.IsZero() && placeInResult(bin, result) != merged.Meta.RecycleBin {
				t.Errorf("the bin %s of %s is not the bin %s of the result", bin, c.Name, merged.Meta.RecycleBin)
			}
		}
		if bin := merged.Meta.RecycleBin; !bin.IsZero() && groups[bin] != 1 {
			t.Errorf("the bin %s of the result is %d times in the tree", bin, groups[bin])
		}

		merged.Root.Walk(func(g *snapshot.Group) {
			names := map[string]snapshot.UUID{}
			for i := range g.Groups {
				child := &g.Groups[i]
				if child.UUID == merged.Meta.RecycleBin {
					continue
				}
				if other, taken := names[child.Name]; taken {
					t.Errorf("groups %s and %s stand on one path", other, child.UUID)
				}
				names[child.Name] = child.UUID
			}
		})
	})
}

// TestPropertyTheLimitsHoldTheHistory: the next edit of any entry cuts nothing, and no limit of a
// copy goes down. The real KeePassXC checks the count in TestKeePassXCDoesNotCutTheMergedHistory.
func TestPropertyTheLimitsHoldTheHistory(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")

		merged := runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}).Merged

		items, size := merged.Meta.HistoryMaxItems, merged.Meta.HistoryMaxSize
		for _, e := range allEntries(merged) {
			if versions := int64(1 + len(e.History)); items >= 0 && items < versions {
				t.Errorf("entry %s has %d versions, and the limit is %d", e.UUID, versions, items)
			}
		}
		for _, c := range s.Copies {
			meta := &c.Snapshot.Meta
			if (meta.HistoryMaxItems < 0) != (items < 0) && meta.HistoryMaxItems < 0 {
				t.Errorf("%s has no limit of items, and the result has %d", c.Name, items)
			}
			if (meta.HistoryMaxSize < 0) != (size < 0) && meta.HistoryMaxSize < 0 {
				t.Errorf("%s has no limit of the size, and the result has %d", c.Name, size)
			}
			if items >= 0 && items < meta.HistoryMaxItems {
				t.Errorf("the limit of items went down from %d to %d", meta.HistoryMaxItems, items)
			}
			if size >= 0 && size < meta.HistoryMaxSize {
				t.Errorf("the limit of the size went down from %d to %d", meta.HistoryMaxSize, size)
			}
		}
	})
}

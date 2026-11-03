package merge_test

import (
	"fmt"
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

// goneByTheMemory gives the entries that left this merge because of an answer of an earlier one.
func goneByTheMemory(result *merge.Result) []snapshot.UUID {
	merged := allEntries(result.Merged)
	var gone []snapshot.UUID
	for _, event := range result.Events {
		if change, ok := event.(merge.DeletionChange); ok && change.Remembered && change.Outcome == merge.DeletionApplied && merged[change.Entry] == nil {
			gone = append(gone, change.Entry)
		}
	}
	return gone
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

		losses := lossaudit.Audit(lossaudit.Input{Copies: []*snapshot.Snapshot{first.Merged, other.Snapshot}, Result: again.Merged})
		if len(losses) != 0 {
			t.Errorf("the audit found losses: %+v", losses)
		}
		chosen := allowedToGo(in.Decisions)
		for _, id := range goneByTheMemory(again) {
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

// TestPropertyAnOldUUIDIsNamedInTheReport: each entry a copy still holds under a UUID an earlier
// merge took in has its word in the report, with that copy, and no line says the copy lacks it.
func TestPropertyAnOldUUIDIsNamedInTheReport(t *testing.T) {
	named := 0
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, onlyPairs(runOn(t, in).Questions))
		first := runOn(t, in)
		into := map[snapshot.UUID]snapshot.UUID{}
		for _, a := range first.Absorbed {
			into[a.Absorbed] = a.Into
		}
		old := rapid.SampledFrom(s.Copies).Draw(t, "old copy")

		again := runOn(t, merge.Input{Copies: []merge.Copy{{Name: "result", Snapshot: first.Merged}, old}, Now: s.Now, Key: in.Key})

		events := map[snapshot.UUID]merge.PairRemembered{}
		changes := map[snapshot.UUID]merge.EntryChange{}
		for _, event := range again.Events {
			switch e := event.(type) {
			case merge.PairRemembered:
				events[e.Entry] = e
			case merge.EntryChange:
				changes[e.Entry] = e
			}
		}
		for absorbed, head := range into {
			if len(heldEntries([]merge.Copy{old})[absorbed]) == 0 {
				continue
			}
			named++
			e := events[head]
			if !slices.Contains(e.Absorbed, absorbed) || !slices.Contains(e.Copies, old.Name) {
				t.Errorf("entry %s of %s has no word in the report: %s", absorbed, old.Name, printed(e))
			}
			if change, told := changes[head]; told && slices.ContainsFunc(change.Copies, func(c merge.CopyState) bool { return !c.Holds }) {
				t.Errorf("the report says a copy lacks entry %s: %s", head, printed(change.Copies))
			}
		}
	})
	if named == 0 {
		t.Error("no scenario has a copy with an old UUID")
	}
}

// TestPropertyAnEditNobodySawAsksAfterAnAcceptedDeletion: an old copy gets an edit after the human
// has deleted the entry, stamped by a clock that runs behind. The edit has an older time than any
// version. It raises the question, it stays in the result, and the Loss Audit agrees.
func TestPropertyAnEditNobodySawAsksAfterAnAcceptedDeletion(t *testing.T) {
	edits := 0
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)
		result := runOn(t, in).Merged
		merged := allEntries(result)

		for uuid := range allowedToGo(in.Decisions) {
			holdings := heldEntries(s.Copies)[uuid]
			if merged[uuid] != nil || len(holdings) == 0 {
				continue
			}
			oldest := holdings[0].main.Times.LastModification
			for _, h := range holdings {
				for _, v := range h.versions {
					if v.Times.LastModification.Before(oldest) {
						oldest = v.Times.LastModification
					}
				}
			}
			for i, c := range s.Copies {
				if len(heldEntries([]merge.Copy{c})[uuid]) == 0 {
					continue
				}
				stale := mergetest.Clone(c.Snapshot)
				e := allEntries(stale)[uuid]
				e.History = append(e.History, e.EntryVersion)
				e.Fields = append(slices.Clone(e.Fields), snapshot.Field{Key: "Edit", Value: "nobody saw this"})
				e.Times.LastModification = oldest.Add(-time.Hour)
				edits++

				again := runOn(t, merge.Input{
					Copies: []merge.Copy{{Name: "result", Snapshot: result}, {Name: fmt.Sprintf("edited %d", i), Snapshot: stale}},
					Now:    s.Now,
					Key:    in.Key,
				})

				if _, asked := deletionQuestions(again.Questions)[uuid]; !asked {
					t.Errorf("entry %s: the edit raised no question", uuid)
				}
				kept := allEntries(again.Merged)[uuid]
				if kept == nil {
					t.Fatalf("entry %s: the edit is gone with the entry", uuid)
				}
				if !idsOf(versionsOfEntry(kept))[identify(&e.EntryVersion)] {
					t.Errorf("entry %s lost the edit", uuid)
				}
				copies := []*snapshot.Snapshot{result, stale}
				if losses := lossaudit.Audit(lossaudit.Input{Copies: copies, Result: again.Merged}); len(losses) != 0 {
					t.Errorf("the audit found losses: %+v", losses)
				}
			}
		}
	})
	if edits == 0 {
		t.Error("no scenario gave an edit after an accepted deletion")
	}
}

// TestPropertyTheMemoryHoldsOnlyUUIDsAndDates: with any answers, each item the merge adds is a
// decision with UUIDs and dates and nothing else.
func TestPropertyTheMemoryHoldsOnlyUUIDsAndDates(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)

		merged := runOn(t, in).Merged

		wantOnlyUUIDsAndDates(t, merged)
		for _, item := range memoryItems(merged) {
			if strings.Contains(item.Key+item.Value, mergetest.SecretMarker) {
				t.Errorf("a secret reached the memory: %s", item.Key)
			}
		}
	})
}

// A property says nothing about a kind of decision the generator never reaches. This looks at a
// fixed set of scenarios and not at the random ones of the property: a question that leaves an
// item of the kind "replaced" comes in about two scenarios of a hundred, so a random hundred can
// hold none, and a test must not fail on that.
func TestScenariosReachEveryKindOfDecisionInTheMemory(t *testing.T) {
	seen := map[string]int{}
	for i := range 500 {
		s := mergetest.Scenarios().Example(i)
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: testKey}
		first, err := merge.Run(in)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range first.Questions {
			// Answer 0 keeps what the rule gives, so an order of versions goes to the memory. The
			// other answers come in turn, for the kinds that need them.
			choice := 0
			if _, isConflict := q.Detail.(merge.Conflict); !isConflict {
				choice = i % q.Choices
			}
			in.Decisions = append(in.Decisions, merge.Decision{Question: q.ID, Choice: choice})
		}
		result, err := merge.Run(in)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range memoryItems(result.Merged) {
			seen[strings.Split(item.Key, "/")[1]]++
		}
	}
	t.Logf("items of the memory by kind in 500 scenarios: %v", seen)
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

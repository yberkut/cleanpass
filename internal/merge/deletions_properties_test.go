package merge_test

import (
	"cmp"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

func deletionQuestions(questions []merge.Question) map[snapshot.UUID]merge.Deletion {
	result := map[snapshot.UUID]merge.Deletion{}
	for _, q := range questions {
		if d, ok := q.Detail.(merge.Deletion); ok {
			result[d.Entry] = d
		}
	}
	return result
}

// newestRecords joins the records of the copies, written apart from the engine.
func newestRecords(copies []merge.Copy) map[snapshot.UUID]time.Time {
	result := map[snapshot.UUID]time.Time{}
	for _, c := range copies {
		for _, d := range c.Snapshot.Deletions {
			if d.Time.After(result[d.UUID]) {
				result[d.UUID] = d.Time
			}
		}
	}
	return result
}

func inAndOutOfTheBin(holdings []held) bool {
	in := slices.ContainsFunc(holdings, func(h held) bool { return h.inBin })
	out := slices.ContainsFunc(holdings, func(h held) bool { return !h.inBin })
	return in && out
}

// allowedToGo gives the entries the decisions allow to disappear.
func allowedToGo(decisions []merge.Decision) map[snapshot.UUID]bool {
	result := map[snapshot.UUID]bool{}
	for _, d := range decisions {
		if id, ok := d.DeletesEntry(); ok {
			result[id] = true
		}
	}
	return result
}

// whenTheNewestContentAppeared walks the versions from the newest down, as long as they have the
// content of the newest one and no other content has the same time. A new time on the same
// content is no change. It is written apart from the engine.
func whenTheNewestContentAppeared(holdings []held) time.Time {
	contentAt := map[time.Time]map[versionID]bool{}
	for _, h := range holdings {
		for _, v := range h.versions {
			content := *v
			content.Times.LastModification = time.Time{}
			at := v.Times.LastModification.UTC()
			if contentAt[at] == nil {
				contentAt[at] = map[versionID]bool{}
			}
			contentAt[at][identify(&content)] = true
		}
	}
	times := slices.SortedFunc(maps.Keys(contentAt), func(a, b time.Time) int { return b.Compare(a) })
	first := times[0]
	if len(contentAt[first]) > 1 {
		return first
	}
	for _, at := range times[1:] {
		if len(contentAt[at]) > 1 || !maps.Equal(contentAt[at], contentAt[times[0]]) {
			break
		}
		first = at
	}
	return first
}

// TestPropertyQuestionsAboutDeletionsDoNotDisappear: each record against an entry and each
// entry in the bin of one copy and outside it in another gives a question, whatever the times
// say, with the right dates. An answer does not take it away. Anything else gives none.
func TestPropertyQuestionsAboutDeletionsDoNotDisappear(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}

		silent := runOn(t, in)

		asked := deletionQuestions(silent.Questions)
		records := newestRecords(s.Copies)
		entries := heldEntries(s.Copies)
		for uuid, holdings := range entries {
			_, isRecorded := records[uuid]
			d, got := asked[uuid]
			if want := isRecorded || inAndOutOfTheBin(holdings); got != want {
				t.Errorf("entry %s: question = %v, want %v", uuid, got, want)
			}
			if !got {
				continue
			}
			deleted := records[uuid]
			for _, h := range holdings {
				if h.recycled.After(deleted) {
					deleted = h.recycled
				}
			}
			modified := whenTheNewestContentAppeared(holdings)
			if !d.Deleted.Equal(deleted) || !d.Modified.Equal(modified) {
				t.Errorf("entry %s: deleted = %s, modified = %s, want %s and %s", uuid, d.Deleted, d.Modified, deleted, modified)
			}
		}
		for uuid := range asked {
			if len(entries[uuid]) == 0 {
				t.Errorf("a question about entry %s, which no copy holds", uuid)
			}
		}

		in.Decisions = drawDecisions(t, silent.Questions)
		if answered := runOn(t, in); !reflect.DeepEqual(answered.Questions, silent.Questions) {
			t.Errorf("the answers changed the questions:\n%s\n%s", printed(answered.Questions), printed(silent.Questions))
		}
	})
}

// TestPropertyOnlyTheAnswerDeleteRemovesAnEntry: an entry with a record goes with the answer
// and with nothing else. Every entry that stays holds every version of every copy. A merged
// pair has an entry leave too, which TestPropertyNoLossWithPairs covers.
func TestPropertyOnlyTheAnswerDeleteRemovesAnEntry(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, withoutPairs(runOn(t, in).Questions))

		merged := allEntries(runOn(t, in).Merged)

		allowed := allowedToGo(in.Decisions)
		records := newestRecords(s.Copies)
		for uuid, holdings := range heldEntries(s.Copies) {
			_, isRecorded := records[uuid]
			e, stays := merged[uuid]
			if want := !allowed[uuid] || !isRecorded; stays != want {
				t.Errorf("entry %s: stays = %v, want %v", uuid, stays, want)
			}
			if !stays {
				continue
			}
			got := idsOf(versionsOfEntry(e))
			for _, h := range holdings {
				for id := range idsOf(h.versions) {
					if !got[id] {
						t.Errorf("entry %s lost a version", uuid)
					}
				}
			}
		}
	})
}

// TestPropertyRecordsOfTheResult: the result has the newest record about each object, sorted,
// whatever the human answers.
func TestPropertyRecordsOfTheResult(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)

		result := runOn(t, in).Merged

		var want []snapshot.Deletion
		for uuid, stamp := range newestRecords(s.Copies) {
			want = append(want, snapshot.Deletion{UUID: uuid, Time: stamp})
		}
		slices.SortFunc(want, func(a, b snapshot.Deletion) int {
			return cmp.Compare(a.UUID.String(), b.UUID.String())
		})
		if !reflect.DeepEqual(result.Deletions, want) {
			t.Errorf("records = %s, want %s", printed(result.Deletions), printed(want))
		}
	})
}

// TestPropertyTheAuditAcceptsOnlyTheAnswerDelete: with the entries the decisions name, the Loss
// Audit finds nothing in a result with any answers. Without them it finds exactly the entries
// that went, so the answer delete is the only way for data to go.
func TestPropertyTheAuditAcceptsOnlyTheAnswerDelete(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, withoutPairs(runOn(t, in).Questions))
		copies := make([]*snapshot.Snapshot, len(s.Copies))
		for i, c := range s.Copies {
			copies[i] = c.Snapshot
		}

		result := runOn(t, in).Merged

		allowed := lossaudit.Allowed{Deleted: slices.Collect(maps.Keys(allowedToGo(in.Decisions)))}
		if losses := lossaudit.Audit(lossaudit.Input{Copies: copies, Result: result, Allowed: allowed}); len(losses) != 0 {
			t.Errorf("the audit found losses with the decisions: %+v", losses)
		}
		gone := map[snapshot.UUID]bool{}
		merged := allEntries(result)
		for uuid := range heldEntries(s.Copies) {
			if merged[uuid] == nil {
				gone[uuid] = true
			}
		}
		found := map[snapshot.UUID]bool{}
		for _, loss := range lossaudit.Audit(lossaudit.Input{Copies: copies, Result: result}) {
			if loss.Kind != lossaudit.VersionLost && loss.Kind != lossaudit.AttachmentLost {
				t.Errorf("the audit found a loss of another kind: %+v", loss)
			}
			found[loss.Object] = true
		}
		if !maps.Equal(found, gone) {
			t.Errorf("without decisions the audit found losses in %v, the entries that went are %v", found, gone)
		}
	})
}

// TestPropertyTheBinHoldsWhatStaysDeleted: without the answer delete, an entry a copy holds
// outside the bin lies outside it. An entry every copy holds in the bin does not come back to
// life, and the answer delete puts the others there.
func TestPropertyTheBinHoldsWhatStaysDeleted(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, withoutPairs(runOn(t, in).Questions))

		result := runOn(t, in).Merged

		allowed := allowedToGo(in.Decisions)
		binned := groupsInBin(result)
		merged := allEntries(result)
		for uuid, holdings := range heldEntries(s.Copies) {
			if merged[uuid] == nil {
				continue
			}
			_, got := binned[groupsWithEntry(result, uuid)[0].UUID]
			want := allowed[uuid] || !slices.ContainsFunc(holdings, func(h held) bool { return !h.inBin })
			if got != want {
				t.Errorf("entry %s: in the bin = %v, want %v", uuid, got, want)
			}
		}
	})
}

// A property says nothing about a case the generator never makes.
func TestScenariosReachEveryCaseOfADeletion(t *testing.T) {
	seen := map[string]int{}
	for i := range 500 {
		s := mergetest.Scenarios().Example(i)
		result, err := merge.Run(merge.Input{Copies: s.Copies, Now: s.Now, Key: testKey})
		if err != nil {
			t.Fatal(err)
		}
		records := newestRecords(s.Copies)
		for _, d := range deletionQuestions(result.Questions) {
			recorded, recycled, outside := len(d.Recorded) > 0, len(d.Recycled) > 0, len(d.Held) > 0
			switch {
			case recorded && recycled && outside:
				seen["a record, the bin and a place outside it"]++
			case recorded && outside:
				seen["a record against an entry"]++
			case recorded:
				seen["a record against an entry in the bin"]++
			default:
				seen["the bin against a place outside it"]++
			}
			if at, ok := records[d.Entry]; ok && at.After(d.Modified) {
				seen["a record newer than the last change"]++
			}
			if at, ok := records[d.Entry]; ok && at.Before(d.Modified) {
				seen["a record older than the last change"]++
			}
			if _, inConflict := conflictQuestions(result.Questions)[d.Entry]; inConflict {
				seen["a deletion of an entry in conflict"]++
			}
		}
		times := map[snapshot.UUID]time.Time{}
		for _, c := range s.Copies {
			for _, d := range c.Snapshot.Deletions {
				if at, ok := times[d.UUID]; ok && !at.Equal(d.Time) {
					seen["two records with different times"]++
				}
				times[d.UUID] = d.Time
			}
		}
		if len(result.Merged.Deletions) > 0 {
			seen["a record of something gone in all copies"]++
		}
		countBinCases(seen, s.Copies, result.Merged)
	}
	t.Logf("cases in 500 scenarios: %v", seen)
	for _, want := range []string{
		"a record against an entry", "a record against an entry in the bin", "the bin against a place outside it",
		"a record, the bin and a place outside it", "a record newer than the last change", "a record older than the last change",
		"a deletion of an entry in conflict", "two records with different times", "a record of something gone in all copies",
		"an entry only in the bin", "an entry in a group in the bin", "a group in the bin of one copy and outside it in another",
		"an entry that left its group for the bin", "a bin only some copies have", "a record about a group that stays",
	} {
		if seen[want] == 0 {
			t.Errorf("no scenario reaches the case %q", want)
		}
	}
}

func countBinCases(seen map[string]int, copies []merge.Copy, merged *snapshot.Snapshot) {
	entries := allEntries(merged)
	for uuid, holdings := range heldEntries(copies) {
		if !slices.ContainsFunc(holdings, func(h held) bool { return !h.inBin }) {
			seen["an entry only in the bin"]++
			if entries[uuid] != nil && !slices.ContainsFunc(holdings, func(h held) bool { return h.group == groupsWithEntry(merged, uuid)[0].UUID }) {
				seen["an entry that left its group for the bin"]++
			}
		}
		for _, h := range holdings {
			if h.inBin && h.group != merged.Meta.RecycleBin {
				seen["an entry in a group in the bin"]++
			}
		}
	}
	inBin, outside, bins := map[snapshot.UUID]bool{}, map[snapshot.UUID]bool{}, 0
	for _, c := range copies {
		binned := groupsInBin(c.Snapshot)
		if len(binned) > 0 {
			bins++
		}
		c.Snapshot.Root.Walk(func(g *snapshot.Group) {
			if _, in := binned[g.UUID]; in {
				inBin[g.UUID] = true
			} else {
				outside[g.UUID] = true
			}
		})
	}
	for id := range inBin {
		if outside[id] {
			seen["a group in the bin of one copy and outside it in another"]++
		}
	}
	if bins > 0 && bins < len(copies) {
		seen["a bin only some copies have"]++
	}
	for uuid := range newestRecords(copies) {
		if inBin[uuid] || outside[uuid] {
			seen["a record about a group that stays"]++
		}
	}
}

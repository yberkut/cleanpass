package merge_test

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

func drawKey(t *rapid.T) merge.Key {
	return merge.Key{0: 1, 1: rapid.Byte().Draw(t, "key")}
}

func runOn(t *rapid.T, in merge.Input) *merge.Result {
	t.Helper()
	result, err := merge.Run(in)
	if err != nil {
		t.Fatalf("merge.Run: %v", err)
	}
	return result
}

// drawDecisions answers some of the questions.
func drawDecisions(t *rapid.T, questions []merge.Question) []merge.Decision {
	var decisions []merge.Decision
	for _, q := range questions {
		if rapid.Bool().Draw(t, "answer") {
			decisions = append(decisions, merge.Decision{Question: q.ID, Choice: rapid.IntRange(0, q.Choices-1).Draw(t, "choice")})
		}
	}
	return decisions
}

func wantSameSnapshot(t *rapid.T, got, want *snapshot.Snapshot) {
	t.Helper()
	if !reflect.DeepEqual(got.Root, want.Root) {
		t.Errorf("the tree differs:\n got %s\nwant %s", printed(got.Root), printed(want.Root))
	}
	if !reflect.DeepEqual(got.Meta, want.Meta) {
		t.Errorf("the settings differ:\n got %s\nwant %s", printed(got.Meta), printed(want.Meta))
	}
	if !reflect.DeepEqual(got.Deletions, want.Deletions) {
		t.Errorf("the deletions differ:\n got %s\nwant %s", printed(got.Deletions), printed(want.Deletions))
	}
	if !reflect.DeepEqual(got.Attachments, want.Attachments) {
		t.Errorf("the attachments differ: got %d, want %d", len(got.Attachments), len(want.Attachments))
	}
}

// TestPropertyCommutative: a permutation of the copies changes nothing, not even the
// questions and the report.
func TestPropertyCommutative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)
		first := runOn(t, in)

		in.Copies = rapid.Permutation(s.Copies).Draw(t, "order")
		second := runOn(t, in)

		wantSameSnapshot(t, second.Merged, first.Merged)
		if !reflect.DeepEqual(second.Questions, first.Questions) {
			t.Errorf("the questions differ:\n got %s\nwant %s", printed(second.Questions), printed(first.Questions))
		}
		if !reflect.DeepEqual(second.Report, first.Report) {
			t.Errorf("the report differs:\n got %s\nwant %s", printed(second.Report), printed(first.Report))
		}
	})
}

// TestPropertyAssociative: merging in parts gives what merging at once gives.
func TestPropertyAssociative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		key := drawKey(t)
		merged := func(copies []merge.Copy) *snapshot.Snapshot {
			return runOn(t, merge.Input{Copies: copies, Now: s.Now, Key: key}).Merged
		}
		at := rapid.IntRange(1, len(s.Copies)-1).Draw(t, "split")

		atOnce := merged(s.Copies)
		inParts := merged([]merge.Copy{
			{Name: "left", Snapshot: merged(s.Copies[:at])},
			{Name: "right", Snapshot: merged(s.Copies[at:])},
		})
		oneByOne := merged(s.Copies[:1])
		for _, next := range s.Copies[1:] {
			oneByOne = merged([]merge.Copy{{Name: "so far", Snapshot: oneByOne}, next})
		}

		wantSameSnapshot(t, inParts, atOnce)
		wantSameSnapshot(t, oneByOne, atOnce)
	})
}

// TestPropertyIdempotent: merging the result with any of its copies, or with itself, changes
// nothing and asks nothing, with or without decisions. One exception waits for the memory of
// decisions: a copy that still holds what the human decided about a deletion asks again, and
// the same answer gives the same result.
func TestPropertyIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)
		result := runOn(t, in)
		own := merge.Copy{Name: "result", Snapshot: result.Merged}

		for _, other := range append(slices.Clone(s.Copies), own) {
			pair := merge.Input{Copies: []merge.Copy{own, other}, Now: s.Now, Key: in.Key}
			for _, q := range runOn(t, pair).Questions {
				if _, isDeletion := q.Detail.(merge.Deletion); !isDeletion || other.Name == own.Name {
					t.Errorf("the merge with %s asks again: %s", other.Name, printed(q))
				}
				if i := slices.IndexFunc(in.Decisions, func(d merge.Decision) bool { return d.Question == q.ID }); i >= 0 {
					pair.Decisions = append(pair.Decisions, in.Decisions[i])
				}
			}

			wantSameSnapshot(t, runOn(t, pair).Merged, result.Merged)
		}
	})
}

// A version as the spec sees it: its time and its content. The engine has its own way to
// tell, with a keyed fingerprint. This one is written apart from it.
type versionID string

func identify(v *snapshot.EntryVersion) versionID {
	normal := *v
	normal.Times.LastAccess, normal.Times.UsageCount = time.Time{}, 0
	normal.Times.LocationChanged, normal.PreviousParentGroup = time.Time{}, nil
	normal.Fields = slices.Clone(v.Fields)
	slices.SortFunc(normal.Fields, func(a, b snapshot.Field) int { return strings.Compare(a.Key, b.Key) })
	normal.Attachments = slices.Clone(v.Attachments)
	slices.SortFunc(normal.Attachments, func(a, b snapshot.AttachmentRef) int {
		return strings.Compare(a.Name+string(a.ID[:]), b.Name+string(b.ID[:]))
	})
	normal.CustomData = nil
	for _, item := range v.CustomData {
		if !item.IsService() {
			item.LastModification = time.Time{}
			normal.CustomData = append(normal.CustomData, item)
		}
	}
	slices.SortFunc(normal.CustomData, func(a, b snapshot.CustomDataItem) int {
		return strings.Compare(a.Key+"\x00"+a.Value, b.Key+"\x00"+b.Value)
	})
	tags := strings.FieldsFunc(v.Tags, func(r rune) bool { return r == ',' || r == ';' })
	slices.Sort(tags)
	normal.Tags = strings.Join(tags, ",")
	data, err := json.Marshal(normal)
	if err != nil {
		panic(err)
	}
	return versionID(data)
}

// held is what one copy holds of an entry, as the spec sees it.
type held struct {
	main     *snapshot.EntryVersion
	mainID   versionID
	history  map[versionID]bool
	versions []*snapshot.EntryVersion
	group    snapshot.UUID
	inBin    bool
	// recycled is the time the entry or a group around it went to the bin.
	recycled time.Time
}

// groupsInBin gives the groups in the recycle bin of a copy, the bin included, with the time
// each one went there.
func groupsInBin(s *snapshot.Snapshot) map[snapshot.UUID]time.Time {
	result := map[snapshot.UUID]time.Time{}
	var mark func(g *snapshot.Group, since time.Time)
	mark = func(g *snapshot.Group, since time.Time) {
		result[g.UUID] = since
		for i := range g.Groups {
			child := &g.Groups[i]
			if child.Times.LocationChanged.After(since) {
				mark(child, child.Times.LocationChanged)
			} else {
				mark(child, since)
			}
		}
	}
	s.Root.Walk(func(g *snapshot.Group) {
		if g.UUID == s.Meta.RecycleBin {
			mark(g, time.Time{})
		}
	})
	return result
}

func heldEntries(copies []merge.Copy) map[snapshot.UUID][]held {
	result := map[snapshot.UUID][]held{}
	for _, c := range copies {
		binned := groupsInBin(c.Snapshot)
		c.Snapshot.Root.Walk(func(g *snapshot.Group) {
			for i := range g.Entries {
				e := &g.Entries[i]
				h := held{main: &e.EntryVersion, mainID: identify(&e.EntryVersion), history: map[versionID]bool{}, group: g.UUID}
				if since, inBin := binned[g.UUID]; inBin {
					h.inBin, h.recycled = true, since
					if e.Times.LocationChanged.After(since) {
						h.recycled = e.Times.LocationChanged
					}
				}
				h.versions = append(h.versions, &e.EntryVersion)
				for j := range e.History {
					h.versions = append(h.versions, &e.History[j])
					if id := identify(&e.History[j]); id != h.mainID {
						h.history[id] = true
					}
				}
				result[e.UUID] = append(result[e.UUID], h)
			}
		})
	}
	return result
}

// mustAsk says, from the words of the spec alone, whether the entry raises a question: two
// main versions differ, no copy holds one of them behind the other, and they are less than
// 24 hours apart or one of them comes from the future.
func mustAsk(holdings []held, now time.Time) bool {
	replaced := func(old, by versionID) bool {
		for _, h := range holdings {
			if h.mainID == by && h.history[old] {
				return true
			}
		}
		return false
	}
	for i, a := range holdings {
		for _, b := range holdings[i+1:] {
			if a.mainID == b.mainID || replaced(a.mainID, b.mainID) || replaced(b.mainID, a.mainID) {
				continue
			}
			first, second := a.main.Times.LastModification, b.main.Times.LastModification
			gap := first.Sub(second)
			if gap < 0 {
				gap = -gap
			}
			if gap < 24*time.Hour || first.After(now) || second.After(now) {
				return true
			}
		}
	}
	return false
}

func conflictQuestions(questions []merge.Question) map[snapshot.UUID]merge.Conflict {
	result := map[snapshot.UUID]merge.Conflict{}
	for _, q := range questions {
		if conflict, ok := q.Detail.(merge.Conflict); ok {
			result[conflict.Entry] = conflict
		}
	}
	return result
}

// TestPropertyQuestionsDoNotDisappear: each conflict within 24 hours and each stamp from the
// future in a conflict gives a question. Anything else, a copy that lags first of all, gives none.
func TestPropertyQuestionsDoNotDisappear(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")

		result := runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)})

		asked := conflictQuestions(result.Questions)
		for uuid, holdings := range heldEntries(s.Copies) {
			_, got := asked[uuid]
			if want := mustAsk(holdings, s.Now); got != want {
				t.Errorf("entry %s: question = %v, want %v", uuid, got, want)
			}
		}
	})
}

func allEntries(s *snapshot.Snapshot) map[snapshot.UUID]*snapshot.Entry {
	result := map[snapshot.UUID]*snapshot.Entry{}
	s.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			result[g.Entries[i].UUID] = &g.Entries[i]
		}
	})
	return result
}

func versionsOfEntry(e *snapshot.Entry) []*snapshot.EntryVersion {
	result := []*snapshot.EntryVersion{&e.EntryVersion}
	for i := range e.History {
		result = append(result, &e.History[i])
	}
	return result
}

func idsOf(versions []*snapshot.EntryVersion) map[versionID]bool {
	result := map[versionID]bool{}
	for _, v := range versions {
		result[identify(v)] = true
	}
	return result
}

// TestPropertyTheSetOfVersionsIsTheUnion: the result holds exactly the versions of all copies,
// each once, and the newest by the clock is main.
func TestPropertyTheSetOfVersionsIsTheUnion(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")

		merged := allEntries(runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}).Merged)

		for uuid, holdings := range heldEntries(s.Copies) {
			want := map[versionID]bool{}
			newest := time.Time{}
			for _, h := range holdings {
				maps.Copy(want, idsOf(h.versions))
				for _, v := range h.versions {
					if v.Times.LastModification.After(newest) {
						newest = v.Times.LastModification
					}
				}
			}
			e := merged[uuid]
			if e == nil {
				t.Fatalf("entry %s is gone", uuid)
			}
			all := versionsOfEntry(e)
			got := idsOf(all)
			if !maps.Equal(got, want) {
				t.Errorf("entry %s has versions\n%v\nwant\n%v", uuid, slices.Collect(maps.Keys(got)), slices.Collect(maps.Keys(want)))
			}
			if len(all) != len(got) {
				t.Errorf("entry %s has %d versions and %d different ones", uuid, len(all), len(got))
			}
			if !e.Times.LastModification.Equal(newest) {
				t.Errorf("entry %s: main version has time %s, newest is %s", uuid, e.Times.LastModification, newest)
			}
		}
	})
}

// holdingsOutsideTheBin gives the holdings that decide the place of a kept entry: those outside
// the recycle bin, or all of them if every copy holds the entry in the bin.
func holdingsOutsideTheBin(holdings []held) []held {
	outside := slices.DeleteFunc(slices.Clone(holdings), func(h held) bool { return h.inBin })
	if len(outside) == 0 {
		return holdings
	}
	return outside
}

// TestPropertyUsageIsTheMaximum: the main version has the largest values of all versions of
// all copies, and the entry lies where a copy with the newest move has it. A move to the bin
// does not count against a copy that holds the entry outside the bin.
func TestPropertyUsageIsTheMaximum(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		result := runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)})
		merged := allEntries(result.Merged)

		for uuid, holdings := range heldEntries(s.Copies) {
			var access, located time.Time
			var count int64
			for _, h := range holdings {
				for _, v := range h.versions {
					if v.Times.LastAccess.After(access) {
						access = v.Times.LastAccess
					}
					count = max(count, v.Times.UsageCount)
				}
			}
			deciding := holdingsOutsideTheBin(holdings)
			for _, h := range deciding {
				for _, v := range h.versions {
					if v.Times.LocationChanged.After(located) {
						located = v.Times.LocationChanged
					}
				}
			}
			e := merged[uuid]
			if !e.Times.LastAccess.Equal(access) || e.Times.UsageCount != count || !e.Times.LocationChanged.Equal(located) {
				t.Errorf("entry %s has times %+v, want access %s, count %d, located %s", uuid, e.Times, access, count, located)
			}
			// An entry deleted in every copy that holds it may leave its group, see
			// TestPropertyTheBinHoldsWhatStaysDeleted.
			if !deciding[0].inBin {
				wantGroupFromNewestMove(t, deciding, result.Merged, uuid, located)
			}
		}
	})
}

func groupsWithEntry(s *snapshot.Snapshot, uuid snapshot.UUID) []*snapshot.Group {
	var result []*snapshot.Group
	s.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			if g.Entries[i].UUID == uuid {
				result = append(result, g)
			}
		}
	})
	return result
}

func wantGroupFromNewestMove(t *rapid.T, holdings []held, merged *snapshot.Snapshot, uuid snapshot.UUID, located time.Time) {
	t.Helper()
	group := groupsWithEntry(merged, uuid)[0].UUID
	for _, h := range holdings {
		if h.group != group {
			continue
		}
		for _, v := range h.versions {
			if v.Times.LocationChanged.Equal(located) {
				return
			}
		}
	}
	t.Errorf("entry %s lies in group %s, and no copy has it there with the newest move %s", uuid, group, located)
}

// TestPropertyAnAnswerHolds: the content the human chose is main, each original version stays,
// and a merge with any old copy keeps it that way without a question.
func TestPropertyAnAnswerHolds(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		asked := runOn(t, in).Questions
		var answers []merge.Question
		for _, q := range asked {
			if _, isConflict := q.Detail.(merge.Conflict); isConflict {
				answers = append(answers, q)
			}
		}
		in.Decisions = drawDecisions(t, answers)
		merged := allEntries(runOn(t, in).Merged)
		holdings := heldEntries(s.Copies)

		byID := map[merge.QuestionID]merge.Question{}
		for _, q := range asked {
			byID[q.ID] = q
		}
		for _, d := range in.Decisions {
			conflict := byID[d.Question].Detail.(merge.Conflict)
			e := merged[conflict.Entry]
			if chosen := conflict.Versions[d.Choice].Modified; !sameContentAt(holdings[conflict.Entry], &e.EntryVersion, chosen) {
				t.Errorf("entry %s: the main version does not have the content of the version of %s", conflict.Entry, chosen)
			}
			got := idsOf(versionsOfEntry(e))
			for _, h := range holdings[conflict.Entry] {
				for id := range idsOf(h.versions) {
					if !got[id] {
						t.Errorf("entry %s lost an original version", conflict.Entry)
					}
				}
			}
		}
	})
}

// sameContentAt says the version has the content of a held version with the given time.
func sameContentAt(holdings []held, v *snapshot.EntryVersion, at time.Time) bool {
	content := *v
	content.Times.LastModification = at
	want := identify(&content)
	for _, h := range holdings {
		if idsOf(h.versions)[want] {
			return true
		}
	}
	return false
}

// TestPropertyNoSecretInQuestionsOrReport: the secret values stay out of what a person sees.
func TestPropertyNoSecretInQuestionsOrReport(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")

		result := runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)})

		if text := printed(result.Questions) + printed(result.Report); strings.Contains(text, mergetest.SecretMarker) {
			t.Errorf("a secret reached the questions or the report:\n%s", text)
		}
	})
}

// TestPropertyInputStaysAsItIs: the merge reads its input and changes none of it.
func TestPropertyInputStaysAsItIs(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		before := make([]*snapshot.Snapshot, len(s.Copies))
		for i, c := range s.Copies {
			before[i] = mergetest.Clone(c.Snapshot)
		}
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)

		runOn(t, in)

		for i, c := range s.Copies {
			if !reflect.DeepEqual(c.Snapshot, before[i]) {
				t.Errorf("the merge changed copy %s", c.Name)
			}
		}
	})
}

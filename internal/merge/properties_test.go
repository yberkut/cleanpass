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

// coincidences is what a merge concludes from the tree it sees, and what a tree can no longer
// show: which groups stood on one path, and which group left a cycle.
type coincidences struct {
	joined map[snapshot.UUID]snapshot.UUID
	broken map[snapshot.UUID]bool
}

// coincidencesOf gives what the steps of a merge in parts concluded. The memory carries the
// joins to the last step, and the report tells the cycles of each step.
func coincidencesOf(steps ...*merge.Result) coincidences {
	seen := coincidences{joined: steps[len(steps)-1].MergedGroups, broken: map[snapshot.UUID]bool{}}
	for _, step := range steps {
		for _, event := range step.Report {
			if cycle, ok := event.(merge.GroupCycleBroken); ok {
				seen.broken[cycle.Group] = true
			}
		}
	}
	return seen
}

// TestPropertyAssociative: merging in parts gives what merging at once gives.
//
// There is one exception, and it has no way out. Two groups on one path, or a cycle, are a matter
// of the tree a part sees. Another part can move or rename one of those groups, and then the tree
// of all copies has no such thing. The part has joined the groups or moved one of them by then.
// Taking that back needs the old name in the memory, and the spec forbids names there. So a
// scenario where the parts conclude other things than the whole stays out, and only such a one.
func TestPropertyAssociative(t *testing.T) {
	checked, skipped := 0, 0
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		key := drawKey(t)
		run := func(copies ...merge.Copy) *merge.Result {
			return runOn(t, merge.Input{Copies: copies, Now: s.Now, Key: key})
		}
		at := rapid.IntRange(1, len(s.Copies)-1).Draw(t, "split")

		atOnce := run(s.Copies...)
		left, right := run(s.Copies[:at]...), run(s.Copies[at:]...)
		inParts := run(merge.Copy{Name: "left", Snapshot: left.Merged}, merge.Copy{Name: "right", Snapshot: right.Merged})
		steps := []*merge.Result{run(s.Copies[0])}
		for _, next := range s.Copies[1:] {
			steps = append(steps, run(merge.Copy{Name: "so far", Snapshot: steps[len(steps)-1].Merged}, next))
		}
		oneByOne := steps[len(steps)-1]

		whole := coincidencesOf(atOnce)
		for _, tc := range []struct {
			got   *merge.Result
			steps []*merge.Result
		}{{inParts, []*merge.Result{left, right, inParts}}, {oneByOne, steps}} {
			if !reflect.DeepEqual(coincidencesOf(tc.steps...), whole) {
				skipped++
				continue
			}
			checked++
			wantSameSnapshot(t, tc.got.Merged, atOnce.Merged)
		}
	})
	t.Logf("%d merges in parts compared, %d concluded other things than the whole", checked, skipped)
	if skipped > checked/4 {
		t.Errorf("%d of %d merges in parts stayed out, so the property says little", skipped, checked+skipped)
	}
}

// questionsToBeGone gives the questions the decisions have closed. An answer about all pairs with
// the same content closes the pairs it names if it merges them. Its own ID says nothing: the
// question exists as long as such a pair is open. A pair stays open if a merge would have made
// one entry of two others the human wants kept apart, and the report says so.
func questionsToBeGone(decisions []merge.Decision, result *merge.Result) map[merge.QuestionID]bool {
	gone := map[merge.QuestionID]bool{}
	for _, d := range decisions {
		gone[d.Question] = true
	}
	for _, q := range result.Questions {
		if bulk, ok := q.Detail.(merge.IdenticalPairs); ok {
			if gone[q.ID] && slices.Contains(decisions, merge.Decision{Question: q.ID, Choice: 1}) {
				for _, pair := range bulk.Pairs {
					gone[pair] = true
				}
			}
			delete(gone, q.ID)
		}
	}
	for _, event := range result.Report {
		if open, ok := event.(merge.PairNotMerged); ok {
			delete(gone, merge.QuestionID("pair:"+open.First.Entry.String()+":"+open.Second.Entry.String()))
		}
	}
	return gone
}

// TestPropertyIdempotent: merging the result with any of its copies, or with itself, changes
// nothing, with any decisions and also later. No question the human has answered comes back, so
// the merge needs no decisions. The other questions may stay, and a main version that this copy
// has replaced is the only conflict left.
func TestPropertyIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)
		result := runOn(t, in)
		gone := questionsToBeGone(in.Decisions, result)
		own := merge.Copy{Name: "result", Snapshot: result.Merged}
		merged := allEntries(result.Merged)
		later := s.Now.Add(rapid.SampledFrom([]time.Duration{0, day}).Draw(t, "time to the next merge"))

		for _, other := range append(slices.Clone(s.Copies), own) {
			pair := merge.Input{Copies: []merge.Copy{own, other}, Now: later, Key: in.Key}
			again := runOn(t, pair)

			wantSameSnapshot(t, again.Merged, result.Merged)
			for _, q := range again.Questions {
				if gone[q.ID] {
					t.Errorf("the merge with %s asks %s again", other.Name, q.ID)
				}
			}
			asked := conflictQuestions(again.Questions)
			for uuid, holdings := range heldEntries(pair.Copies) {
				if merged[uuid] == nil {
					continue // The human deleted it or merged it, and only an old copy still holds it.
				}
				conflict, got := asked[uuid]
				answered := gone[merge.QuestionID("conflict:"+uuid.String())]
				if want := !answered && mainIsReplaced(holdings, identify(&merged[uuid].EntryVersion)); got != want {
					t.Errorf("the merge with %s, entry %s: question = %v, want %v", other.Name, uuid, got, want)
				}
				if got && (conflict.Close || conflict.Future) {
					t.Errorf("the merge with %s, entry %s: a conflict is back: %s", other.Name, uuid, printed(conflict))
				}
			}
		}

		// All old copies in one merge raise nothing that one at a time does not raise.
		all := runOn(t, merge.Input{Copies: append([]merge.Copy{own}, s.Copies...), Now: later, Key: in.Key})
		wantSameSnapshot(t, all.Merged, result.Merged)
		for _, q := range all.Questions {
			if gone[q.ID] {
				t.Errorf("the merge with all old copies asks %s again", q.ID)
			}
			if conflict, ok := q.Detail.(merge.Conflict); ok && (conflict.Close || conflict.Future) {
				t.Errorf("the merge with all old copies, entry %s: a conflict is back: %s", conflict.Entry, printed(conflict))
			}
		}
	})
}

// TestScenariosReachAConflictAnEarlierMergeSettled: the case of two old copies in one merge must
// occur, or the last part of TestPropertyIdempotent says nothing.
func TestScenariosReachAConflictAnEarlierMergeSettled(t *testing.T) {
	reached := 0
	for i := range 500 {
		s := mergetest.Scenarios().Example(i)
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: testKey}
		first, err := merge.Run(in)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range first.Questions {
			if conflict, ok := q.Detail.(merge.Conflict); ok && conflict.Close {
				in.Decisions = append(in.Decisions, merge.Decision{Question: q.ID, Choice: len(conflict.Versions) - 1})
			}
		}
		result, err := merge.Run(in)
		if err != nil {
			t.Fatal(err)
		}
		own := heldEntries([]merge.Copy{{Name: "result", Snapshot: result.Merged}})
		for uuid, holdings := range heldEntries(s.Copies) {
			if len(own[uuid]) == 0 {
				continue
			}
			settled := idsOf(own[uuid][0].versions[1:])
			for i, a := range holdings {
				for _, b := range holdings[i+1:] {
					first, second := identify(a.main), identify(b.main)
					if first != second && !idsOf(a.versions[1:])[second] && !idsOf(b.versions[1:])[first] && settled[first] && settled[second] {
						reached++
					}
				}
			}
		}
	}
	t.Logf("%d conflicts between two copies that the result settled", reached)
	if reached == 0 {
		t.Error("no scenario has two old copies in conflict that the result holds behind its main version")
	}
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
	main *snapshot.EntryVersion
	// versions holds the main version first, and the history after it.
	versions []*snapshot.EntryVersion
	group    snapshot.UUID
	atRoot   bool
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
				h := held{main: &e.EntryVersion, group: g.UUID, atRoot: g == &c.Snapshot.Root}
				if since, inBin := binned[g.UUID]; inBin {
					h.inBin, h.recycled = true, since
					if e.Times.LocationChanged.After(since) {
						h.recycled = e.Times.LocationChanged
					}
				}
				h.versions = append(h.versions, &e.EntryVersion)
				for j := range e.History {
					h.versions = append(h.versions, &e.History[j])
				}
				result[e.UUID] = append(result[e.UUID], h)
			}
		})
	}
	return result
}

// identifyContent is a version without its time.
func identifyContent(v *snapshot.EntryVersion) versionID {
	content := *v
	content.Times.LastModification = time.Time{}
	return identify(&content)
}

func customDataItems(v *snapshot.EntryVersion) map[snapshot.CustomDataItem]bool {
	items := map[snapshot.CustomDataItem]bool{}
	for _, item := range v.CustomData {
		if !item.IsService() {
			item.LastModification = time.Time{}
			items[item] = true
		}
	}
	return items
}

// goesInto says, from the words of the ticket alone, that a version is absorbed by another one:
// the same time, the same content, and custom data that is a strict subset of the other.
func goesInto(smaller, larger *snapshot.EntryVersion) bool {
	withoutData := func(v *snapshot.EntryVersion) versionID {
		bare := *v
		bare.CustomData = nil
		return identify(&bare)
	}
	less, more := customDataItems(smaller), customDataItems(larger)
	if len(less) >= len(more) || withoutData(smaller) != withoutData(larger) {
		return false
	}
	for item := range less {
		if !more[item] {
			return false
		}
	}
	return true
}

// standing gives the versions that nothing absorbs.
func standing(versions []*snapshot.EntryVersion) map[versionID]*snapshot.EntryVersion {
	result := map[versionID]*snapshot.EntryVersion{}
	for _, v := range versions {
		if !slices.ContainsFunc(versions, func(other *snapshot.EntryVersion) bool { return goesInto(v, other) }) {
			result[identify(v)] = v
		}
	}
	return result
}

func versionsOfHoldings(holdings []held) []*snapshot.EntryVersion {
	var versions []*snapshot.EntryVersion
	for _, h := range holdings {
		versions = append(versions, h.versions...)
	}
	return versions
}

// standIDs gives the IDs of the versions that stand for a version: the version, or those that
// absorb it.
func standIDs(v *snapshot.EntryVersion, standing map[versionID]*snapshot.EntryVersion) []versionID {
	if standing[identify(v)] != nil {
		return []versionID{identify(v)}
	}
	var ids []versionID
	for id, other := range standing {
		if goesInto(v, other) {
			ids = append(ids, id)
		}
	}
	return ids
}

// copyOrder is what one copy says about the order of the standing versions. An older version of
// the copy that shares a standing version with its main version counts only as main.
type copyOrder struct {
	mains, behind map[versionID]bool
}

func ordersOf(holdings []held, tops map[versionID]*snapshot.EntryVersion) []copyOrder {
	orders := make([]copyOrder, len(holdings))
	for i, h := range holdings {
		order := copyOrder{mains: map[versionID]bool{}, behind: map[versionID]bool{}}
		for _, id := range standIDs(h.main, tops) {
			order.mains[id] = true
		}
		for _, old := range h.versions[1:] {
			ids := standIDs(old, tops)
			if slices.ContainsFunc(ids, func(id versionID) bool { return order.mains[id] }) {
				continue
			}
			for _, id := range ids {
				order.behind[id] = true
			}
		}
		orders[i] = order
	}
	return orders
}

// mustAsk says, from the words of the spec and the ticket alone, whether the entry raises a
// question: two main versions differ in content, no copy holds one of them behind the other or
// both behind a third one, and they are less than 24 hours apart or one of them comes from the
// future. A main version with
// less custom data stands for the versions that absorb it, and says nothing against any of them.
func mustAsk(holdings []held, now time.Time) bool {
	tops := standing(versionsOfHoldings(holdings))
	orders := ordersOf(holdings, tops)
	replaced := func(old, by versionID) bool {
		return slices.ContainsFunc(orders, func(o copyOrder) bool { return o.mains[by] && o.behind[old] })
	}
	mains := map[versionID]bool{}
	for _, o := range orders {
		maps.Copy(mains, o.mains)
	}
	bothReplaced := func(first, second versionID) bool {
		return slices.ContainsFunc(orders, func(o copyOrder) bool { return o.behind[first] && o.behind[second] })
	}
	without := func(with, other versionID) bool {
		return slices.ContainsFunc(orders, func(o copyOrder) bool { return o.mains[with] && !o.mains[other] })
	}
	ids := slices.Collect(maps.Keys(mains))
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			if identifyContent(tops[a]) == identifyContent(tops[b]) || !without(a, b) || !without(b, a) || replaced(a, b) || replaced(b, a) || bothReplaced(a, b) {
				continue
			}
			first, second := tops[a].Times.LastModification, tops[b].Times.LastModification
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

// mainIsReplaced says, from the words of the rule alone, that the chosen main version sits in
// the history of a copy whose own main version has other content.
func mainIsReplaced(holdings []held, chosen versionID) bool {
	tops := standing(versionsOfHoldings(holdings))
	for _, o := range ordersOf(holdings, tops) {
		if !o.behind[chosen] {
			continue
		}
		for id := range o.mains {
			if identifyContent(tops[id]) != identifyContent(tops[chosen]) {
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

// TestPropertyQuestionsDoNotDisappear: each conflict within 24 hours, each stamp from the future
// in a conflict and each main version that a copy has replaced gives a question. Anything else,
// a copy that lags with a clock that agrees first of all, gives none.
func TestPropertyQuestionsDoNotDisappear(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")

		result := runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)})

		asked := conflictQuestions(result.Questions)
		merged := allEntries(result.Merged)
		for uuid, holdings := range heldEntries(s.Copies) {
			conflict, got := asked[uuid]
			replaced := mainIsReplaced(holdings, identify(&merged[uuid].EntryVersion))
			if want := mustAsk(holdings, s.Now) || replaced; got != want {
				t.Errorf("entry %s: question = %v, want %v", uuid, got, want)
			}
			if got && conflict.Replaced != replaced {
				t.Errorf("entry %s: the question says replaced = %v, want %v", uuid, conflict.Replaced, replaced)
			}
		}
	})
}

// TestPropertyAMainVersionThatMovesWithTheKeyAsks: another run has another key. Whenever that
// moves the main version of an earlier result, the run asks, so no main version moves silently.
func TestPropertyAMainVersionThatMovesWithTheKeyAsks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		key := drawKey(t)
		settled := runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: key}).Merged
		before := allEntries(settled)
		own := merge.Copy{Name: "result", Snapshot: settled}
		other := rapid.SampledFrom(append(slices.Clone(s.Copies), own)).Draw(t, "other copy")
		key[0] = 2

		again := runOn(t, merge.Input{Copies: []merge.Copy{own, other}, Now: s.Now, Key: key})

		asked := conflictQuestions(again.Questions)
		for uuid, e := range allEntries(again.Merged) {
			if _, got := asked[uuid]; !got && identify(&e.EntryVersion) != identify(&before[uuid].EntryVersion) {
				t.Errorf("entry %s: the main version moved without a question", uuid)
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
// each once, apart from those another version absorbs, and the newest by the clock is main.
func TestPropertyTheSetOfVersionsIsTheUnion(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")

		merged := allEntries(runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}).Merged)

		for uuid, holdings := range heldEntries(s.Copies) {
			want := map[versionID]bool{}
			for id := range standing(versionsOfHoldings(holdings)) {
				want[id] = true
			}
			newest := time.Time{}
			for _, h := range holdings {
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
				wantGroupFromNewestMove(t, deciding, result, uuid, located)
			}
		}
	})
}

// placeInResult gives the UUID a group of a copy has in the result.
func placeInResult(group snapshot.UUID, result *merge.Result) snapshot.UUID {
	if head, joined := result.MergedGroups[group]; joined {
		return head
	}
	return group
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

func wantGroupFromNewestMove(t *rapid.T, holdings []held, result *merge.Result, uuid snapshot.UUID, located time.Time) {
	t.Helper()
	merged := result.Merged
	group := groupsWithEntry(merged, uuid)[0].UUID
	for _, h := range holdings {
		// All root groups are one group in the result, whatever their UUIDs, and so are the
		// groups that joined.
		if placeInResult(h.group, result) != group && !(h.atRoot && group == merged.Root.UUID) {
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

// TestPropertyAnAnswerHolds: the content the human chose is main and each original version stays.
// A merge with any old copy keeps it that way, and an answer other than the first ends the question.
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
		result := runOn(t, in).Merged
		merged := allEntries(result)
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
			for id := range standing(versionsOfHoldings(holdings[conflict.Entry])) {
				if !got[id] {
					t.Errorf("entry %s lost an original version", conflict.Entry)
				}
			}
			if d.Choice == 0 {
				continue
			}
			for _, old := range s.Copies {
				again := runOn(t, merge.Input{Copies: []merge.Copy{{Name: "result", Snapshot: result}, old}, Now: s.Now, Key: in.Key})
				wantSameSnapshot(t, again.Merged, result)
				if _, asked := conflictQuestions(again.Questions)[conflict.Entry]; asked {
					t.Errorf("entry %s: the merge with %s asks again after the answer", conflict.Entry, old.Name)
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

		text := printed(result.Questions) + printed(result.Report)
		for _, marker := range []string{mergetest.SecretMarker, mergetest.CustomDataMarker} {
			if strings.Contains(text, marker) {
				t.Errorf("a value with the marker %s reached the questions or the report:\n%s", marker, text)
			}
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

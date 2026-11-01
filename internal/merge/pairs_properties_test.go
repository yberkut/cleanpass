package merge_test

import (
	"maps"
	"net/url"
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

func isAboutPairs(q merge.Question) bool {
	switch q.Detail.(type) {
	case merge.SimilarPair, merge.IdenticalPairs:
		return true
	}
	return false
}

func withoutPairs(questions []merge.Question) []merge.Question {
	var others []merge.Question
	for _, q := range questions {
		if !isAboutPairs(q) {
			others = append(others, q)
		}
	}
	return others
}

func onlyPairs(questions []merge.Question) []merge.Question {
	var pairs []merge.Question
	for _, q := range questions {
		if isAboutPairs(q) {
			pairs = append(pairs, q)
		}
	}
	return pairs
}

// visibleField gives the value of a field that is no secret. The rules here come from the words
// of the spec and are written apart from the engine.
func visibleField(v *snapshot.EntryVersion, key string) string {
	for _, f := range v.Fields {
		if f.Key == key && !f.Protected {
			return strings.ToLower(strings.TrimSpace(f.Value))
		}
	}
	return ""
}

func visibleHost(v *snapshot.EntryVersion) string {
	value := visibleField(v, "URL")
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// mustBeSimilar says, from the words of the spec, that two main versions make a similar pair.
func mustBeSimilar(a, b *snapshot.EntryVersion) bool {
	user := visibleField(a, "UserName")
	if user != visibleField(b, "UserName") {
		return false
	}
	title := visibleField(a, "Title")
	host := visibleHost(a)
	return (title != "" && title == visibleField(b, "Title")) || (user != "" && host != "" && host == visibleHost(b))
}

// deletedInACopy gives the entries with a record of deletion or a place in a recycle bin.
func deletedInACopy(copies []merge.Copy) map[snapshot.UUID]bool {
	deleted := map[snapshot.UUID]bool{}
	for _, c := range copies {
		for _, d := range c.Snapshot.Deletions {
			deleted[d.UUID] = true
		}
		c.Snapshot.Root.Walk(func(g *snapshot.Group) {
			if !g.UUID.IsZero() && g.UUID == c.Snapshot.Meta.RecycleBin {
				g.Walk(func(inBin *snapshot.Group) {
					for i := range inBin.Entries {
						deleted[inBin.Entries[i].UUID] = true
					}
				})
			}
		})
	}
	return deleted
}

// TestPropertyEverySimilarPairAsks: a question comes for each similar pair and for nothing else,
// and it stays apart without an answer.
func TestPropertyEverySimilarPairAsks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")

		result := runOn(t, merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)})

		if len(result.Absorbed) != 0 {
			t.Errorf("entries were absorbed without an answer: %v", result.Absorbed)
		}
		asked := pairQuestions(result)
		merged := allEntries(result.Merged)
		deleted := deletedInACopy(s.Copies)
		for first, a := range merged {
			for second, b := range merged {
				if !isSmaller(first, second) {
					continue
				}
				_, got := asked[[2]snapshot.UUID{first, second}]
				want := mustBeSimilar(&a.EntryVersion, &b.EntryVersion) && !deleted[first] && !deleted[second]
				if got != want {
					t.Errorf("entries %s and %s: question = %v, want %v:\n%s\n%s", first, second, got, want, printed(a.Fields), printed(b.Fields))
				}
			}
		}
	})
}

func isSmaller(a, b snapshot.UUID) bool { return string(a[:]) < string(b[:]) }

// TestPropertyNoAnswerChangesTheQuestionsAboutPairs: the questions about pairs come from the
// copies alone, so no answer can take away or change a question about a pair.
func TestPropertyNoAnswerChangesTheQuestionsAboutPairs(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		before := runOn(t, in)

		in.Decisions = drawDecisions(t, before.Questions)
		after := runOn(t, in)

		if !reflect.DeepEqual(onlyPairs(after.Questions), onlyPairs(before.Questions)) {
			t.Errorf("the questions about pairs changed:\n got %s\nwant %s", printed(onlyPairs(after.Questions)), printed(onlyPairs(before.Questions)))
		}
	})
}

// TestPropertyAMergedPairHoldsExactlyItsVersions: each entry holds the versions of itself and of
// the entries it took in, each once and nothing else, the newest by the clock on top. Each absorbed
// entry is gone from the tree, and a merge of the result with itself does not bring it back.
func TestPropertyAMergedPairHoldsExactlyItsVersions(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, onlyPairs(runOn(t, in).Questions))

		result := runOn(t, in)

		into := map[snapshot.UUID]snapshot.UUID{}
		for _, a := range result.Absorbed {
			if !isSmaller(a.Into, a.Absorbed) {
				t.Errorf("entry %s went into %s, whose UUID is larger", a.Absorbed, a.Into)
			}
			into[a.Absorbed] = a.Into
		}
		want := map[snapshot.UUID]map[versionID]bool{}
		newest := map[snapshot.UUID]time.Time{}
		for uuid, holdings := range heldEntries(s.Copies) {
			final, wasAbsorbed := into[uuid]
			if !wasAbsorbed {
				final = uuid
			}
			if want[final] == nil {
				want[final] = map[versionID]bool{}
			}
			for _, h := range holdings {
				maps.Copy(want[final], idsOf(h.versions))
				for _, v := range h.versions {
					if v.Times.LastModification.After(newest[final]) {
						newest[final] = v.Times.LastModification
					}
				}
			}
		}
		merged := allEntries(result.Merged)
		if len(merged) != len(want) {
			t.Errorf("the result has %d entries, want %d", len(merged), len(want))
		}
		for uuid, versions := range want {
			e := merged[uuid]
			if e == nil {
				t.Fatalf("entry %s is gone", uuid)
			}
			all := versionsOfEntry(e)
			if got := idsOf(all); !maps.Equal(got, versions) || len(all) != len(got) {
				t.Errorf("entry %s has %d versions, %d different ones, want %d", uuid, len(all), len(got), len(versions))
			}
			if !e.Times.LastModification.Equal(newest[uuid]) {
				t.Errorf("entry %s: main version has time %s, newest is %s", uuid, e.Times.LastModification, newest[uuid])
			}
		}

		again := runOn(t, merge.Input{Copies: []merge.Copy{{Name: "result", Snapshot: result.Merged}}, Now: s.Now, Key: in.Key})
		wantSameSnapshot(t, again.Merged, result.Merged)
	})
}

// TestPropertyNoLossWithPairs is the property "no losses" of the spec, proved by the Loss Audit:
// with any answers about conflicts, pairs and deletions, nothing of the input is gone. The audit
// lets the versions of an absorbed entry move only because the result names that entry, and an
// entry go only because a decision names it.
func TestPropertyNoLossWithPairs(t *testing.T) {
	merges := 0
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)

		result := runOn(t, in)

		copies := make([]*snapshot.Snapshot, len(s.Copies))
		for i, c := range s.Copies {
			copies[i] = c.Snapshot
		}
		allowed := lossaudit.Allowed{
			Absorbed:     map[snapshot.UUID]snapshot.UUID{},
			Deleted:      slices.Collect(maps.Keys(allowedToGo(in.Decisions))),
			MergedGroups: result.MergedGroups,
		}
		for _, a := range result.Absorbed {
			allowed.Absorbed[a.Absorbed] = a.Into
		}
		losses := lossaudit.Audit(lossaudit.Input{Copies: copies, Result: result.Merged, Allowed: allowed})
		if len(losses) != 0 {
			t.Errorf("the audit found losses: %+v", losses)
		}
		if len(result.Absorbed) > 0 {
			merges++
			if len(lossaudit.Audit(lossaudit.Input{Copies: copies, Result: result.Merged})) == 0 {
				t.Errorf("the entries %v left the tree, and the audit saw no change", result.Absorbed)
			}
		}
	})
	if merges == 0 {
		t.Error("no scenario merged a pair, so the property says nothing about pairs")
	}
}

// contentOfVersion is a version as a person sees its content: no times, and no history.
func contentOfVersion(v *snapshot.EntryVersion) versionID {
	content := *v
	content.Times.LastModification, content.Times.Creation = time.Time{}, time.Time{}
	return identify(&content)
}

// TestPropertyTheBulkAnswerMergesAllPairsWithTheSameContentAndNoOthers: the one answer makes one
// entry of two entries exactly if their content is the same.
func TestPropertyTheBulkAnswerMergesAllPairsWithTheSameContentAndNoOthers(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		first := runOn(t, in)
		pairs := pairQuestions(first)
		for _, q := range first.Questions {
			if _, ok := q.Detail.(merge.IdenticalPairs); ok {
				in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}
			}
		}

		result := runOn(t, in)

		head := map[snapshot.UUID]snapshot.UUID{}
		for _, a := range result.Absorbed {
			head[a.Absorbed] = a.Into
		}
		familyOf := func(id snapshot.UUID) snapshot.UUID {
			if into, ok := head[id]; ok {
				return into
			}
			return id
		}
		before := allEntries(first.Merged)
		for ids := range pairs {
			same := contentOfVersion(&before[ids[0]].EntryVersion) == contentOfVersion(&before[ids[1]].EntryVersion)
			if same && familyOf(ids[0]) != familyOf(ids[1]) {
				t.Errorf("the pair %v has the same content and stays apart", ids)
			}
		}
		for _, a := range result.Absorbed {
			if contentOfVersion(&before[a.Absorbed].EntryVersion) != contentOfVersion(&before[a.Into].EntryVersion) {
				t.Errorf("entry %s went into %s with another content", a.Absorbed, a.Into)
			}
		}
	})
}

// seePairCases counts the cases of the rule of pairs for TestScenariosReachEveryCase.
func seePairCases(seen map[string]int, copies []merge.Copy, result *merge.Result) {
	if copies[0].Snapshot.Root.UUID != copies[1].Snapshot.Root.UUID {
		seen["copies with different root groups"]++
	}
	conflicts := conflictQuestions(result.Questions)
	inPairs := map[snapshot.UUID]int{}
	for _, q := range result.Questions {
		if bulk, ok := q.Detail.(merge.IdenticalPairs); ok && len(bulk.Pairs) > 1 {
			seen["question about several pairs"]++
		}
		pair, ok := q.Detail.(merge.SimilarPair)
		if !ok {
			continue
		}
		inPairs[pair.First.Entry]++
		inPairs[pair.Second.Entry]++
		for name, happens := range map[string]bool{
			"pair by title and user name":                     pair.SameTitleAndUser && !pair.SameUserAndHost,
			"pair by user name and host only":                 pair.SameUserAndHost && !pair.SameTitleAndUser,
			"pair by both":                                    pair.SameUserAndHost && pair.SameTitleAndUser,
			"pair with the same password":                     pair.SamePassword,
			"pair with different passwords":                   !pair.SamePassword,
			"pair with the same content":                      pair.SameContent,
			"pair with the same password and another content": pair.SamePassword && !pair.SameContent,
			"pair in another spelling":                        pair.SameTitleAndUser && pair.First.Label != pair.Second.Label && pair.First.Label.URL == pair.Second.Label.URL,
			"pair in one copy":                                len(pair.First.Copies) == 1 && reflect.DeepEqual(pair.First.Copies, pair.Second.Copies),
			"pair over two copies":                            !slices.ContainsFunc(pair.First.Copies, func(name string) bool { return slices.Contains(pair.Second.Copies, name) }),
		} {
			if happens {
				seen[name]++
			}
		}
		_, firstInConflict := conflicts[pair.First.Entry]
		_, secondInConflict := conflicts[pair.Second.Entry]
		if firstInConflict || secondInConflict {
			seen["pair with a conflict about an entry"]++
		}
	}
	for _, pairs := range inPairs {
		if pairs > 1 {
			seen["entry in two pairs"]++
		}
	}
}

// TestPropertyNoSecretAfterAnswers: the answers add events to the report, and those hold no
// secret either.
func TestPropertyNoSecretAfterAnswers(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)

		result := runOn(t, in)

		if text := printed(result.Questions) + printed(result.Report) + printed(result.Absorbed); strings.Contains(text, mergetest.SecretMarker) {
			t.Errorf("a secret reached the questions or the report:\n%s", text)
		}
	})
}

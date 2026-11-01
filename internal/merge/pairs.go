package merge

import (
	"cmp"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

func init() { addRule(phasePairs, askAboutPairs) }

// SimilarPair is the Detail of a question about two entries that look like one account under
// two UUIDs. Answer 0 keeps both, which is also what happens without an answer, and answer 1
// merges them.
type SimilarPair struct {
	// First and Second are the entries, the smaller UUID first. A merge keeps that UUID.
	First, Second PairSide
	// SameTitleAndUser and SameUserAndHost tell what the entries have in common.
	SameTitleAndUser, SameUserAndHost bool
	// SamePassword says the passwords are the same. This is all a question holds about a
	// password: not the value and nothing made from the value.
	SamePassword bool
	// SameContent says the main versions differ in nothing a person would call content. The
	// question of IdenticalPairs covers such a pair.
	SameContent bool
	// Differences names what differs between the main versions.
	Differences Difference
}

// IdenticalPairs is the Detail of the one question about all pairs with the same content, which
// saves the human from hundreds of questions after an import. It exists only if there is such a
// pair. Answer 0 changes nothing, which is also what happens without an answer, and answer 1
// merges them all, except a pair that has an answer of its own.
type IdenticalPairs struct {
	// Pairs holds the IDs of the questions about these pairs, sorted.
	Pairs []QuestionID
}

// identicalPairsQuestion is the ID of the question about all pairs with the same content.
const identicalPairsQuestion QuestionID = "pairs:identical"

// PairSide is what the human sees of one entry of a pair.
type PairSide struct {
	Entry snapshot.UUID
	Label Label
	// Modified is the time of the main version.
	Modified time.Time
	// Copies names the copies that hold the entry, sorted.
	Copies []string
}

// Absorption says a merged pair took one entry into another.
type Absorption struct {
	Absorbed, Into snapshot.UUID
}

// PairMerged is the event about an entry that took other entries in.
type PairMerged struct {
	Entry snapshot.UUID
	Label Label
	// Absorbed holds the UUIDs of the other entries, sorted.
	Absorbed []snapshot.UUID
}

func (p PairMerged) sortKey() string { return "pair merged:" + p.Entry.String() }

// PairNotMerged is the event about a pair the human wanted merged. The merge did not happen,
// because it would have made one entry of two others the human wanted kept apart.
type PairNotMerged struct {
	First, Second PairSide
}

func (p PairNotMerged) sortKey() string {
	return "pair not merged:" + p.First.Entry.String() + ":" + p.Second.Entry.String()
}

func askAboutPairs(m *merger) {
	pairs := m.findPairs()
	var identical []QuestionID
	for _, pair := range pairs {
		if m.memory.different[orderedPair(pair.first.uuid, pair.second.uuid)] {
			pair.answer, pair.answered = pairAnswerKeepBoth, true
			continue
		}
		detail := pair.detail()
		id := QuestionID("pair:" + pair.first.uuid.String() + ":" + pair.second.uuid.String())
		pair.answer, pair.answered = m.ask(Question{ID: id, Choices: 2, Detail: detail})
		pair.sameContent = detail.SameContent
		if detail.SameContent {
			identical = append(identical, id)
		}
		if pair.answered && pair.answer == pairAnswerKeepBoth {
			m.remember(memoryDifferent, pair.first.uuid.String(), pair.second.uuid.String())
		}
	}
	mergeIdentical := false
	if len(identical) > 0 {
		choice, _ := m.ask(Question{ID: identicalPairsQuestion, Choices: 2, Detail: IdenticalPairs{Pairs: identical}})
		mergeIdentical = choice == pairAnswerMerge
	}
	m.mergePairs(pairs, mergeIdentical)
}

// mergePairs applies the answers in the order of the pairs, so the order of the copies does not
// count. An entry can be in several pairs, so the merges make families, and the smallest UUID of
// a family is the one that stays.
func (m *merger) mergePairs(pairs []*similarPair, mergeIdentical bool) {
	keptApart := slices.Collect(maps.Keys(m.memory.different))
	for _, pair := range pairs {
		if pair.answered && pair.answer == pairAnswerKeepBoth {
			keptApart = append(keptApart, orderedPair(pair.first.uuid, pair.second.uuid))
		}
	}
	family := m.sameEntries
	for _, pair := range pairs {
		switch {
		case !pair.wantsMerge(mergeIdentical):
		case family.wouldJoinAny(pair, keptApart):
			m.tell(PairNotMerged{First: pair.first.pairSide(), Second: pair.second.pairSide()})
		default:
			family.join(pair.first.uuid, pair.second.uuid)
		}
	}
	absorbed := map[snapshot.UUID][]snapshot.UUID{}
	for _, e := range m.sortedEntries() {
		if head := family.head(e.uuid); head != e.uuid {
			m.absorb(m.entries[head], e)
			absorbed[head] = append(absorbed[head], e.uuid)
		}
	}
	for head, members := range absorbed {
		m.tell(PairMerged{Entry: head, Label: labelOf(&m.entries[head].main().data), Absorbed: members})
	}
}

// The answers to a question about a pair.
const (
	pairAnswerKeepBoth = iota
	pairAnswerMerge
)

// families tells which entries are one by now: it holds the way from an entry to the head of
// its family. The head is the smallest UUID of the family.
type families map[snapshot.UUID]snapshot.UUID

func (f families) head(id snapshot.UUID) snapshot.UUID {
	for {
		next, ok := f[id]
		if !ok {
			return id
		}
		id = next
	}
}

func (f families) join(a, b snapshot.UUID) {
	a, b = f.head(a), f.head(b)
	if compareUUIDs(a, b) > 0 {
		a, b = b, a
	}
	if a != b {
		f[b] = a
	}
}

// wouldJoinAny says merging the pair would make one family of the two entries of one of the others.
func (f families) wouldJoinAny(pair *similarPair, others [][2]snapshot.UUID) bool {
	a, b := f.head(pair.first.uuid), f.head(pair.second.uuid)
	return slices.ContainsFunc(others, func(other [2]snapshot.UUID) bool {
		x, y := f.head(other[0]), f.head(other[1])
		return (x == a && y == b) || (x == b && y == a)
	})
}

// absorb moves all versions of an entry under another entry, which lies where the newest move
// of the two put it. The entry itself is gone after this, but no version of it is.
func (m *merger) absorb(into, absorbed *entryState) {
	for _, v := range absorbed.versions {
		into.put(v.key, v.data)
	}
	if cmp.Or(absorbed.located.Compare(into.located), compareUUIDs(absorbed.group, into.group)) > 0 {
		into.located, into.group = absorbed.located, absorbed.group
	}
	delete(m.entries, absorbed.uuid)
}

// similarPair is two entries with something in common, the smaller UUID first.
type similarPair struct {
	first, second              *entryState
	sameTitleAndUser, sameHost bool
	sameContent                bool
	// answer is the answer of the human, if answered.
	answer   int
	answered bool
}

// wantsMerge says what the human wants for this pair. An answer about this pair beats the
// answer about all pairs with the same content.
func (p *similarPair) wantsMerge(mergeIdentical bool) bool {
	if p.answered {
		return p.answer == pairAnswerMerge
	}
	return mergeIdentical && p.sameContent
}

func (p *similarPair) detail() SimilarPair {
	first, second := &p.first.ruleMain.data, &p.second.ruleMain.data
	difference := differences(first, second)
	return SimilarPair{
		First:            p.first.pairSide(),
		Second:           p.second.pairSide(),
		SameTitleAndUser: p.sameTitleAndUser,
		SameUserAndHost:  p.sameHost,
		SamePassword:     passwordOf(first) == passwordOf(second),
		SameContent:      isOnlyCreation(difference),
		Differences:      difference,
	}
}

// isOnlyCreation says two versions differ at most in the time of creation. That time is not
// something a person sets: an import writes a new one, so with it no pair after an import would
// have the same content. A merge keeps both versions anyway.
func isOnlyCreation(d Difference) bool {
	return len(d.Fields) == 0 && !slices.ContainsFunc(d.Attributes, func(a Attribute) bool { return a != AttributeCreation })
}

func (e *entryState) pairSide() PairSide {
	names := make([]string, len(e.holdings))
	for i := range e.holdings {
		names[i] = e.holdings[i].copy.Name
	}
	slices.Sort(names)
	return PairSide{
		Entry:    e.uuid,
		Label:    labelOf(&e.ruleMain.data),
		Modified: e.ruleMain.key.time(),
		Copies:   slices.Compact(names),
	}
}

func passwordOf(v *snapshot.EntryVersion) string {
	for i := range v.Fields {
		if v.Fields[i].Key == "Password" {
			return v.Fields[i].Value
		}
	}
	return ""
}

// findPairs gives the similar pairs in a fixed order. It sees an entry only through the Label of
// its main version by the rule:
//   - the Label holds no protected value, so no secret can make a pair or break one;
//   - no answer changes that version, so no answer changes the questions about pairs.
//
// An entry that is deleted in any copy waits: merging it could put a living account into the
// recycle bin or contradict the answer about the deletion. A later merge sees the pair, once the
// memory says the human keeps the entry.
func (m *merger) findPairs() []*similarPair {
	deleted := m.deletedSomewhere()
	withSameValue := map[similarity][]*entryState{}
	for _, e := range m.sortedEntries() {
		if deleted[e.uuid] {
			continue
		}
		for _, s := range similaritiesOf(labelOf(&e.ruleMain.data)) {
			withSameValue[s] = append(withSameValue[s], e)
		}
	}
	found := map[[2]snapshot.UUID]*similarPair{}
	for s, family := range withSameValue {
		for i, a := range family {
			for _, b := range family[i+1:] {
				id := [2]snapshot.UUID{a.uuid, b.uuid}
				if found[id] == nil {
					found[id] = &similarPair{first: a, second: b}
				}
				if s.byHost {
					found[id].sameHost = true
				} else {
					found[id].sameTitleAndUser = true
				}
			}
		}
	}
	return slices.SortedFunc(maps.Values(found), func(a, b *similarPair) int {
		return cmp.Or(compareUUIDs(a.first.uuid, b.first.uuid), compareUUIDs(a.second.uuid, b.second.uuid))
	})
}

// deletedSomewhere gives the entries with a record of deletion or a place in the recycle bin in
// any copy.
func (m *merger) deletedSomewhere() map[snapshot.UUID]bool {
	deleted := map[snapshot.UUID]bool{}
	for i := range m.in.Copies {
		s := m.in.Copies[i].Snapshot
		for _, d := range s.Deletions {
			deleted[d.UUID] = true
		}
		s.Root.Walk(func(g *snapshot.Group) {
			if s.Meta.RecycleBin.IsZero() || g.UUID != s.Meta.RecycleBin {
				return
			}
			g.Walk(func(inBin *snapshot.Group) {
				for j := range inBin.Entries {
					deleted[inBin.Entries[j].UUID] = true
				}
			})
		})
	}
	for id := range deleted {
		if e := m.entries[id]; e != nil {
			if answer, remembered := m.memory.deletionAnswer(e, m.deletion(e, m.records[id])); remembered && answer == AnswerKeep {
				delete(deleted, id)
			}
		}
	}
	return deleted
}

// similarity is what two similar entries have in common: a user name, and a host if byHost,
// otherwise a title. All values are normalized.
type similarity struct {
	byHost      bool
	titleOrHost string
	userName    string
}

// similaritiesOf gives what an entry can have in common with another one. An empty value says
// nothing about the account, so only a title may stand next to an empty user name: without
// this, all notes with no user name and no URL would be one big family of pairs.
func similaritiesOf(label Label) []similarity {
	title, user := normalized(label.Title), normalized(label.UserName)
	var result []similarity
	if title != "" {
		result = append(result, similarity{titleOrHost: title, userName: user})
	}
	if host := hostOf(label.URL); user != "" && host != "" {
		result = append(result, similarity{byHost: true, titleOrHost: host, userName: user})
	}
	return result
}

func normalized(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

// hostOf gives the host of a URL, or nothing if the value is no URL. People often write a URL
// with no scheme, so a value with no scheme counts as a host and a path.
func hostOf(value string) string {
	value = strings.TrimSpace(value)
	if value != "" && !strings.Contains(value, "://") {
		value = "//" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

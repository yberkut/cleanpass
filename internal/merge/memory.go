package merge

import (
	"encoding/hex"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The memory of decisions lives in the custom data of the database, because KeePassXC keeps it
// through a save, so the next merge with an old copy does not ask the same thing again.
//
// Each decision is one item, and a key is never given another value. So the memory of several
// copies joins as a set, as all custom data does, and nothing makes the Loss Audit miss a value.
// The key names the decision, with UUIDs and the dates it rests on. The value is the date of the
// decision. Nothing else goes in: no name and no secret.
//
//	cleanpass/entries/<a>/<b>    entries a and b are one entry, under the smaller UUID
//	cleanpass/groups/<a>/<b>     groups a and b are one group, under the smaller UUID
//	cleanpass/different/<a>/<b>  the human keeps these two similar entries apart
//	cleanpass/rejected/<e>/<d>   the human kept entry e against a deletion dated up to d
//	cleanpass/accepted/<e>/<v>   the human deleted entry e and saw a version with the time v;
//	                             one key for each time of a version
//	cleanpass/replaced/<e>/<m>/<r> the human let the main version with date m of entry e stay
//	                             on top of the versions up to date r that replaced it
//
// A later date or another change is new to the human, so the question comes again. For a deleted
// entry a date is not enough: a clock that runs behind writes an old time on a new edit, and it
// would go with the entry unseen. So any time the human did not see asks. The Loss Audit reads
// the keys with accepted by itself, so their form is a contract with it.
const memoryPrefix = "cleanpass/"

const (
	memoryEntries   = "entries"
	memoryGroups    = "groups"
	memoryDifferent = "different"
	memoryRejected  = "rejected"
	memoryAccepted  = "accepted"
	memoryReplaced  = "replaced"
)

// memory is what the copies remember, with every entry under the UUID it has by now.
type memory struct {
	entries, groups families
	different       map[[2]snapshot.UUID]bool
	rejected        map[snapshot.UUID][]rejection
	accepted        map[snapshot.UUID]*acceptance
	replaced        map[snapshot.UUID][]rememberedOrder
}

// rejection is an answer that kept an entry against the deletions dated up to through.
type rejection struct {
	through, decided time.Time
}

// acceptance is what the answers that deleted an entry knew.
type acceptance struct {
	// seen holds the times of the versions the human saw, in the form of memoryDate.
	seen map[string]bool
	// decided is the date of the newest of these answers.
	decided time.Time
}

type rememberedOrder struct {
	main, replacedBy time.Time
}

// readMemory reads the decisions of earlier merges from the copies. An item it cannot read is
// plain custom data to it: it has no effect and it stays.
func readMemory(m *merger) {
	mem := memory{
		entries:   families{},
		groups:    families{},
		different: map[[2]snapshot.UUID]bool{},
		rejected:  map[snapshot.UUID][]rejection{},
		accepted:  map[snapshot.UUID]*acceptance{},
		replaced:  map[snapshot.UUID][]rememberedOrder{},
	}
	metas := make([]*snapshot.Meta, len(m.in.Copies))
	for i := range m.in.Copies {
		metas[i] = &m.in.Copies[i].Snapshot.Meta
	}
	items := joinCustomData(metas)
	// Families first, because the other decisions name an entry by the UUID it has by now.
	for i := range items {
		if kind, parts, ok := parseMemoryKey(items[i].Key); ok && len(parts) == 2 && (kind == memoryEntries || kind == memoryGroups) {
			a, b, ok := twoUUIDs(parts)
			if !ok {
				continue
			}
			if kind == memoryEntries {
				mem.entries.join(a, b)
			} else {
				mem.groups.join(a, b)
			}
		}
	}
	for i := range items {
		if kind, parts, ok := parseMemoryKey(items[i].Key); ok {
			mem.read(kind, parts, items[i].Value)
		}
	}
	m.memory = mem
	m.sameEntries = maps.Clone(mem.entries)
	m.sameGroups = maps.Clone(mem.groups)
	m.sameUnits = maps.Clone(mem.groups)
}

func (mem *memory) read(kind string, parts []string, value string) {
	switch kind {
	case memoryDifferent:
		if a, b, ok := twoUUIDs(parts); ok {
			if a, b = mem.entries.head(a), mem.entries.head(b); a != b {
				mem.different[orderedPair(a, b)] = true
			}
		}
	case memoryRejected:
		if id, through, ok := uuidAndDate(parts); ok {
			decided, _ := time.Parse(time.RFC3339Nano, value)
			id = mem.entries.head(id)
			mem.rejected[id] = append(mem.rejected[id], rejection{through: through, decided: decided})
		}
	case memoryAccepted:
		if id, seen, ok := uuidAndDate(parts); ok {
			decided, _ := time.Parse(time.RFC3339Nano, value)
			id = mem.entries.head(id)
			if mem.accepted[id] == nil {
				mem.accepted[id] = &acceptance{seen: map[string]bool{}}
			}
			mem.accepted[id].seen[memoryDate(seen)] = true
			mem.accepted[id].decided = later(mem.accepted[id].decided, decided)
		}
	case memoryReplaced:
		if len(parts) != 3 {
			return
		}
		id, main, ok := uuidAndDate(parts[:2])
		replacedBy, err := time.Parse(time.RFC3339Nano, parts[2])
		if ok && err == nil {
			id = mem.entries.head(id)
			mem.replaced[id] = append(mem.replaced[id], rememberedOrder{main: main, replacedBy: replacedBy})
		}
	}
}

func parseMemoryKey(key string) (kind string, parts []string, ok bool) {
	rest, isMemory := strings.CutPrefix(key, memoryPrefix)
	if !isMemory {
		return "", nil, false
	}
	fields := strings.Split(rest, "/")
	return fields[0], fields[1:], true
}

func parseUUID(text string) (snapshot.UUID, bool) {
	var id snapshot.UUID
	if len(text) != hex.EncodedLen(len(id)) {
		return id, false
	}
	_, err := hex.Decode(id[:], []byte(text))
	return id, err == nil
}

func twoUUIDs(parts []string) (a, b snapshot.UUID, ok bool) {
	if len(parts) != 2 {
		return a, b, false
	}
	a, okA := parseUUID(parts[0])
	b, okB := parseUUID(parts[1])
	return a, b, okA && okB
}

func uuidAndDate(parts []string) (snapshot.UUID, time.Time, bool) {
	if len(parts) != 2 {
		return snapshot.UUID{}, time.Time{}, false
	}
	id, ok := parseUUID(parts[0])
	date, err := time.Parse(time.RFC3339Nano, parts[1])
	return id, date, ok && err == nil
}

func orderedPair(a, b snapshot.UUID) [2]snapshot.UUID {
	if compareUUIDs(a, b) > 0 {
		a, b = b, a
	}
	return [2]snapshot.UUID{a, b}
}

// deletionAnswer gives the answer an earlier merge has given to this question, if it still holds.
// A record about a newer deletion ends a rejection. A version with a time the human did not see
// ends an acceptance, whatever that time is. If both hold, the newer decision counts, and of two
// at the same time the one that keeps the entry.
func (mem *memory) deletionAnswer(e *entryState, d Deletion) (answer int, ok bool) {
	var kept *rejection
	for i := range mem.rejected[e.uuid] {
		r := &mem.rejected[e.uuid][i]
		if !d.Deleted.After(r.through) && (kept == nil || r.decided.After(kept.decided)) {
			kept = r
		}
	}
	accepted := mem.accepted[e.uuid]
	if accepted != nil && slices.ContainsFunc(e.versions, func(v *version) bool { return !accepted.seen[memoryDate(v.key.time())] }) {
		accepted = nil
	}
	switch {
	case accepted != nil && (kept == nil || accepted.decided.After(kept.decided)):
		return AnswerDelete, true
	case kept != nil:
		return AnswerKeep, true
	}
	return 0, false
}

// orderIsSettled says the human has let this main version stay on top of these newer edits.
func (mem *memory) orderIsSettled(id snapshot.UUID, main time.Time, replacing []versionKey) bool {
	return slices.ContainsFunc(mem.replaced[id], func(r rememberedOrder) bool {
		return r.main.Equal(main) && !slices.ContainsFunc(replacing, func(k versionKey) bool { return k.time().After(r.replacedBy) })
	})
}

// remember stores a decision of this merge in the result.
func (m *merger) remember(kind string, parts ...string) {
	if m.remembered == nil {
		m.remembered = map[string]bool{}
	}
	m.remembered[memoryPrefix+kind+"/"+strings.Join(parts, "/")] = true
}

func memoryDate(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// rememberDecisions adds the decisions of this merge to the custom data of the result. It writes
// each family as all its pairs, so the memory is the same however the families grew. An item
// that is already there stays with its date.
func rememberDecisions(m *merger) {
	for _, family := range []struct {
		kind    string
		members families
	}{{memoryEntries, m.sameEntries}, {memoryGroups, m.sameGroups}} {
		for _, pair := range family.members.pairs() {
			m.remember(family.kind, pair[0].String(), pair[1].String())
		}
	}
	now := m.in.Now.UTC().Truncate(time.Second)
	for _, key := range slices.Sorted(maps.Keys(m.remembered)) {
		if !slices.ContainsFunc(m.out.Meta.CustomData, func(item snapshot.CustomDataItem) bool { return item.Key == key }) {
			m.out.Meta.CustomData = append(m.out.Meta.CustomData, snapshot.CustomDataItem{Key: key, Value: memoryDate(now), LastModification: now})
		}
	}
	slices.SortFunc(m.out.Meta.CustomData, func(a, b snapshot.CustomDataItem) int { return strings.Compare(a.Key, b.Key) })
}

// pairs gives all pairs inside the families, each with the smaller UUID first.
func (f families) pairs() [][2]snapshot.UUID {
	members := map[snapshot.UUID][]snapshot.UUID{}
	for id := range f {
		head := f.head(id)
		members[head] = append(members[head], id)
	}
	var result [][2]snapshot.UUID
	for head, others := range members {
		all := append(others, head)
		for i, a := range all {
			for _, b := range all[i+1:] {
				result = append(result, orderedPair(a, b))
			}
		}
	}
	return result
}

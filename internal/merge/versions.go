package merge

import (
	"bytes"
	"cmp"
	"slices"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

func init() { addRule(phaseVersions, mergeVersions) }

func mergeVersions(m *merger) {
	for i := range m.in.Copies {
		c := &m.in.Copies[i]
		c.Snapshot.Root.Walk(func(g *snapshot.Group) {
			for j := range g.Entries {
				m.hold(c, g.UUID, &g.Entries[j])
			}
		})
	}
	for _, e := range m.sortedEntries() {
		e.join(m.in.Key)
		e.ruleMain = e.main()
		if !m.goesByMemory(e) {
			m.settle(e)
		}
	}
}

// versionKey identifies a version: its time and its content fingerprint. The order of
// the keys is the order of the versions: the largest key is the main version.
type versionKey struct {
	seconds int64
	nanos   int
	// family is the fingerprint without the custom data. Between versions with the same time
	// it speaks first, so custom data has no say in which edit lies on top: the same edit lacks
	// its custom data in one history and has it in another, and a fingerprint with that data
	// would order two edits one way in a merge of one copy and another way in a merge of all.
	family fingerprint
	// content is the fingerprint of all the version holds. It tells two versions apart.
	content fingerprint
}

func (k Key) versionKey(v *snapshot.EntryVersion) versionKey {
	t := v.Times.LastModification
	return versionKey{
		seconds: t.Unix(),
		nanos:   t.Nanosecond(),
		family:  k.fingerprintOf(contentWithoutCustomData(v)),
		content: k.fingerprintOf(contentOf(v)),
	}
}

func compareKeys(a, b versionKey) int {
	return cmp.Or(
		cmp.Compare(a.seconds, b.seconds),
		cmp.Compare(a.nanos, b.nanos),
		bytes.Compare(a.family[:], b.family[:]),
		bytes.Compare(a.content[:], b.content[:]),
	)
}

// time gives the time of the version in UTC.
func (k versionKey) time() time.Time { return time.Unix(k.seconds, int64(k.nanos)).UTC() }

func (k versionKey) sameTime(other versionKey) bool {
	return k.seconds == other.seconds && k.nanos == other.nanos
}

// version is one member of the set of versions of an entry.
type version struct {
	key versionKey
	// data holds the largest usage of all places where the copies hold this version.
	data snapshot.EntryVersion
}

// usage holds the values that change without a new version, so they are not part of the
// version's identity. Joining them is a maximum.
type usage struct {
	lastAccess time.Time
	count      int64
	located    time.Time
	previous   *snapshot.UUID
}

func usageOf(v *snapshot.EntryVersion) usage {
	return usage{
		lastAccess: v.Times.LastAccess,
		count:      v.Times.UsageCount,
		located:    v.Times.LocationChanged,
		previous:   v.PreviousParentGroup,
	}
}

func (u usage) applyTo(v *snapshot.EntryVersion) {
	v.Times.LastAccess = u.lastAccess
	v.Times.UsageCount = u.count
	v.Times.LocationChanged = u.located
	v.PreviousParentGroup = u.previous
}

func (u usage) join(other usage) usage {
	if other.lastAccess.After(u.lastAccess) {
		u.lastAccess = other.lastAccess
	}
	u.count = max(u.count, other.count)
	if compareLocations(other, u) > 0 {
		u.located, u.previous = other.located, other.previous
	}
	return u
}

func compareLocations(a, b usage) int {
	return cmp.Or(a.located.Compare(b.located), comparePointers(a.previous, b.previous))
}

// comparePointers puts nil before any UUID.
func comparePointers(a, b *snapshot.UUID) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	return compareUUIDs(*a, *b)
}

// entryState is an entry during a merge.
type entryState struct {
	uuid snapshot.UUID
	// holdings holds one item for each place a copy holds this entry.
	holdings []holding
	// absorbed holds what the copies still hold of entries an earlier merge has taken into this
	// one. These entries bring their versions and a line in the report, and nothing else: what a
	// copy does to an entry that is gone says nothing about this one. Only an entry with no
	// holding of its own lies where they lie, see join.
	absorbed []holding
	// versions is the set of versions, the oldest first. The last one is the main version.
	versions []*version
	// ruleMain is the main version by the rule, before any answer of the human.
	ruleMain *version
	// group is the group the entry lies in. The newest LocationChanged decides.
	group snapshot.UUID
	// located is that LocationChanged.
	located time.Time
	// recycled says the entry stays deleted, so it has to lie in the recycle bin.
	recycled bool
}

// holding is what one copy holds of an entry.
type holding struct {
	copy *Copy
	// group is the group of the result. source is the group in the copy, which says whether
	// the copy holds the entry in a recycle bin.
	group, source snapshot.UUID
	entry         *snapshot.Entry
	// main and older are the keys of the main version and the history in this copy.
	main  versionKey
	older []versionKey
	// stands holds the versions of the set that stand for main: main itself, or the versions
	// that absorbed it.
	stands []versionKey
	// history holds the versions of the set that stand for the older versions of this copy. An
	// older version that shares one with main counts only as main.
	history map[versionKey]bool
}

// readOrder finds what the versions of this copy became in the set, once all copies are read.
func (h *holding) readOrder(e *entryState) {
	h.stands = e.standIns(h.main, &h.entry.EntryVersion)
	h.history = make(map[versionKey]bool, len(h.older))
	for i, key := range h.older {
		stands := e.standIns(key, &h.entry.History[i])
		if slices.ContainsFunc(stands, func(k versionKey) bool { return slices.Contains(h.stands, k) }) {
			continue
		}
		for _, k := range stands {
			h.history[k] = true
		}
	}
}

// hold records that a copy holds an entry in a group.
func (m *merger) hold(c *Copy, group snapshot.UUID, e *snapshot.Entry) {
	id := m.memory.entries.head(e.UUID)
	state := m.entries[id]
	if state == nil {
		state = &entryState{uuid: id}
		m.entries[id] = state
	}
	h := holding{copy: c, group: m.groupID(group), source: group, entry: e}
	if id == e.UUID {
		state.holdings = append(state.holdings, h)
	} else {
		state.absorbed = append(state.absorbed, h)
	}
}

// join builds the set of versions and the location from the holdings.
func (e *entryState) join(key Key) {
	if len(e.holdings) == 0 {
		e.holdings, e.absorbed = e.absorbed, nil
	}
	for i := range e.absorbed {
		e.read(&e.absorbed[i], key)
	}
	for i := range e.holdings {
		h := &e.holdings[i]
		located := e.read(h, key)
		if i == 0 || cmp.Or(located.Compare(e.located), compareUUIDs(h.group, e.group)) > 0 {
			e.located, e.group = located, h.group
		}
	}
	for _, holdings := range [][]holding{e.absorbed, e.holdings} {
		for i := range holdings {
			holdings[i].readOrder(e)
		}
	}
}

// read adds the versions of a holding to the set, and it gives the newest move of those versions.
func (e *entryState) read(h *holding, key Key) (located time.Time) {
	h.main = key.versionKey(&h.entry.EntryVersion)
	e.put(h.main, h.entry.EntryVersion)
	located = h.entry.Times.LocationChanged
	h.older = make([]versionKey, len(h.entry.History))
	for j := range h.entry.History {
		old := &h.entry.History[j]
		h.older[j] = key.versionKey(old)
		e.put(h.older[j], *old)
		located = later(located, old.Times.LocationChanged)
	}
	return located
}

// put adds a version to the set. The same version in several places joins into one, and a
// version that another one absorbs leaves the set with only its usage left behind, see absorbs.
// So the set never depends on the order in which the versions arrive.
func (e *entryState) put(key versionKey, data snapshot.EntryVersion) {
	i, found := e.search(key)
	if found {
		kept := e.versions[i]
		joined := usageOf(&kept.data).join(usageOf(&data))
		if bytes.Compare(exactOf(&data), exactOf(&kept.data)) < 0 {
			kept.data = data
		}
		joined.applyTo(&kept.data)
		return
	}
	if peers := e.sameTime(i, key); len(peers) > 0 {
		absorbed := false
		for _, peer := range peers {
			if absorbs(&peer.data, &data) {
				usageOf(&peer.data).join(usageOf(&data)).applyTo(&peer.data)
				absorbed = true
			}
		}
		if absorbed {
			return
		}
		e.versions = slices.DeleteFunc(e.versions, func(v *version) bool {
			if !v.key.sameTime(key) || !absorbs(&data, &v.data) {
				return false
			}
			usageOf(&data).join(usageOf(&v.data)).applyTo(&data)
			return true
		})
		i, _ = e.search(key)
	}
	e.versions = slices.Insert(e.versions, i, &version{key: key, data: data})
}

func (e *entryState) search(key versionKey) (int, bool) {
	return slices.BinarySearchFunc(e.versions, key, func(v *version, key versionKey) int {
		return compareKeys(v.key, key)
	})
}

// sameTime gives the versions with the time of the key. at is where the key lies or would lie.
func (e *entryState) sameTime(at int, key versionKey) []*version {
	first, end := at, at
	for first > 0 && e.versions[first-1].key.sameTime(key) {
		first--
	}
	for end < len(e.versions) && e.versions[end].key.sameTime(key) {
		end++
	}
	return e.versions[first:end]
}

// standIns gives the versions of the set that stand for a version a copy holds: the version
// itself, or the versions that absorbed it. There can be several: two versions can each hold
// more than this one and differ from each other.
func (e *entryState) standIns(key versionKey, held *snapshot.EntryVersion) []versionKey {
	at, found := e.search(key)
	if found {
		return []versionKey{key}
	}
	var keys []versionKey
	for _, peer := range e.sameTime(at, key) {
		if absorbs(&peer.data, held) {
			keys = append(keys, peer.key)
		}
	}
	return keys
}

// main gives the main version.
func (e *entryState) main() *version { return e.versions[len(e.versions)-1] }

// result gives the merged entry.
//
// Each version gets the largest usage of itself and of all versions that are not newer. A newer
// version inherits its usage from the older ones anyway, so on a plain history this changes
// nothing. It makes the main version hold the maximum over all versions, and it makes the result
// of a merge the same whether the copies come at once or in parts. Versions with the same time
// share their usage: nothing orders them, and the usage of a version that went into one of them
// must not depend on which part of the copies met it first.
func (e *entryState) result() snapshot.Entry {
	versions := make([]snapshot.EntryVersion, len(e.versions))
	var running usage
	for i := 0; i < len(e.versions); {
		end := i
		for ; end < len(e.versions) && e.versions[end].key.sameTime(e.versions[i].key); end++ {
			running = running.join(usageOf(&e.versions[end].data))
		}
		for ; i < end; i++ {
			versions[i] = e.versions[i].data
			running.applyTo(&versions[i])
		}
	}
	last := len(versions) - 1
	return snapshot.Entry{UUID: e.uuid, EntryVersion: versions[last], History: versions[:last]}
}

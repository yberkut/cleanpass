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
	for _, e := range m.sortedEntries() {
		e.join(m.in.Key)
		m.settle(e)
	}
}

// versionKey identifies a version: its time and its content fingerprint. The order of
// the keys is the order of the versions: the largest key is the main version.
type versionKey struct {
	seconds int64
	nanos   int
	content fingerprint
}

func (k Key) versionKey(v *snapshot.EntryVersion) versionKey {
	t := v.Times.LastModification
	return versionKey{seconds: t.Unix(), nanos: t.Nanosecond(), content: k.fingerprintOf(v)}
}

func compareKeys(a, b versionKey) int {
	return cmp.Or(cmp.Compare(a.seconds, b.seconds), cmp.Compare(a.nanos, b.nanos), bytes.Compare(a.content[:], b.content[:]))
}

// time gives the time of the version in UTC.
func (k versionKey) time() time.Time { return time.Unix(k.seconds, int64(k.nanos)).UTC() }

// version is one member of the set of versions of an entry.
type version struct {
	key  versionKey
	data snapshot.EntryVersion
	// usage joins the values of all places where the copies hold this version.
	usage usage
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
	// versions is the set of versions, the oldest first. The last one is the main version.
	versions []*version
	// group is the group the entry lies in. The newest LocationChanged decides.
	group snapshot.UUID
	// located is that LocationChanged.
	located time.Time
}

// holding is what one copy holds of an entry.
type holding struct {
	copy  string
	group snapshot.UUID
	entry *snapshot.Entry
	// main and history are the keys of the versions in this copy. A version that is both
	// main and history counts only as main.
	main    versionKey
	history map[versionKey]bool
}

// hold records that a copy holds an entry in a group.
func (m *merger) hold(copyName string, group snapshot.UUID, e *snapshot.Entry) {
	state := m.entries[e.UUID]
	if state == nil {
		state = &entryState{uuid: e.UUID}
		m.entries[e.UUID] = state
	}
	state.holdings = append(state.holdings, holding{copy: copyName, group: group, entry: e})
}

// join builds the set of versions and the location from the holdings.
func (e *entryState) join(key Key) {
	for i := range e.holdings {
		h := &e.holdings[i]
		h.main = key.versionKey(&h.entry.EntryVersion)
		e.put(h.main, h.entry.EntryVersion)
		located := h.entry.Times.LocationChanged
		h.history = make(map[versionKey]bool, len(h.entry.History))
		for j := range h.entry.History {
			old := &h.entry.History[j]
			oldKey := key.versionKey(old)
			e.put(oldKey, *old)
			if oldKey != h.main {
				h.history[oldKey] = true
			}
			located = later(located, old.Times.LocationChanged)
		}
		if i == 0 || cmp.Or(located.Compare(e.located), compareUUIDs(h.group, e.group)) > 0 {
			e.located, e.group = located, h.group
		}
	}
}

// put adds a version to the set. The same version in several places joins into one.
func (e *entryState) put(key versionKey, data snapshot.EntryVersion) {
	i, found := slices.BinarySearchFunc(e.versions, key, func(v *version, key versionKey) int {
		return compareKeys(v.key, key)
	})
	if !found {
		e.versions = slices.Insert(e.versions, i, &version{key: key, data: data, usage: usageOf(&data)})
		return
	}
	kept := e.versions[i]
	kept.usage = kept.usage.join(usageOf(&data))
	if bytes.Compare(exactOf(&data), exactOf(&kept.data)) < 0 {
		kept.data = data
	}
}

// main gives the main version.
func (e *entryState) main() *version { return e.versions[len(e.versions)-1] }

// result gives the merged entry.
//
// Each version gets the largest usage of itself and of all older versions. A newer
// version inherits its usage from the older ones anyway, so on a plain history this
// changes nothing. It makes the main version hold the maximum over all versions, and it
// makes the result of a merge the same whether the copies come at once or in parts.
func (e *entryState) result() snapshot.Entry {
	versions := make([]snapshot.EntryVersion, len(e.versions))
	var running usage
	for i, v := range e.versions {
		running = running.join(v.usage)
		versions[i] = v.data
		versions[i].Times.LastAccess = running.lastAccess
		versions[i].Times.UsageCount = running.count
		versions[i].Times.LocationChanged = running.located
		versions[i].PreviousParentGroup = running.previous
	}
	last := len(versions) - 1
	result := snapshot.Entry{UUID: e.uuid, EntryVersion: versions[last]}
	if last > 0 {
		result.History = versions[:last]
	}
	return result
}

package merge

import (
	"cmp"
	"slices"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// keepRecycledInTheBin runs after phaseGroups, because only then the merged tree is known.
func init() { addRule(phaseMetadata, keepRecycledInTheBin) }

// recycleBins is what the copies say about their recycle bins.
type recycleBins struct {
	// inBin holds for each copy the groups that lie in a bin, with the time each one went
	// there. The time of a bin itself is zero.
	inBin map[*Copy]map[snapshot.UUID]time.Time
	// outside holds the groups that a copy holds outside every bin.
	outside map[snapshot.UUID]bool
	// result is the bin of the merged copy, and changed its RecycleBinChanged.
	result  snapshot.UUID
	changed time.Time
}

// readBins finds the groups in the bins. A bin is a bin in every copy, also in a copy that
// holds the same group and has no bin of its own or has switched its bin off. KeePassXC itself
// looks at the group alone. A root group is never a bin.
func (m *merger) readBins() recycleBins {
	declared := map[snapshot.UUID]bool{}
	for i := range m.in.Copies {
		if bin := m.in.Copies[i].Snapshot.Meta.RecycleBin; !bin.IsZero() && !m.roots[bin] {
			declared[bin] = true
		}
	}
	bins := recycleBins{inBin: map[*Copy]map[snapshot.UUID]time.Time{}, outside: map[snapshot.UUID]bool{}}
	existing := map[snapshot.UUID]bool{}
	for i := range m.in.Copies {
		c := &m.in.Copies[i]
		groups := map[snapshot.UUID]time.Time{}
		bins.inBin[c] = groups
		var walk func(g *snapshot.Group, inside bool, since time.Time)
		walk = func(g *snapshot.Group, inside bool, since time.Time) {
			if inside {
				since = later(since, g.Times.LocationChanged)
			} else if declared[g.UUID] {
				inside = true
			}
			if declared[g.UUID] {
				existing[g.UUID] = true
			}
			if inside {
				groups[m.groupID(g.UUID)] = since
			} else {
				bins.outside[m.groupID(g.UUID)] = true
			}
			for j := range g.Groups {
				walk(&g.Groups[j], inside, since)
			}
		}
		walk(&c.Snapshot.Root, false, time.Time{})
	}
	bins.chooseResult(m.in.Copies, existing)
	return bins
}

// chooseResult takes the newest bin that exists, and the smallest UUID of two equally new ones.
// A copy without a bin takes nothing away, because the entries in the bin of another copy must
// not come back to life.
func (b *recycleBins) chooseResult(copies []Copy, existing map[snapshot.UUID]bool) {
	best := slices.MaxFunc(copies, func(x, y Copy) int {
		first, second := &x.Snapshot.Meta, &y.Snapshot.Meta
		return cmp.Or(
			compareFlags(existing[first.RecycleBin], existing[second.RecycleBin]),
			first.RecycleBinChanged.Compare(second.RecycleBinChanged),
			compareUUIDs(second.RecycleBin, first.RecycleBin),
		)
	}).Snapshot.Meta
	b.result, b.changed = best.RecycleBin, best.RecycleBinChanged
}

func compareFlags(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// recycled says the copy holds the entry in a bin, and since when.
func (b *recycleBins) recycled(h *holding) (time.Time, bool) {
	since, inBin := b.inBin[h.copy][h.group]
	return later(since, h.entry.Times.LocationChanged), inBin
}

// count says how many holdings of the entry lie in a bin and how many outside.
func (b *recycleBins) count(e *entryState) (inBin, outside int) {
	for i := range e.holdings {
		if _, recycled := b.recycled(&e.holdings[i]); recycled {
			inBin++
		} else {
			outside++
		}
	}
	return inBin, outside
}

// placeBy lets only the holdings in the bin, or only those outside it, decide where the entry
// lies, as if the others never moved it. A version only the others hold keeps no place of its
// own, so it counts for nothing in the next merge either: the result is the same whether the
// copies come at once or in parts. The main version still takes the newest place of all versions.
func (m *merger) placeBy(e *entryState, inBin bool) {
	for _, v := range e.versions {
		v.data.Times.LocationChanged, v.data.PreviousParentGroup = time.Time{}, nil
	}
	placed := false
	for i := range e.holdings {
		h := &e.holdings[i]
		if _, recycled := m.bins.recycled(h); recycled != inBin {
			continue
		}
		e.find(h.main).takePlace(&h.entry.EntryVersion)
		located := h.entry.Times.LocationChanged
		for j := range h.entry.History {
			old := &h.entry.History[j]
			e.find(m.in.Key.versionKey(old)).takePlace(old)
			located = later(located, old.Times.LocationChanged)
		}
		if !placed || cmp.Or(located.Compare(e.located), compareUUIDs(h.group, e.group)) > 0 {
			e.located, e.group, placed = located, h.group, true
		}
	}
}

// takePlace takes the place of the other version if it is newer.
func (v *version) takePlace(other *snapshot.EntryVersion) {
	if compareLocations(usageOf(other), usageOf(&v.data)) > 0 {
		v.data.Times.LocationChanged, v.data.PreviousParentGroup = other.Times.LocationChanged, other.PreviousParentGroup
	}
}

// keepRecycledInTheBin moves an entry that stays deleted to the bin of the result if its group
// has ended up outside. That happens when a copy has deleted the group and another one holds
// it outside the bin: the group stays, and what only the first copy holds must not come back
// to life with it.
func keepRecycledInTheBin(m *merger) {
	if m.bins.result.IsZero() {
		return
	}
	children := map[snapshot.UUID][]snapshot.UUID{}
	for id, g := range m.groups {
		children[g.parent] = append(children[g.parent], id)
	}
	inside := map[snapshot.UUID]bool{}
	var mark func(id snapshot.UUID)
	mark = func(id snapshot.UUID) {
		if !inside[id] {
			inside[id] = true
			for _, child := range children[id] {
				mark(child)
			}
		}
	}
	mark(m.bins.result)
	for _, e := range m.entries {
		if e.recycled && !inside[e.group] {
			e.group = m.bins.result
		}
	}
}

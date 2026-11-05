package lossaudit

import (
	"maps"
	"slices"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// checkGroups wants each group of the copies in the result, with what a person set on it.
//
// Some UUIDs are versions of one group, and then the newest version must stay: all root groups,
// all recycle bins, and the groups an earlier merge joined, which the copies remember. The file
// has no history of groups, so the older versions may go, as the spec says. Such a set of UUIDs
// is a unit here.
//
// Groups with the same path join in a merge by a rule of the spec, which is no decision. The
// audit finds these paths by itself and takes no word of the engine for them: a unit that no
// group of the result carries lives on in a group of the result with its name, under the group
// its parent lives on in. A group that took several units in must hold what each of them holds,
// see holdsJoined.
func (a *auditor) checkGroups() {
	c := newGroupCheck(a)
	keptBy := map[snapshot.UUID][]snapshot.UUID{}
	for _, unit := range slices.SortedFunc(maps.Keys(c.versions), compareUUIDs) {
		if a.deleted(c.members[unit]) {
			continue
		}
		keeper, ok := c.keeper(unit)
		if !ok {
			a.lose(Loss{Kind: GroupLost, Object: c.members[unit][0]})
			continue
		}
		keptBy[keeper] = append(keptBy[keeper], unit)
	}
	for keeper, units := range keptBy {
		got := c.result[keeper]
		holds := holdsGroup
		if len(units) > 1 {
			holds = holdsJoined
			var all []*snapshot.Group
			for _, unit := range units {
				all = append(all, c.wanted(unit)...)
			}
			if !comesFrom(got, all) {
				slices.SortFunc(units, func(x, y snapshot.UUID) int { return compareUUIDs(c.members[x][0], c.members[y][0]) })
				a.lose(Loss{Kind: GroupFieldsLost, Object: c.members[units[0]][0]})
			}
		}
		for _, unit := range units {
			if !slices.ContainsFunc(c.wanted(unit), func(want *snapshot.Group) bool { return holds(got, want) }) {
				a.lose(Loss{Kind: GroupFieldsLost, Object: c.members[unit][0]})
			}
		}
	}
}

// groupCheck is what checkGroups knows about the copies and the result.
type groupCheck struct {
	// units tells which UUIDs are versions of one group, apart from the root groups.
	units families
	// roots holds the UUIDs of the root groups of the copies. Their unit is the root of the result.
	roots map[snapshot.UUID]bool
	// versions holds the newest versions of each unit, and members its UUIDs the copies hold,
	// sorted. moved and movedOutside hold the units of its parents by the newest move, in all
	// copies and in the copies that hold it outside a bin.
	versions            map[snapshot.UUID]*newest[*snapshot.Group]
	members             map[snapshot.UUID][]snapshot.UUID
	moved, movedOutside map[snapshot.UUID]*newest[snapshot.UUID]
	// outside holds the units a copy holds outside its recycle bin.
	outside map[snapshot.UUID]bool

	result    map[snapshot.UUID]*snapshot.Group
	children  map[snapshot.UUID][]*snapshot.Group
	root      snapshot.UUID
	resultBin snapshot.UUID
	// binUnit is the unit of all bins, if there is one. It lives on only as itself, never in a
	// group with its name.
	binUnit    snapshot.UUID
	hasBins    bool
	keepers    map[snapshot.UUID]snapshot.UUID
	notKept    map[snapshot.UUID]bool
	inProgress map[snapshot.UUID]bool
}

func newGroupCheck(a *auditor) *groupCheck {
	c := &groupCheck{
		units:        maps.Clone(a.memory.groups),
		roots:        map[snapshot.UUID]bool{},
		versions:     map[snapshot.UUID]*newest[*snapshot.Group]{},
		members:      map[snapshot.UUID][]snapshot.UUID{},
		moved:        map[snapshot.UUID]*newest[snapshot.UUID]{},
		movedOutside: map[snapshot.UUID]*newest[snapshot.UUID]{},
		outside:      map[snapshot.UUID]bool{},
		result:       map[snapshot.UUID]*snapshot.Group{},
		children:     map[snapshot.UUID][]*snapshot.Group{},
		root:         a.in.Result.Root.UUID,
		keepers:      map[snapshot.UUID]snapshot.UUID{},
		notKept:      map[snapshot.UUID]bool{},
		inProgress:   map[snapshot.UUID]bool{},
	}
	for _, s := range a.in.Copies {
		c.roots[s.Root.UUID] = true
	}
	var bins []snapshot.UUID
	for _, s := range a.in.Copies {
		if bin := s.Meta.RecycleBin; !bin.IsZero() && !c.roots[bin] {
			bins = append(bins, bin)
		}
	}
	for _, bin := range bins {
		c.units.join(bins[0], bin)
	}
	if len(bins) > 0 {
		c.binUnit, c.hasBins = c.units.head(bins[0]), true
	}
	for _, s := range a.in.Copies {
		c.walkCopy(s, &s.Root, nil, false)
	}
	for unit := range c.members {
		slices.SortFunc(c.members[unit], compareUUIDs)
		c.members[unit] = slices.Compact(c.members[unit])
	}

	var walk func(g *snapshot.Group)
	walk = func(g *snapshot.Group) {
		c.result[g.UUID] = g
		for i := range g.Groups {
			c.children[g.UUID] = append(c.children[g.UUID], &g.Groups[i])
			walk(&g.Groups[i])
		}
	}
	walk(&a.in.Result.Root)
	if _, exists := c.result[a.in.Result.Meta.RecycleBin]; exists && !c.roots[a.in.Result.Meta.RecycleBin] {
		c.resultBin = a.in.Result.Meta.RecycleBin
	}
	return c
}

func (c *groupCheck) unitOf(id snapshot.UUID) snapshot.UUID {
	if c.roots[id] {
		return c.root
	}
	return c.units.head(id)
}

func (c *groupCheck) walkCopy(s *snapshot.Snapshot, g, parent *snapshot.Group, inBin bool) {
	unit := c.unitOf(g.UUID)
	inBin = inBin || (g.UUID == s.Meta.RecycleBin && !c.roots[g.UUID])
	see(c.versions, unit, g.Times.LastModification, g)
	c.members[unit] = append(c.members[unit], g.UUID)
	if parent != nil && c.unitOf(parent.UUID) != unit {
		see(c.moved, unit, g.Times.LocationChanged, c.unitOf(parent.UUID))
		if !inBin {
			see(c.movedOutside, unit, g.Times.LocationChanged, c.unitOf(parent.UUID))
		}
	}
	if !inBin {
		c.outside[unit] = true
	}
	for i := range g.Groups {
		c.walkCopy(s, &g.Groups[i], g, inBin)
	}
}

// keeper gives the group of the result a unit lives on in: the group with one of its UUIDs, or
// the group with its name on its path.
func (c *groupCheck) keeper(unit snapshot.UUID) (snapshot.UUID, bool) {
	if id, ok := c.keepers[unit]; ok {
		return id, true
	}
	if c.notKept[unit] || c.inProgress[unit] {
		return snapshot.UUID{}, false
	}
	c.inProgress[unit] = true
	id, ok := c.findKeeper(unit)
	delete(c.inProgress, unit)
	if ok {
		c.keepers[unit] = id
	} else {
		c.notKept[unit] = true
	}
	return id, ok
}

func (c *groupCheck) findKeeper(unit snapshot.UUID) (snapshot.UUID, bool) {
	if unit == c.root {
		return c.root, true
	}
	for _, member := range c.members[unit] {
		if _, carried := c.result[member]; carried {
			return member, true
		}
	}
	if c.hasBins && unit == c.binUnit {
		return snapshot.UUID{}, false
	}
	// Several groups can stand on one path, when one group made of them would lose something, and
	// a group on a cycle has two places. The one that holds what the unit holds is where it went,
	// if there is one. Of several, the one with a child of the unit under it, see holdsAChildOf.
	var candidates []*snapshot.Group
	for _, place := range c.placesOf(unit) {
		for _, child := range c.children[place] {
			isNamed := slices.ContainsFunc(c.versions[unit].items, func(v *snapshot.Group) bool { return v.Name == child.Name })
			if isNamed && child.UUID != c.resultBin {
				candidates = append(candidates, child)
			}
		}
	}
	if len(candidates) == 0 {
		return snapshot.UUID{}, false
	}
	holds := func(g *snapshot.Group) bool {
		return slices.ContainsFunc(c.wanted(unit), func(want *snapshot.Group) bool { return holdsJoined(g, want) })
	}
	holdsWithAChild := func(g *snapshot.Group) bool { return holds(g) && c.holdsAChildOf(unit, g) }
	for _, fits := range []func(*snapshot.Group) bool{holdsWithAChild, holds} {
		if i := slices.IndexFunc(candidates, fits); i >= 0 {
			return candidates[i].UUID, true
		}
	}
	return candidates[0].UUID, true
}

// holdsAChildOf says that the group g of the result holds a group that keeps its UUID and stands
// under the unit by its newest move, and under no other group with that time. Such a group stands
// in the group the unit went into, under the root if it gave way in a cycle, or in the bin. The
// root and the bin are no candidates, so the unit went into g.
func (c *groupCheck) holdsAChildOf(unit snapshot.UUID, g *snapshot.Group) bool {
	return slices.ContainsFunc(c.children[g.UUID], func(child *snapshot.Group) bool {
		parents := c.parentsOf(c.unitOf(child.UUID))
		return len(parents) > 0 && !slices.ContainsFunc(parents, func(p snapshot.UUID) bool { return p != unit })
	})
}

// parentsOf gives the units the parent of a unit belongs to, as the spec says: by the newest move.
// A move into a bin has no say while a copy still holds the group outside the bins.
func (c *groupCheck) parentsOf(unit snapshot.UUID) []snapshot.UUID {
	moves := c.moved[unit]
	if c.outside[unit] {
		moves = c.movedOutside[unit]
	}
	if moves == nil {
		return nil
	}
	return moves.items
}

// placesOf gives the groups of the result a unit may stand in: where its parent lives on. A merge
// also puts a group under the root to break a cycle of moves, and into the bin if every copy
// holds it in a bin while its parent stays alive. The bin joins no plain group by its path,
// because what lies in that group is alive.
func (c *groupCheck) placesOf(unit snapshot.UUID) []snapshot.UUID {
	var places []snapshot.UUID
	for _, parent := range c.parentsOf(unit) {
		if id, ok := c.keeper(parent); ok {
			places = append(places, id)
		}
	}
	if c.onACycle(unit) {
		places = append(places, c.root)
	}
	if !c.outside[unit] && !c.resultBin.IsZero() {
		places = append(places, c.resultBin)
	}
	return places
}

// onACycle says the newest moves lead from the unit back to it.
func (c *groupCheck) onACycle(unit snapshot.UUID) bool {
	seen := map[snapshot.UUID]bool{}
	var leadsBack func(from snapshot.UUID) bool
	leadsBack = func(from snapshot.UUID) bool {
		for _, parent := range c.parentsOf(from) {
			if parent == unit {
				return true
			}
			if !seen[parent] {
				seen[parent] = true
				if leadsBack(parent) {
					return true
				}
			}
		}
		return false
	}
	return leadsBack(unit)
}

// wanted gives the newest versions of a unit the result must hold one of. Of versions with the
// same time, one that holds all the others has to stay, or what only it holds is gone.
func (c *groupCheck) wanted(unit snapshot.UUID) []*snapshot.Group {
	versions := c.versions[unit].items
	var holders []*snapshot.Group
	for _, v := range versions {
		if !slices.ContainsFunc(versions, func(other *snapshot.Group) bool { return !holdsJoined(v, other) }) {
			holders = append(holders, v)
		}
	}
	if len(holders) > 0 {
		return holders
	}
	return versions
}

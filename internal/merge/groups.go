package merge

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

func init() { addRule(phaseGroups, mergeGroups) }

// groupState is a group during a merge.
type groupState struct {
	// data holds the fields of the group, without children.
	data snapshot.Group
	// parent is the UUID of the parent group. The newest LocationChanged decides.
	parent snapshot.UUID
}

// GroupCycleBroken is the event about a group that left its parent, because moves in different
// copies had put a set of groups under each other. The group lies under the root now.
type GroupCycleBroken struct {
	Group snapshot.UUID
	Name  string
}

func (g GroupCycleBroken) sortKey() string { return "group cycle:" + g.Group.String() }

// GroupsJoined is the event about groups that went into one group, because they had the same
// path or because all of them were a recycle bin.
type GroupsJoined struct {
	Into snapshot.UUID
	Name string
	// Joined holds the UUIDs of the other groups, sorted.
	Joined []snapshot.UUID
}

func (g GroupsJoined) sortKey() string { return "groups joined:" + g.Into.String() }

// mergeGroups joins the groups of all copies. Every group lands in the result: as itself or in
// the group that took its place, so no entry loses its place. It runs before everything else,
// because the place of an entry is a group of the result.
//
// A step decides by the tree the steps before it give: the fields and the place of each group,
// then the cycles, then the paths. A new join changes that tree and can bring two more groups
// on one path, so the steps run until no join is left.
func mergeGroups(m *merger) {
	var broken []snapshot.UUID
	for {
		m.bins = m.readBins()
		m.joinGroups()
		broken = m.breakCycles()
		m.keepDeletedGroupsInTheBin()
		if !m.joinSamePaths() {
			break
		}
	}
	for _, id := range broken {
		m.tell(GroupCycleBroken{Group: id, Name: m.groups[id].data.Name})
	}
	m.tellNewJoins()
	m.tellGroupsKeptApart()
}

// joinGroups makes one state for each group of the result out of all copies. The versions of
// one group are those of one UUID, of all bins and of the groups an earlier merge has joined:
// the newest of them holds. The groups this merge joins by their path lose nothing, see
// joinable.
func (m *merger) joinGroups() {
	units := map[snapshot.UUID]*groupState{}
	for i := range m.in.Copies {
		c := &m.in.Copies[i]
		m.seeGroup(units, c, snapshot.UUID{}, &c.Snapshot.Root)
	}
	joined := map[snapshot.UUID][]*groupState{}
	for unit, state := range units {
		id := m.groupID(unit)
		joined[id] = append(joined[id], state)
	}
	m.groups = make(map[snapshot.UUID]*groupState, len(joined))
	for id, states := range joined {
		slices.SortFunc(states, func(a, b *groupState) int { return compareGroupVersions(&b.data, &a.data) })
		state := &groupState{data: states[0].data, parent: states[0].parent}
		for _, other := range states[1:] {
			state.take(joinFields(state.data, other.data), other.data, other.parent)
		}
		m.groups[id] = state
	}
}

func (m *merger) seeGroup(units map[snapshot.UUID]*groupState, c *Copy, parent snapshot.UUID, g *snapshot.Group) {
	id := m.groupID(g.UUID)
	fields := *g
	fields.UUID = id
	fields.Entries, fields.Groups = nil, nil
	if _, inBin := m.bins.inBin[c][g.UUID]; (inBin && m.bins.outside[id]) || parent == id {
		// Another copy holds the group outside the bin, so the group stays there, and the
		// move to the bin has no say. A place inside the group itself comes from two groups
		// that are one by now, and it has no say either. The zero values lose to any other place.
		parent, fields.Times.LocationChanged, fields.PreviousParentGroup = snapshot.UUID{}, time.Time{}, nil
	}

	unit := m.unitID(g.UUID)
	if state := units[unit]; state == nil {
		units[unit] = &groupState{data: fields, parent: parent}
	} else {
		state.join(fields, parent)
	}
	for i := range g.Groups {
		m.seeGroup(units, c, id, &g.Groups[i])
	}
}

// join takes in another version of the same group: the fields of the newest version, see
// compareGroupVersions.
func (s *groupState) join(other snapshot.Group, parent snapshot.UUID) {
	fields := s.data
	if compareGroupVersions(&other, &s.data) > 0 {
		fields = other
	}
	s.take(fields, other, parent)
}

// take sets the fields, and of s and other it keeps the parent of the newest move. Usage joins
// as a maximum.
func (s *groupState) take(fields, other snapshot.Group, parent snapshot.UUID) {
	fields.Times.LastAccess = later(s.data.Times.LastAccess, other.Times.LastAccess)
	fields.Times.UsageCount = max(s.data.Times.UsageCount, other.Times.UsageCount)

	moved := cmp.Or(
		other.Times.LocationChanged.Compare(s.data.Times.LocationChanged),
		compareUUIDs(parent, s.parent),
		comparePointers(other.PreviousParentGroup, s.data.PreviousParentGroup),
	)
	if moved > 0 {
		s.parent = parent
		fields.Times.LocationChanged, fields.PreviousParentGroup = other.Times.LocationChanged, other.PreviousParentGroup
	} else {
		fields.Times.LocationChanged, fields.PreviousParentGroup = s.data.Times.LocationChanged, s.data.PreviousParentGroup
	}
	s.data = fields
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// groupContent encodes the fields of a group, only to tell two versions with the same time
// apart in a way that does not depend on the order of the copies.
func groupContent(g *snapshot.Group) []byte {
	var e encoding
	e.text(g.Name)
	e.text(g.Notes)
	e.number(g.IconID)
	e.uuid(g.CustomIcon)
	e.moment(g.Times.Creation)
	e.moment(g.Times.Expiry)
	e.flag(g.Times.Expires)
	e.flag(g.IsExpanded)
	e.text(g.DefaultAutoTypeSequence)
	e.optional(g.EnableAutoType)
	e.optional(g.EnableSearching)
	e.uuid(g.LastTopVisibleEntry)
	e.text(g.Tags)
	for i := range g.CustomData {
		e.text(g.CustomData[i].Key)
		e.text(g.CustomData[i].Value)
		e.moment(g.CustomData[i].LastModification)
	}
	return e
}

// breakCycles gives each group a way up to the root. Each copy holds a tree, but the newest move
// of each group can come from another copy, and together they can put groups under each other.
// In such a cycle the newest moves hold, as they do everywhere, so the oldest move gives way and
// its group goes under the root. It says which groups went there, sorted.
func (m *merger) breakCycles() []snapshot.UUID {
	var broken []snapshot.UUID
	const onThisWay, done = 1, 2
	seen := map[snapshot.UUID]int{m.root: done}
	for _, start := range slices.SortedFunc(maps.Keys(m.groups), compareUUIDs) {
		var way []snapshot.UUID
		id := start
		for seen[id] == 0 {
			seen[id] = onThisWay
			way = append(way, id)
			if _, known := m.groups[m.groups[id].parent]; !known {
				m.groups[id].parent = m.root
			}
			id = m.groups[id].parent
		}
		if seen[id] == onThisWay {
			cycle := way[slices.Index(way, id):]
			oldest := slices.MinFunc(cycle, func(a, b snapshot.UUID) int {
				return cmp.Or(m.groups[a].data.Times.LocationChanged.Compare(m.groups[b].data.Times.LocationChanged), compareUUIDs(a, b))
			})
			m.groups[oldest].parent = m.root
			broken = append(broken, oldest)
		}
		for _, passed := range way {
			seen[passed] = done
		}
	}
	slices.SortFunc(broken, compareUUIDs)
	return broken
}

// keepDeletedGroupsInTheBin moves a group that no copy holds outside the bin straight to the bin
// if its parent has ended up outside: one copy deleted both, and another one still holds the
// parent. Nobody wants the group back, and the next merge must still see it as deleted.
func (m *merger) keepDeletedGroupsInTheBin() {
	for id, state := range m.groups {
		if id != m.root && id != m.bins.result && !m.bins.outside[id] && m.bins.outside[state.parent] {
			state.parent = m.bins.result
		}
	}
}

// joinSamePaths joins the groups with the same name under the same parent: all paths above them
// are equal once the groups higher up are one. A group joins the first group on its path that
// it is joinable with, by UUID, and otherwise stays apart. The groups a join changes come back
// in the next round. It says whether it joined anything.
func (m *merger) joinSamePaths() bool {
	type family struct {
		head snapshot.UUID
		// fields is what the family has become so far.
		fields snapshot.Group
	}
	joined := false
	onPlace := map[place][]*family{}
	for _, id := range slices.SortedFunc(maps.Keys(m.groups), compareUUIDs) {
		if id == m.root {
			continue
		}
		state := m.groups[id]
		at := m.placeOf(id)
		i := slices.IndexFunc(onPlace[at], func(f *family) bool { return joinable(&f.fields, &state.data) })
		if i < 0 {
			onPlace[at] = append(onPlace[at], &family{head: id, fields: state.data})
			continue
		}
		f := onPlace[at][i]
		m.sameGroups.join(f.head, id)
		f.fields = joinFields(f.fields, state.data)
		joined = true
	}
	return joined
}

// tellNewJoins tells about the joins of this merge. An old join is no news.
func (m *merger) tellNewJoins() {
	joined := map[snapshot.UUID][]snapshot.UUID{}
	for member := range m.sameGroups {
		head := m.sameGroups.head(member)
		if m.memory.groups.head(member) != head {
			joined[head] = append(joined[head], member)
		}
	}
	for head, members := range joined {
		slices.SortFunc(members, compareUUIDs)
		m.tell(GroupsJoined{Into: head, Name: m.groups[head].data.Name, Joined: members})
	}
}

// groupID gives the UUID a group has in the merged tree. All root groups are one group.
func (m *merger) groupID(id snapshot.UUID) snapshot.UUID {
	if m.roots[id] {
		return m.root
	}
	return m.sameGroups.head(id)
}

// unitID gives the UUID of the group whose versions this group's versions are: the same UUID,
// a bin, or a group an earlier merge joined. The newest of them holds.
func (m *merger) unitID(id snapshot.UUID) snapshot.UUID {
	if m.roots[id] {
		return m.root
	}
	return m.sameUnits.head(id)
}

// buildTree gives the merged tree: every group under its parent, every entry in its group. After
// breakCycles a way from every group leads to the root.
func (m *merger) buildTree() snapshot.Group {
	children := map[snapshot.UUID][]snapshot.UUID{}
	for id, state := range m.groups {
		if id != m.root {
			children[state.parent] = append(children[state.parent], id)
		}
	}
	entries := map[snapshot.UUID][]snapshot.Entry{}
	for _, e := range m.sortedEntries() {
		group := e.group
		if _, known := m.groups[group]; !known {
			group = m.root
		}
		entries[group] = append(entries[group], e.result())
	}

	var build func(id snapshot.UUID) snapshot.Group
	build = func(id snapshot.UUID) snapshot.Group {
		group := m.groups[id].data
		group.Entries = entries[id]
		slices.SortFunc(group.Entries, compareEntriesForTree)
		for _, child := range children[id] {
			group.Groups = append(group.Groups, build(child))
		}
		slices.SortFunc(group.Groups, func(a, b snapshot.Group) int {
			return cmp.Or(strings.Compare(a.Name, b.Name), compareUUIDs(a.UUID, b.UUID))
		})
		return group
	}
	return build(m.root)
}

// compareEntriesForTree gives a fixed order, the same for any order of the copies. Order
// by title is what KeePassXC shows by default.
func compareEntriesForTree(a, b snapshot.Entry) int {
	return cmp.Or(strings.Compare(titleOf(&a.EntryVersion), titleOf(&b.EntryVersion)), compareUUIDs(a.UUID, b.UUID))
}

func titleOf(v *snapshot.EntryVersion) string {
	for i := range v.Fields {
		if v.Fields[i].Key == snapshot.FieldTitle {
			return v.Fields[i].Value
		}
	}
	return ""
}

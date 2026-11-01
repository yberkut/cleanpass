package merge

import (
	"bytes"
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
		if !m.joinSamePaths() {
			break
		}
	}
	for _, id := range broken {
		m.tell(GroupCycleBroken{Group: id, Name: m.groups[id].data.Name})
	}
	m.tellNewJoins()
}

// joinGroups makes one state for each group of the result out of all copies.
func (m *merger) joinGroups() {
	m.groups = map[snapshot.UUID]*groupState{}
	for i := range m.in.Copies {
		c := &m.in.Copies[i]
		m.seeGroup(c, snapshot.UUID{}, &c.Snapshot.Root)
	}
}

func (m *merger) seeGroup(c *Copy, parent snapshot.UUID, g *snapshot.Group) {
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

	state := m.groups[id]
	if state == nil {
		m.groups[id] = &groupState{data: fields, parent: parent}
	} else {
		state.join(fields, parent)
	}
	for i := range g.Groups {
		m.seeGroup(c, id, &g.Groups[i])
	}
}

// join takes the fields of the newest version and the parent of the newest move. Usage
// joins as a maximum.
func (s *groupState) join(other snapshot.Group, parent snapshot.UUID) {
	joined := s.data
	if compareGroupVersions(&other, &s.data) > 0 {
		joined = other
	}
	joined.Times.LastAccess = later(s.data.Times.LastAccess, other.Times.LastAccess)
	joined.Times.UsageCount = max(s.data.Times.UsageCount, other.Times.UsageCount)

	moved := cmp.Or(
		other.Times.LocationChanged.Compare(s.data.Times.LocationChanged),
		compareUUIDs(parent, s.parent),
		comparePointers(other.PreviousParentGroup, s.data.PreviousParentGroup),
	)
	if moved > 0 {
		s.parent = parent
		joined.Times.LocationChanged, joined.PreviousParentGroup = other.Times.LocationChanged, other.PreviousParentGroup
	} else {
		joined.Times.LocationChanged, joined.PreviousParentGroup = s.data.Times.LocationChanged, s.data.PreviousParentGroup
	}
	s.data = joined
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func compareGroupVersions(a, b *snapshot.Group) int {
	return cmp.Or(a.Times.LastModification.Compare(b.Times.LastModification), bytes.Compare(groupContent(a), groupContent(b)))
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

// joinSamePaths joins each two groups with the same name under the same parent: all paths above
// them are equal once the groups higher up are one. The groups a join changes come back in the
// next round. The bin joins no plain group: what lies in such a group is alive, and it would
// count as deleted. It says whether it joined anything.
func (m *merger) joinSamePaths() bool {
	type place struct {
		parent snapshot.UUID
		name   string
		isBin  bool
	}
	joined := false
	first := map[place]snapshot.UUID{}
	for _, id := range slices.SortedFunc(maps.Keys(m.groups), compareUUIDs) {
		if id == m.root {
			continue
		}
		state := m.groups[id]
		at := place{parent: state.parent, name: state.data.Name, isBin: id == m.bins.result}
		if other, taken := first[at]; taken {
			m.sameGroups.join(other, id)
			joined = true
		} else {
			first[at] = id
		}
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
		if v.Fields[i].Key == "Title" {
			return v.Fields[i].Value
		}
	}
	return ""
}

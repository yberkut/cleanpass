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

// mergeGroups joins all groups of all copies by UUID. Every group of every copy lands in
// the result, so no entry loses its place.
func mergeGroups(m *merger) {
	for i := range m.in.Copies {
		m.seeGroup(snapshot.UUID{}, &m.in.Copies[i].Snapshot.Root)
	}
}

func (m *merger) seeGroup(parent snapshot.UUID, g *snapshot.Group) {
	id := m.groupID(g.UUID)
	fields := *g
	fields.UUID = id
	fields.Entries, fields.Groups = nil, nil

	state := m.groups[id]
	if state == nil {
		m.groups[id] = &groupState{data: fields, parent: parent}
	} else {
		state.join(fields, parent)
	}
	for i := range g.Groups {
		m.seeGroup(id, &g.Groups[i])
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

// buildTree gives the merged tree: every group under its parent, every entry in its group.
// A group whose parent is gone or that no path from the root reaches goes under the root,
// so that nothing disappears.
func (m *merger) buildTree() snapshot.Group {
	childGroups := map[snapshot.UUID][]snapshot.UUID{}
	for _, id := range slices.SortedFunc(maps.Keys(m.groups), compareUUIDs) {
		if id == m.root {
			continue
		}
		parent := m.groups[id].parent
		if _, known := m.groups[parent]; !known {
			parent = m.root
		}
		childGroups[parent] = append(childGroups[parent], id)
	}
	entries := map[snapshot.UUID][]snapshot.Entry{}
	for _, e := range m.sortedEntries() {
		group := e.group
		if _, known := m.groups[group]; !known {
			group = m.root
		}
		entries[group] = append(entries[group], e.result())
	}

	placed := map[snapshot.UUID]bool{}
	var build func(id snapshot.UUID) snapshot.Group
	build = func(id snapshot.UUID) snapshot.Group {
		placed[id] = true
		group := m.groups[id].data
		group.Entries = entries[id]
		slices.SortFunc(group.Entries, compareEntriesForTree)
		for _, child := range childGroups[id] {
			if !placed[child] {
				group.Groups = append(group.Groups, build(child))
			}
		}
		return group
	}
	root := build(m.root)
	for _, id := range slices.SortedFunc(maps.Keys(m.groups), compareUUIDs) {
		if !placed[id] {
			root.Groups = append(root.Groups, build(id))
		}
	}
	sortGroups(&root)
	return root
}

// compareEntriesForTree gives a fixed order, the same for any order of the copies. Order
// by title is what KeePassXC shows by default.
func compareEntriesForTree(a, b snapshot.Entry) int {
	return cmp.Or(strings.Compare(titleOf(&a.EntryVersion), titleOf(&b.EntryVersion)), compareUUIDs(a.UUID, b.UUID))
}

func sortGroups(g *snapshot.Group) {
	slices.SortFunc(g.Groups, func(a, b snapshot.Group) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), compareUUIDs(a.UUID, b.UUID))
	})
	for i := range g.Groups {
		sortGroups(&g.Groups[i])
	}
}

func titleOf(v *snapshot.EntryVersion) string {
	for i := range v.Fields {
		if v.Fields[i].Key == "Title" {
			return v.Fields[i].Value
		}
	}
	return ""
}

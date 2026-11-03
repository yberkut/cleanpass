package merge

import (
	"bytes"
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// A join by path must lose nothing a person set on any of the groups. So two groups on one path
// become one only if each field a person sets is the same in both or empty in one of them, and
// an item of custom data with the same key has the same value. The group they make takes the
// fields of the newest one, fills each empty field from the other, and holds the tags and the
// custom data of both. Otherwise both groups stay, see GroupsKeptApart.
//
// The time of creation is no field a person sets: an import writes a new one. The group takes it
// from the newest one, as all the rest.

// defaultGroupIcon is the icon KeePassXC gives a new group. With no custom icon it counts as no
// icon a person has chosen.
const defaultGroupIcon = 48

// joinable says one group made of a and b loses nothing of either.
func joinable(a, b *snapshot.Group) bool {
	values := func(g *snapshot.Group) map[string]string {
		result := map[string]string{}
		for i := range g.CustomData {
			if item := &g.CustomData[i]; !item.IsService() {
				result[item.Key] = item.Value
			}
		}
		return result
	}
	first, second := values(a), values(b)
	for key, value := range first {
		if other, both := second[key]; both && other != value {
			return false
		}
	}
	for _, field := range personFields {
		if !field.empty(a) && !field.empty(b) && !field.same(a, b) {
			return false
		}
	}
	return true
}

// personField is a field of a group a person sets, apart from the name, the tags and the custom
// data.
type personField struct {
	empty func(g *snapshot.Group) bool
	same  func(a, b *snapshot.Group) bool
	// take copies the field from one group into another.
	take func(into, from *snapshot.Group)
}

var personFields = []personField{
	{
		empty: func(g *snapshot.Group) bool { return g.Notes == "" },
		same:  func(a, b *snapshot.Group) bool { return a.Notes == b.Notes },
		take:  func(into, from *snapshot.Group) { into.Notes = from.Notes },
	},
	{
		empty: func(g *snapshot.Group) bool { return g.IconID == defaultGroupIcon && g.CustomIcon.IsZero() },
		same:  func(a, b *snapshot.Group) bool { return a.IconID == b.IconID && a.CustomIcon == b.CustomIcon },
		take:  func(into, from *snapshot.Group) { into.IconID, into.CustomIcon = from.IconID, from.CustomIcon },
	},
	{
		empty: func(g *snapshot.Group) bool { return !g.Times.Expires },
		same:  func(a, b *snapshot.Group) bool { return a.Times.Expiry.Equal(b.Times.Expiry) },
		take:  func(into, from *snapshot.Group) { into.Times.Expires, into.Times.Expiry = true, from.Times.Expiry },
	},
	{
		empty: func(g *snapshot.Group) bool { return g.DefaultAutoTypeSequence == "" },
		same:  func(a, b *snapshot.Group) bool { return a.DefaultAutoTypeSequence == b.DefaultAutoTypeSequence },
		take:  func(into, from *snapshot.Group) { into.DefaultAutoTypeSequence = from.DefaultAutoTypeSequence },
	},
	{
		empty: func(g *snapshot.Group) bool { return g.EnableAutoType == nil },
		same:  func(a, b *snapshot.Group) bool { return *a.EnableAutoType == *b.EnableAutoType },
		take:  func(into, from *snapshot.Group) { into.EnableAutoType = from.EnableAutoType },
	},
	{
		empty: func(g *snapshot.Group) bool { return g.EnableSearching == nil },
		same:  func(a, b *snapshot.Group) bool { return *a.EnableSearching == *b.EnableSearching },
		take:  func(into, from *snapshot.Group) { into.EnableSearching = from.EnableSearching },
	},
}

// joinFields gives the fields of one group made of base and other, two joinable groups, where
// base is the newer one.
func joinFields(base, other snapshot.Group) snapshot.Group {
	for _, field := range personFields {
		if field.empty(&base) && !field.empty(&other) {
			field.take(&base, &other)
		}
	}
	base.Tags = joinTags(base.Tags, other.Tags)
	base.CustomData = joinGroupCustomData(base.CustomData, other.CustomData)
	return base
}

// joinTags keeps the spelling of base when other adds no tag.
func joinTags(base, other string) string {
	all := splitTags(base + ";" + other)
	if len(all) == len(splitTags(base)) {
		return base
	}
	return strings.Join(all, ";")
}

// joinGroupCustomData adds the items of other that base lacks, and then sorts them by key, so the
// result does not depend on the order of the joins. The service items are those of base.
func joinGroupCustomData(base, other []snapshot.CustomDataItem) []snapshot.CustomDataItem {
	byKey := map[string]snapshot.CustomDataItem{}
	for _, item := range base {
		byKey[item.Key] = item
	}
	added := false
	for _, item := range other {
		if _, known := byKey[item.Key]; !known && !item.IsService() {
			byKey[item.Key], added = item, true
		}
	}
	if !added {
		return base
	}
	return slices.SortedFunc(maps.Values(byKey), func(a, b snapshot.CustomDataItem) int { return strings.Compare(a.Key, b.Key) })
}

// richness counts what a person has set on a group. Of two versions with the same time the one
// that holds all the other holds is richer, so it wins.
func richness(g *snapshot.Group) int {
	count := len(splitTags(g.Tags)) + len(contentCustomData(g.CustomData))
	for _, field := range personFields {
		if !field.empty(g) {
			count++
		}
	}
	return count
}

// GroupsKeptApart is the event about groups that stand on one path and stay apart, because one
// group made of them would lose what a person has set on one of them.
type GroupsKeptApart struct {
	Name string
	// Groups holds the UUIDs of the groups, sorted.
	Groups []snapshot.UUID
}

func (g GroupsKeptApart) sortKey() string { return "groups kept apart:" + g.Groups[0].String() }

// place is where a group stands in the merged tree. The bin joins no plain group: what lies in
// such a group is alive, and it would count as deleted.
type place struct {
	parent snapshot.UUID
	name   string
	isBin  bool
}

func (m *merger) placeOf(id snapshot.UUID) place {
	state := m.groups[id]
	return place{parent: state.parent, name: state.data.Name, isBin: id == m.bins.result}
}

// tellGroupsKeptApart tells about each path with more than one group.
func (m *merger) tellGroupsKeptApart() {
	onPlace := map[place][]snapshot.UUID{}
	for id := range m.groups {
		if id != m.root {
			at := m.placeOf(id)
			onPlace[at] = append(onPlace[at], id)
		}
	}
	for at, ids := range onPlace {
		if len(ids) > 1 {
			slices.SortFunc(ids, compareUUIDs)
			m.tell(GroupsKeptApart{Name: at.name, Groups: ids})
		}
	}
}

// compareGroupVersions orders two versions of a group: by time, then the richer one, then by
// content only to give a fixed order.
func compareGroupVersions(a, b *snapshot.Group) int {
	return cmp.Or(
		a.Times.LastModification.Compare(b.Times.LastModification),
		cmp.Compare(richness(a), richness(b)),
		bytes.Compare(groupContent(a), groupContent(b)),
	)
}

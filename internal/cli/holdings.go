package cli

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// keepsMainCustomData says that the merge of all holds the main version of each entry of the
// copy with the custom data it has here. If not, that version went into one with more, so the
// copy lacks data it has in use.
//
// It looks at the main version of the copy and not at the one on top of the merge of this file:
// with two versions of the same time, the key of the run can put an older edit there, and that
// one may lack its custom data in the history, as KeePassXC writes it on each edit.
func keepsMainCustomData(copy, merged *snapshot.Snapshot) bool {
	kept := merged.Root.EntriesByUUID()
	holds := true
	copy.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			// The memory of an earlier merge can take an entry away: it went into another entry,
			// or the human deleted it. Then there is nothing here to hold it against.
			if e := kept[g.Entries[i].UUID]; e != nil {
				holds = holds && holdsVersionWithCustomData(e, &g.Entries[i].EntryVersion)
			}
		}
	})
	return holds
}

func holdsVersionWithCustomData(e *snapshot.Entry, main *snapshot.EntryVersion) bool {
	same := func(v *snapshot.EntryVersion) bool {
		return v.Times.LastModification.Equal(main.Times.LastModification) &&
			reflect.DeepEqual(plainCustomData(v.CustomData), plainCustomData(main.CustomData))
	}
	if same(&e.EntryVersion) {
		return true
	}
	for i := range e.History {
		if same(&e.History[i]) {
			return true
		}
	}
	return false
}

// holdings is what a merged copy holds, in a form where two merges can be compared. It
// leaves out all that changes with no edit: the service data, the time stamps of changes,
// the usage values, the state of the window of KeePassXC and the order inside a list.
type holdings struct {
	meta      snapshot.Meta
	groups    map[snapshot.UUID]groupHolding
	entries   map[snapshot.UUID]entryHolding
	deletions map[snapshot.UUID]bool
}

type groupHolding struct {
	fields snapshot.Group
	parent snapshot.UUID
}

// entryHolding counts the versions of an entry instead of naming them. The engine alone
// knows what makes two versions the same, and without decisions it loses none. So the
// merge of all holds every version of the merge of one file, and the same count means the
// same versions, apart from custom data: a version with less of it goes into one with more.
//
// In a history that is what KeePassXC writes on each edit, so it is no difference between the
// copies. keepsMainCustomData looks at the main version of the copy.
type entryHolding struct {
	group    snapshot.UUID
	versions int
}

func holdingsOf(merged *snapshot.Snapshot) holdings {
	result := holdings{
		meta:      metaFields(merged.Meta),
		groups:    map[snapshot.UUID]groupHolding{},
		entries:   map[snapshot.UUID]entryHolding{},
		deletions: map[snapshot.UUID]bool{},
	}
	// All root groups are one group in a merge, whatever their UUIDs.
	inTree := func(id snapshot.UUID) snapshot.UUID {
		if id == merged.Root.UUID {
			return snapshot.UUID{}
		}
		return id
	}
	parents := map[snapshot.UUID]snapshot.UUID{}
	merged.Root.Walk(func(g *snapshot.Group) {
		id := inTree(g.UUID)
		result.groups[id] = groupHolding{fields: groupFields(g, id), parent: parents[g.UUID]}
		for i := range g.Groups {
			parents[g.Groups[i].UUID] = id
		}
		for i := range g.Entries {
			e := &g.Entries[i]
			result.entries[e.UUID] = entryHolding{group: id, versions: 1 + len(e.History)}
		}
	})
	for i := range merged.Deletions {
		result.deletions[merged.Deletions[i].UUID] = true
	}
	return result
}

func groupFields(g *snapshot.Group, id snapshot.UUID) snapshot.Group {
	fields := *g
	fields.UUID = id
	fields.Entries, fields.Groups = nil, nil
	fields.Times = snapshot.Times{Creation: g.Times.Creation, Expiry: g.Times.Expiry, Expires: g.Times.Expires}
	fields.PreviousParentGroup = nil
	fields.IsExpanded = false
	fields.LastTopVisibleEntry = snapshot.UUID{}
	fields.CustomData = plainCustomData(g.CustomData)
	return fields
}

func metaFields(meta snapshot.Meta) snapshot.Meta {
	meta.Generator = ""
	for _, stamp := range []*time.Time{
		&meta.SettingsChanged, &meta.DatabaseNameChanged, &meta.DatabaseDescriptionChanged, &meta.DefaultUserNameChanged,
		&meta.MasterKeyChanged, &meta.RecycleBinChanged, &meta.EntryTemplatesGroupChanged,
	} {
		*stamp = time.Time{}
	}
	meta.LastSelectedGroup, meta.LastTopVisibleGroup = snapshot.UUID{}, snapshot.UUID{}
	meta.CustomData = plainCustomData(meta.CustomData)
	meta.CustomIcons = plainIcons(meta.CustomIcons)
	return meta
}

// plainCustomData leaves the items a person or a plugin has set, sorted, without their time.
func plainCustomData(items []snapshot.CustomDataItem) []snapshot.CustomDataItem {
	var result []snapshot.CustomDataItem
	for i := range items {
		if !items[i].IsService() {
			result = append(result, snapshot.CustomDataItem{Key: items[i].Key, Value: items[i].Value})
		}
	}
	slices.SortFunc(result, func(a, b snapshot.CustomDataItem) int {
		return cmp.Or(strings.Compare(a.Key, b.Key), strings.Compare(a.Value, b.Value))
	})
	return result
}

func plainIcons(icons []snapshot.CustomIcon) []snapshot.CustomIcon {
	var result []snapshot.CustomIcon
	for _, icon := range icons {
		icon.LastModification = time.Time{}
		result = append(result, icon)
	}
	slices.SortFunc(result, func(a, b snapshot.CustomIcon) int { return strings.Compare(a.UUID.String(), b.UUID.String()) })
	return result
}

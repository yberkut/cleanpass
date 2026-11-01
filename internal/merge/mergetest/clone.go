package mergetest

import (
	"maps"
	"slices"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// Clone gives a deep copy of a snapshot, so that a change of one fork does not reach
// another one, and a test can prove that a merge left its input alone.
func Clone(s *snapshot.Snapshot) *snapshot.Snapshot {
	result := *s
	result.Meta.CustomIcons = slices.Clone(s.Meta.CustomIcons)
	result.Meta.CustomData = slices.Clone(s.Meta.CustomData)
	result.Root = cloneGroup(&s.Root)
	result.Deletions = slices.Clone(s.Deletions)
	result.Attachments = maps.Clone(s.Attachments)
	return &result
}

func cloneGroup(g *snapshot.Group) snapshot.Group {
	result := *g
	result.EnableAutoType = clonePointer(g.EnableAutoType)
	result.EnableSearching = clonePointer(g.EnableSearching)
	result.PreviousParentGroup = clonePointer(g.PreviousParentGroup)
	result.CustomData = slices.Clone(g.CustomData)
	result.Entries = cloneSlice(g.Entries, cloneEntry)
	result.Groups = cloneSlice(g.Groups, cloneGroup)
	return result
}

func cloneEntry(e *snapshot.Entry) snapshot.Entry {
	result := *e
	result.EntryVersion = cloneVersion(&e.EntryVersion)
	result.History = cloneSlice(e.History, cloneVersion)
	return result
}

func cloneVersion(v *snapshot.EntryVersion) snapshot.EntryVersion {
	result := *v
	result.QualityCheck = clonePointer(v.QualityCheck)
	result.PreviousParentGroup = clonePointer(v.PreviousParentGroup)
	result.Fields = slices.Clone(v.Fields)
	result.Attachments = slices.Clone(v.Attachments)
	result.AutoType.Associations = slices.Clone(v.AutoType.Associations)
	result.CustomData = slices.Clone(v.CustomData)
	return result
}

func cloneSlice[T any](items []T, clone func(*T) T) []T {
	if items == nil {
		return nil
	}
	result := make([]T, len(items))
	for i := range items {
		result[i] = clone(&items[i])
	}
	return result
}

func clonePointer[T any](p *T) *T {
	if p == nil {
		return nil
	}
	value := *p
	return &value
}

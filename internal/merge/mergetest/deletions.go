package mergetest

import (
	"slices"
	"time"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The moves of deletions. All copies of a scenario share one recycle bin UUID, also when each
// of them makes the group by itself. Two different bins need the rule of one bin in the result,
// so a move that makes them comes with that rule.
func init() {
	register("delete entry", deleteEntry)
	register("delete entry in two copies", deleteEntryInTwoCopies)
	register("move entry to the bin", recycleEntry)
	register("move group to the bin", recycleGroup)
	register("delete group", deleteGroup)
	register("empty the bin", emptyBin)
}

// binID gives the UUID of the recycle bin of this scenario.
func (w *world) binID() snapshot.UUID {
	if w.bin.IsZero() {
		w.bin = w.newUUID()
	}
	return w.bin
}

// makeBin adds the recycle bin to a copy unless it has one, as a first deletion does.
func (w *world) makeBin(s *snapshot.Snapshot, stamp time.Time) {
	id := w.binID()
	if s.Meta.RecycleBin == id {
		return
	}
	s.Meta.RecycleBinEnabled, s.Meta.RecycleBin, s.Meta.RecycleBinChanged = true, id, stamp
	s.Root.Groups = append(s.Root.Groups, snapshot.Group{
		UUID:  id,
		Name:  "Recycle Bin",
		Times: snapshot.Times{Creation: stamp, LastModification: stamp, LocationChanged: stamp},
	})
}

// find gives the group with this UUID. It must exist.
func (f *fork) find(id snapshot.UUID) *snapshot.Group {
	for _, g := range f.groups() {
		if g.UUID == id {
			return g
		}
	}
	panic("mergetest: no such group")
}

// binned lists the groups in the bin of the copy, the bin included.
func (f *fork) binned() map[snapshot.UUID]bool {
	result := map[snapshot.UUID]bool{}
	if f.snapshot.Meta.RecycleBin.IsZero() {
		return result
	}
	f.find(f.snapshot.Meta.RecycleBin).Walk(func(g *snapshot.Group) { result[g.UUID] = true })
	return result
}

// record adds a record about a deleted object.
func (f *fork) record(id snapshot.UUID, stamp time.Time) {
	f.snapshot.Deletions = append(f.snapshot.Deletions, snapshot.Deletion{UUID: id, Time: stamp})
}

// recordAll adds the records for a group that goes away, with all it holds, as keepassxc-cli
// does.
func (f *fork) recordAll(g *snapshot.Group, stamp time.Time) {
	g.Walk(func(child *snapshot.Group) {
		f.record(child.UUID, stamp)
		for i := range child.Entries {
			f.record(child.Entries[i].UUID, stamp)
		}
	})
}

// groupRef points to a group through its parent.
type groupRef struct {
	parent *snapshot.Group
	index  int
}

func (r groupRef) group() *snapshot.Group { return &r.parent.Groups[r.index] }

// remove takes the group out of its parent. It moves the groups after it, so earlier pointers
// to them no longer hold.
func (r groupRef) remove() snapshot.Group {
	group := *r.group()
	r.parent.Groups = slices.Delete(r.parent.Groups, r.index, r.index+1)
	return group
}

// pickGroup draws a group other than the root and the bin itself, if the copy has one.
func (f *fork) pickGroup(t *rapid.T, allowed func(snapshot.UUID) bool) (groupRef, bool) {
	var refs []groupRef
	for _, parent := range f.groups() {
		for i := range parent.Groups {
			if id := parent.Groups[i].UUID; id != f.snapshot.Meta.RecycleBin && allowed(id) {
				refs = append(refs, groupRef{parent: parent, index: i})
			}
		}
	}
	if len(refs) == 0 {
		return groupRef{}, false
	}
	return refs[rapid.IntRange(0, len(refs)-1).Draw(t, "group")], true
}

// deleteEntry removes an entry for good: the bin is off, or the entry is already in the bin.
func deleteEntry(t *rapid.T, _ *world, f *fork) {
	if ref, ok := f.pickEntry(t); ok {
		f.record(ref.entry().UUID, f.tick(t))
		ref.group.Entries = slices.Delete(ref.group.Entries, ref.index, ref.index+1)
	}
}

// deleteEntryInTwoCopies makes two copies remove the same entry at different times, because
// deletions of a random entry rarely meet.
func deleteEntryInTwoCopies(t *rapid.T, w *world, f *fork) {
	other := rapid.SampledFrom(w.forks).Draw(t, "other copy")
	ref, ok := f.pickEntry(t)
	if !ok || other == f {
		return
	}
	id := ref.entry().UUID
	for _, candidate := range other.entries() {
		if candidate.entry().UUID == id {
			candidate.group.Entries = slices.Delete(candidate.group.Entries, candidate.index, candidate.index+1)
			other.record(id, other.tick(t))
			f.record(id, f.tick(t))
			ref.group.Entries = slices.Delete(ref.group.Entries, ref.index, ref.index+1)
			return
		}
	}
}

// recycleEntry moves an entry to the bin. keepassxc-cli 2.7.12 also gives it a new time of the
// last change, with the same content and no item in the history, so a copy of the entry gets a
// version only by that time. The spec expects the time to stay, and the desktop program has not
// been tried, so the move does either.
func recycleEntry(t *rapid.T, w *world, f *fork) {
	w.makeBin(f.snapshot, f.clock)
	bin := f.find(w.binID())
	ref, ok := f.pickEntry(t)
	if !ok || ref.group == bin {
		return
	}
	moved := *ref.entry()
	previous := ref.group.UUID
	stamp := f.tick(t)
	moved.PreviousParentGroup = &previous
	moved.Times.LocationChanged = stamp
	if rapid.Bool().Draw(t, "the move renews the time") {
		moved.Times.LastModification, moved.Times.LastAccess = stamp, stamp
	}
	ref.group.Entries = slices.Delete(ref.group.Entries, ref.index, ref.index+1)
	bin.Entries = append(bin.Entries, moved)
}

// recycleGroup moves a group to the bin as keepassxc-cli does: the group gets new times, and its
// entries go with it without any change of their own.
func recycleGroup(t *rapid.T, w *world, f *fork) {
	w.makeBin(f.snapshot, f.clock)
	binned := f.binned()
	ref, ok := f.pickGroup(t, func(id snapshot.UUID) bool { return !binned[id] })
	if !ok {
		return
	}
	previous := ref.parent.UUID
	stamp := f.tick(t)
	moved := ref.remove()
	moved.PreviousParentGroup = &previous
	moved.Times.LastModification, moved.Times.LastAccess, moved.Times.LocationChanged = stamp, stamp, stamp
	bin := f.find(w.binID())
	bin.Groups = append(bin.Groups, moved)
}

// deleteGroup removes a group for good, with all it holds.
func deleteGroup(t *rapid.T, _ *world, f *fork) {
	ref, ok := f.pickGroup(t, func(snapshot.UUID) bool { return true })
	if !ok {
		return
	}
	gone := ref.remove()
	f.recordAll(&gone, f.tick(t))
}

// emptyBin removes all the bin holds. The bin itself stays.
func emptyBin(t *rapid.T, _ *world, f *fork) {
	if f.snapshot.Meta.RecycleBin.IsZero() {
		return
	}
	stamp := f.tick(t)
	bin := f.find(f.snapshot.Meta.RecycleBin)
	for i := range bin.Entries {
		f.record(bin.Entries[i].UUID, stamp)
	}
	for i := range bin.Groups {
		f.recordAll(&bin.Groups[i], stamp)
	}
	bin.Entries, bin.Groups = nil, nil
}

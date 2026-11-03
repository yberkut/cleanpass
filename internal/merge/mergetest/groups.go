package mergetest

import (
	"time"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The moves of groups.
func init() {
	register("add group", addGroup)
	register("move group", moveGroup)
	register("move two groups under each other", moveUnderEachOther)
	register("edit group", editGroup)
}

// addGroup adds a group only this copy has. Few names exist, so it often stands on the path of
// another group, in this copy or in another one.
func addGroup(t *rapid.T, w *world, f *fork) {
	stamp := f.tick(t)
	target := rapid.SampledFrom(f.groups()).Draw(t, "target group")
	target.Groups = append(target.Groups, snapshot.Group{
		UUID:  w.newUUID(),
		Name:  rapid.SampledFrom(groupNames).Draw(t, "group name"),
		Times: snapshot.Times{Creation: stamp, LastModification: stamp, LastAccess: stamp, LocationChanged: stamp},
	})
}

// below says the group other lies in the group g, or is g.
func below(g *snapshot.Group, other snapshot.UUID) bool {
	found := false
	g.Walk(func(child *snapshot.Group) { found = found || child.UUID == other })
	return found
}

// moveInto moves a group as KeePassXC does: only its place changes.
func (f *fork) moveInto(ref groupRef, target snapshot.UUID, stamp time.Time) {
	previous := ref.parent.UUID
	moved := ref.remove()
	moved.PreviousParentGroup = &previous
	moved.Times.LocationChanged = stamp
	into := f.find(target)
	into.Groups = append(into.Groups, moved)
}

// moveGroup moves a group other than the bin to any group that is not inside it, the bin and
// the groups in it too.
func moveGroup(t *rapid.T, _ *world, f *fork) {
	ref, ok := f.pickGroup(t, func(snapshot.UUID) bool { return true })
	if !ok {
		return
	}
	var targets []snapshot.UUID
	for _, g := range f.groups() {
		if g.UUID != ref.parent.UUID && !below(ref.group(), g.UUID) {
			targets = append(targets, g.UUID)
		}
	}
	if len(targets) > 0 {
		f.moveInto(ref, rapid.SampledFrom(targets).Draw(t, "target group"), f.tick(t))
	}
}

// refTo finds the group with this UUID through its parent.
func (f *fork) refTo(id snapshot.UUID) (groupRef, bool) {
	for _, parent := range f.groups() {
		for i := range parent.Groups {
			if parent.Groups[i].UUID == id {
				return groupRef{parent: parent, index: i}, true
			}
		}
	}
	return groupRef{}, false
}

// moveUnderEachOther makes the moves of a cycle: this copy moves one group under a second one,
// and another copy moves the second one under the first one. Random moves rarely meet.
func moveUnderEachOther(t *rapid.T, w *world, f *fork) {
	other := rapid.SampledFrom(w.forks).Draw(t, "other copy")
	first, ok := f.pickGroup(t, func(snapshot.UUID) bool { return true })
	if !ok || other == f {
		return
	}
	firstID := first.group().UUID
	second, ok := f.pickGroup(t, func(id snapshot.UUID) bool { return id != firstID && id != other.snapshot.Meta.RecycleBin })
	if !ok {
		return
	}
	secondID := second.group().UUID
	firstThere, inOther := other.refTo(firstID)
	secondThere, alsoInOther := other.refTo(secondID)
	if !inOther || !alsoInOther || firstID == other.snapshot.Meta.RecycleBin {
		return
	}
	if !below(first.group(), secondID) {
		f.moveInto(first, secondID, f.tick(t))
	}
	if !below(secondThere.group(), firstID) {
		other.moveInto(secondThere, firstThere.group().UUID, other.tick(t))
	}
}

// editGroup changes what a person sets on a group, and with that its time.
func editGroup(t *rapid.T, _ *world, f *fork) {
	g := rapid.SampledFrom(f.groups()).Draw(t, "group")
	g.Times.LastModification = f.tick(t)
	if rapid.Bool().Draw(t, "rename the group") {
		g.Name = rapid.SampledFrom(groupNames).Draw(t, "group name")
	} else {
		g.Notes = drawText(t)
	}
}

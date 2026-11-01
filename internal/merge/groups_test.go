package merge_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

var (
	groupA = snapshot.UUID{15: 0xA1}
	groupB = snapshot.UUID{15: 0xA2}
	groupC = snapshot.UUID{15: 0xA3}
)

// node is a group for a test tree.
func node(id snapshot.UUID, name string, located float64, children ...snapshot.Group) snapshot.Group {
	return snapshot.Group{UUID: id, Name: name, Times: snapshot.Times{LocationChanged: hours(located)}, Groups: children}
}

// tree is a copy with these groups under the root.
func tree(groups ...snapshot.Group) *snapshot.Snapshot {
	return &snapshot.Snapshot{Root: snapshot.Group{UUID: rootID, Name: "Root", Groups: groups}}
}

// shape prints where each group lies: a line for each group with the path of its UUIDs.
func shape(s *snapshot.Snapshot) []string {
	var lines []string
	var walk func(g *snapshot.Group, path string)
	walk = func(g *snapshot.Group, path string) {
		path += "/" + g.UUID.String()[30:]
		lines = append(lines, path)
		for i := range g.Groups {
			walk(&g.Groups[i], path)
		}
	}
	for i := range s.Root.Groups {
		walk(&s.Root.Groups[i], "")
	}
	slices.Sort(lines)
	return lines
}

func wantShape(t *testing.T, s *snapshot.Snapshot, want ...string) {
	t.Helper()
	if got := shape(s); !slices.Equal(got, want) {
		t.Errorf("the tree is %q, want %q", got, want)
	}
}

// Two copies move two groups under each other. The newest move holds, as it does for any group,
// so the oldest move in the cycle gives way and that group goes under the root.
func TestACycleBreaksAtTheOldestMoveInAnyOrder(t *testing.T) {
	first := tree(node(groupA, "a", 1, node(groupB, "b", 5)))
	second := tree(node(groupB, "b", 1, node(groupA, "a", 9)))

	inAnyOrder(t, []*snapshot.Snapshot{first, second}, func(t *testing.T, merged *snapshot.Snapshot) {
		// A moved at 9 and B moved at 5, so B gives way, and A stays under it.
		wantShape(t, merged, "/a2", "/a2/a1")
	})
}

func TestACycleOfTwoMovesAtTheSameTimeBreaksAtTheSmallestUUID(t *testing.T) {
	first := tree(node(groupA, "a", 1, node(groupB, "b", 5)))
	second := tree(node(groupB, "b", 1, node(groupA, "a", 5)))

	inAnyOrder(t, []*snapshot.Snapshot{first, second}, func(t *testing.T, merged *snapshot.Snapshot) {
		wantShape(t, merged, "/a1", "/a1/a2")
	})
}

func TestACycleOfThreeCopiesBreaksInOnePlace(t *testing.T) {
	one := tree(node(groupA, "a", 1, node(groupB, "b", 5)), node(groupC, "c", 1))
	two := tree(node(groupB, "b", 1, node(groupC, "c", 7)), node(groupA, "a", 1))
	three := tree(node(groupC, "c", 1, node(groupA, "a", 9)), node(groupB, "b", 1))

	inAnyOrder(t, []*snapshot.Snapshot{one, two, three}, func(t *testing.T, merged *snapshot.Snapshot) {
		wantShape(t, merged, "/a2", "/a2/a3", "/a2/a3/a1")
	})
}

// A group below a cycle is no part of it, whatever its UUID: it stays with its parent.
func TestAGroupBelowACycleStaysWithItsParent(t *testing.T) {
	below := snapshot.UUID{15: 0x10}
	first := tree(node(groupA, "a", 1, node(groupB, "b", 5, node(below, "below", 2))))
	second := tree(node(groupB, "b", 1, node(below, "below", 2), node(groupA, "a", 9)))

	inAnyOrder(t, []*snapshot.Snapshot{first, second}, func(t *testing.T, merged *snapshot.Snapshot) {
		wantShape(t, merged, "/a2", "/a2/10", "/a2/a1")
	})
}

func TestACycleKeepsAllEntriesAndGoesInTheReport(t *testing.T) {
	a := node(groupA, "a", 1, node(groupB, "b", 5))
	a.Entries = []snapshot.Entry{entry(version(hours(1), "x"))}
	second := tree(node(groupB, "b", 1, node(groupA, "a", 9)))

	result := mergeOf(t, tree(a), second)

	if got := result.Merged.Stats(); got.Entries != 1 || got.Groups != 3 {
		t.Errorf("stats = %+v", got)
	}
	var broken []merge.GroupCycleBroken
	for _, event := range result.Report {
		if b, ok := event.(merge.GroupCycleBroken); ok {
			broken = append(broken, b)
		}
	}
	if want := []merge.GroupCycleBroken{{Group: groupB, Name: "b"}}; !reflect.DeepEqual(broken, want) {
		t.Errorf("events = %+v, want %+v", broken, want)
	}
}

// A second merge with a copy that holds the old place has the same cycle. The answer holds.
func TestABrokenCycleStaysBrokenInTheNextMerge(t *testing.T) {
	first := tree(node(groupA, "a", 1, node(groupB, "b", 5)))
	second := tree(node(groupB, "b", 1, node(groupA, "a", 9)))
	merged := mergeOf(t, first, second).Merged

	for _, old := range []*snapshot.Snapshot{first, second} {
		wantSameTree(t, mergeOf(t, merged, old).Merged, merged)
	}
}

func wantSameTree(t *testing.T, got, want *snapshot.Snapshot) {
	t.Helper()
	if !reflect.DeepEqual(got.Root, want.Root) {
		t.Errorf("the tree differs:\n got %s\nwant %s", printed(got.Root), printed(want.Root))
	}
}

func TestGroupsWithTheSamePathJoinUnderTheSmallestUUID(t *testing.T) {
	first := node(groupB, "Work", 1)
	first.Entries = []snapshot.Entry{entry(version(hours(1), "x"))}
	second := node(groupA, "Work", 1)
	second.Entries = []snapshot.Entry{{UUID: otherID, EntryVersion: version(hours(1), "y")}}
	second.Entries[0].EntryVersion.Fields[0].Value = "Other"

	for _, order := range [][]*snapshot.Snapshot{{tree(first), tree(second)}, {tree(second), tree(first)}} {
		result := mergeOf(t, order...)

		wantNoQuestions(t, result)
		wantShape(t, result.Merged, "/a1")
		if got := len(result.Merged.Root.Groups[0].Entries); got != 2 {
			t.Errorf("the group has %d entries, want both", got)
		}
		if want := map[snapshot.UUID]snapshot.UUID{groupB: groupA}; !reflect.DeepEqual(result.MergedGroups, want) {
			t.Errorf("merged groups = %v, want %v", result.MergedGroups, want)
		}
	}
}

func TestTheSamePathJoinsAllTheWayDown(t *testing.T) {
	deepA, deepB := snapshot.UUID{15: 0xD1}, snapshot.UUID{15: 0xD2}
	first := tree(node(groupA, "Work", 1, node(deepA, "Keys", 1, node(snapshot.UUID{15: 0xE1}, "Old", 1))))
	second := tree(node(groupB, "Work", 1, node(deepB, "Keys", 1, node(snapshot.UUID{15: 0xE2}, "Old", 1))))

	result := mergeOf(t, first, second)

	wantShape(t, result.Merged, "/a1", "/a1/d1", "/a1/d1/e1")
	want := map[snapshot.UUID]snapshot.UUID{groupB: groupA, deepB: deepA, {15: 0xE2}: {15: 0xE1}}
	if !reflect.DeepEqual(result.MergedGroups, want) {
		t.Errorf("merged groups = %v, want %v", result.MergedGroups, want)
	}
}

func TestThreeGroupsOnOnePathGiveOneGroup(t *testing.T) {
	result := mergeOf(t, tree(node(groupC, "Work", 1)), tree(node(groupB, "Work", 1)), tree(node(groupA, "Work", 1)))

	wantShape(t, result.Merged, "/a1")
	want := map[snapshot.UUID]snapshot.UUID{groupB: groupA, groupC: groupA}
	if !reflect.DeepEqual(result.MergedGroups, want) {
		t.Errorf("merged groups = %v, want %v", result.MergedGroups, want)
	}
}

// Nothing says two groups in one copy are not one, and the spec has no exception.
func TestTwoGroupsWithOnePathInOneCopyJoinToo(t *testing.T) {
	wantShape(t, mergeOf(t, tree(node(groupB, "Work", 1), node(groupA, "Work", 1))).Merged, "/a1")
}

func TestDifferentPathsDoNotJoin(t *testing.T) {
	for name, s := range map[string]*snapshot.Snapshot{
		"another name":       tree(node(groupA, "Work", 1), node(groupB, "work", 1)),
		"under other groups": tree(node(groupA, "One", 1, node(snapshot.UUID{15: 0xD1}, "Keys", 1)), node(groupB, "Two", 1, node(snapshot.UUID{15: 0xD2}, "Keys", 1))),
	} {
		t.Run(name, func(t *testing.T) {
			if got := mergeOf(t, s).MergedGroups; len(got) != 0 {
				t.Errorf("merged groups = %v, want none", got)
			}
		})
	}
}

// The path comes from the newest name and the newest place, so it is the one a person sees in
// the result.
func TestThePathComesFromTheNewestVersionOfTheGroup(t *testing.T) {
	renamed := node(groupA, "Job", 1)
	renamed.Times.LastModification = hours(9)
	old := node(groupA, "Work", 1)
	old.Times.LastModification = hours(1)
	another := node(groupB, "Work", 1)

	result := mergeOf(t, tree(renamed), tree(old, another))

	if len(result.MergedGroups) != 0 {
		t.Errorf("merged groups = %v: A has the name Job by now", result.MergedGroups)
	}
}

func TestAMoveBringsTwoGroupsOnOnePath(t *testing.T) {
	result := mergeOf(t, tree(node(groupA, "One", 1, node(groupB, "Work", 9)), node(groupC, "Work", 1)), tree(node(groupC, "Work", 1), node(groupB, "Work", 1), node(groupA, "One", 1)))

	if len(result.MergedGroups) != 0 {
		t.Errorf("merged groups = %v: B lies in One by now", result.MergedGroups)
	}
	result = mergeOf(t, tree(node(groupC, "One", 1, node(groupB, "Work", 9)), node(groupA, "Work", 1)), tree(node(groupC, "One", 1, node(groupA, "Work", 9))))

	if want := map[snapshot.UUID]snapshot.UUID{groupB: groupA}; !reflect.DeepEqual(result.MergedGroups, want) {
		t.Errorf("merged groups = %v, want %v", result.MergedGroups, want)
	}
}

func TestAJoinedGroupHasTheFieldsOfItsNewestVersion(t *testing.T) {
	newest := node(groupB, "Work", 1)
	newest.Notes = "newest"
	newest.Times.LastModification = hours(9)
	oldest := node(groupA, "Work", 1)
	oldest.Notes = "oldest"
	oldest.Times.LastModification = hours(1)

	for _, order := range [][]*snapshot.Snapshot{{tree(newest), tree(oldest)}, {tree(oldest), tree(newest)}} {
		got := mergeOf(t, order...).Merged.Root.Groups[0]

		if got.UUID != groupA || got.Notes != "newest" {
			t.Errorf("group %s has notes %q", got.UUID, got.Notes)
		}
	}
}

// The groups the settings name are still there under their new UUID.
func TestTheSettingsFollowAJoinedGroup(t *testing.T) {
	first := tree(node(groupB, "Work", 1))
	first.Meta.EntryTemplatesGroup, first.Meta.EntryTemplatesGroupChanged = groupB, hours(1)
	first.Meta.LastSelectedGroup, first.Meta.LastTopVisibleGroup = groupB, groupB

	meta := mergeOf(t, first, tree(node(groupA, "Work", 1))).Merged.Meta

	if meta.EntryTemplatesGroup != groupA || meta.LastSelectedGroup != groupA || meta.LastTopVisibleGroup != groupA {
		t.Errorf("settings point at %s, %s and %s, want %s", meta.EntryTemplatesGroup, meta.LastSelectedGroup, meta.LastTopVisibleGroup, groupA)
	}
}

func TestAJoinGoesInTheReport(t *testing.T) {
	result := mergeOf(t, tree(node(groupB, "Work", 1)), tree(node(groupA, "Work", 1)))

	want := merge.GroupsJoined{Into: groupA, Name: "Work", Joined: []snapshot.UUID{groupB}}
	if !slices.ContainsFunc(result.Report, func(e merge.Event) bool { return reflect.DeepEqual(e, want) }) {
		t.Errorf("report = %s, want %+v", printed(result.Report), want)
	}
}

// binTree is a copy with this group as its recycle bin.
func binTree(bin snapshot.Group, others ...snapshot.Group) *snapshot.Snapshot {
	s := tree(append([]snapshot.Group{bin}, others...)...)
	s.Meta.RecycleBin, s.Meta.RecycleBinChanged = bin.UUID, hours(1)
	return s
}

func TestTwoBinsJoinUnderTheSmallestUUIDWhateverTheirPlaces(t *testing.T) {
	inFirst := node(groupB, "Кошик", 1)
	inFirst.Entries = []snapshot.Entry{entry(version(hours(1), "x"))}
	first := binTree(inFirst)
	second := binTree(node(groupA, "Recycle Bin", 1))

	for _, order := range [][]*snapshot.Snapshot{{first, second}, {second, first}} {
		result := mergeOf(t, order...)

		wantNoQuestions(t, result)
		wantShape(t, result.Merged, "/a1")
		if got := result.Merged.Meta.RecycleBin; got != groupA {
			t.Errorf("the bin is %s, want %s", got, groupA)
		}
		if got := groupOfOnlyEntry(t, result.Merged); got != groupA {
			t.Errorf("the entry lies in %s, want the bin", got)
		}
		if got := result.MergedGroups[groupB]; got != groupA {
			t.Errorf("the second bin went into %s, want %s", got, groupA)
		}
	}
}

// A person can name a group as the bin of another copy. What lies in it is alive, and a join
// would make it deleted.
func TestAPlainGroupNamedAsTheBinStaysAPlainGroup(t *testing.T) {
	plain := node(groupA, "Bin", 1)
	plain.Entries = []snapshot.Entry{entry(version(hours(1), "x"))}

	result := mergeOf(t, tree(plain), binTree(node(groupB, "Bin", 1)))

	wantNoQuestions(t, result)
	wantShape(t, result.Merged, "/a1", "/a2")
	if got := groupOfOnlyEntry(t, result.Merged); got != groupA {
		t.Errorf("the entry lies in %s, want the plain group", got)
	}
}

// Under a joined bin, a group moved to the bin in another copy joins by its path too.
func TestGroupsWithOnePathInTheBinJoin(t *testing.T) {
	first := binTree(node(groupA, "Bin", 1, node(snapshot.UUID{15: 0xD1}, "Old", 1)))
	second := binTree(node(groupB, "Bin", 1, node(snapshot.UUID{15: 0xD2}, "Old", 1)))

	wantShape(t, mergeOf(t, first, second).Merged, "/a1", "/a1/d1")
}

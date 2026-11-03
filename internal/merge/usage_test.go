package merge_test

import (
	"reflect"
	"testing"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// Reading an entry changes these values, but not its version. Copies that only read an
// entry hold the same version with other values.
func TestLastAccessAndUsageCountAreTheMaximum(t *testing.T) {
	read := func(access float64, count int64) snapshot.Entry {
		v := version(hours(1), "same")
		v.Times.LastAccess = hours(access)
		v.Times.UsageCount = count
		return entry(v)
	}

	result := mergeOf(t, database(read(5, 1)), database(read(3, 7)))

	merged := onlyEntry(t, result.Merged)
	wantNoQuestions(t, result)
	if len(merged.History) != 0 {
		t.Errorf("reading made %d more versions", len(merged.History))
	}
	if !merged.Times.LastAccess.Equal(hours(5)) || merged.Times.UsageCount != 7 {
		t.Errorf("last access = %s, usage count = %d, want %s and 7", merged.Times.LastAccess, merged.Times.UsageCount, hours(5))
	}
}

func TestTheMainVersionHasTheMaximumOverAllVersionsOfAllCopies(t *testing.T) {
	old := version(hours(1), "old")
	old.Times.UsageCount = 50
	old.Times.LastAccess = hours(40)
	newer := version(hours(2), "new")
	newer.Times.UsageCount = 2
	newer.Times.LastAccess = hours(3)

	merged := onlyEntry(t, mergeOf(t, database(entry(newer)), database(entry(old))).Merged)

	if merged.Times.UsageCount != 50 || !merged.Times.LastAccess.Equal(hours(40)) {
		t.Errorf("usage count = %d, last access = %s, want 50 and %s", merged.Times.UsageCount, merged.Times.LastAccess, hours(40))
	}
}

// A plain history already grows, so the merge does not touch its values.
func TestUsageInAPlainHistoryStaysAsItIs(t *testing.T) {
	old := version(hours(1), "old")
	old.Times.UsageCount = 3
	old.Times.LastAccess = hours(2)
	newer := version(hours(5), "new")
	newer.Times.UsageCount = 4
	newer.Times.LastAccess = hours(6)
	want := entry(newer, old)

	got := onlyEntry(t, mergeOf(t, database(want)).Merged)

	if !reflect.DeepEqual(*got, want) {
		t.Errorf("entry = %+v, want %+v", *got, want)
	}
}

var (
	homeID = snapshot.UUID{15: 0xA1}
	workID = snapshot.UUID{15: 0xA2}
)

// databaseWithGroups is a copy with two groups and one entry in the given group.
func databaseWithGroups(group snapshot.UUID, e snapshot.Entry) *snapshot.Snapshot {
	home := snapshot.Group{UUID: homeID, Name: "Home"}
	work := snapshot.Group{UUID: workID, Name: "Work"}
	if group == homeID {
		home.Entries = []snapshot.Entry{e}
	} else {
		work.Entries = []snapshot.Entry{e}
	}
	return &snapshot.Snapshot{Root: snapshot.Group{UUID: rootID, Name: "Root", Groups: []snapshot.Group{home, work}}}
}

func groupOfOnlyEntry(t *testing.T, s *snapshot.Snapshot) snapshot.UUID {
	t.Helper()
	onlyEntry(t, s)
	var group snapshot.UUID
	s.Root.Walk(func(g *snapshot.Group) {
		if len(g.Entries) == 1 {
			group = g.UUID
		}
	})
	return group
}

func movedEntry(located float64, previous *snapshot.UUID) snapshot.Entry {
	v := version(hours(1), "same")
	v.Times.LocationChanged = hours(located)
	v.PreviousParentGroup = previous
	return entry(v)
}

func TestTheNewestLocationChangedDecidesTheGroup(t *testing.T) {
	inHome := databaseWithGroups(homeID, movedEntry(5, nil))
	inWork := databaseWithGroups(workID, movedEntry(9, &homeID))

	for name, snapshots := range map[string][]*snapshot.Snapshot{
		"moved copy last":  {inHome, inWork},
		"moved copy first": {inWork, inHome},
	} {
		t.Run(name, func(t *testing.T) {
			result := mergeOf(t, snapshots...)

			wantNoQuestions(t, result)
			merged := result.Merged
			if got := groupOfOnlyEntry(t, merged); got != workID {
				t.Errorf("the entry lies in %s, want %s", got, workID)
			}
			moved := onlyEntry(t, merged)
			if !moved.Times.LocationChanged.Equal(hours(9)) {
				t.Errorf("location changed = %s, want %s", moved.Times.LocationChanged, hours(9))
			}
			if moved.PreviousParentGroup == nil || *moved.PreviousParentGroup != homeID {
				t.Errorf("previous parent = %v, want %s", moved.PreviousParentGroup, homeID)
			}
		})
	}
}

func TestLocationDoesNotFollowTheNewestContent(t *testing.T) {
	moved := version(hours(1), "old content")
	moved.Times.LocationChanged = hours(9)
	edited := version(hours(5), "new content")
	edited.Times.LocationChanged = hours(2)

	merged := mergeOf(t, databaseWithGroups(workID, entry(moved)), databaseWithGroups(homeID, entry(edited, version(hours(1), "old content")))).Merged

	if got := groupOfOnlyEntry(t, merged); got != workID {
		t.Errorf("the entry lies in %s, want %s", got, workID)
	}
	wantVersions(t, onlyEntry(t, merged), "new content", "old content")
}

// The tie needs a rule too, or the result would depend on the order of the copies.
func TestTheSameLocationChangedInTwoGroupsGivesTheSameGroupInAnyOrder(t *testing.T) {
	inHome := databaseWithGroups(homeID, movedEntry(5, nil))
	inWork := databaseWithGroups(workID, movedEntry(5, nil))

	forward := groupOfOnlyEntry(t, mergeOf(t, inHome, inWork).Merged)
	backward := groupOfOnlyEntry(t, mergeOf(t, inWork, inHome).Merged)

	if forward != backward {
		t.Errorf("the group depends on the order of the copies: %s and %s", forward, backward)
	}
}

func TestEveryEntryOfEveryCopyIsInTheTree(t *testing.T) {
	second := snapshot.UUID{15: 0x02}
	first := databaseWithGroups(homeID, entry(version(hours(1), "a")))
	other := databaseWithGroups(workID, snapshot.Entry{UUID: second, EntryVersion: version(hours(1), "b")})

	merged := mergeOf(t, first, other).Merged

	if stats := merged.Stats(); stats.Entries != 2 || stats.Groups != 3 {
		t.Errorf("stats = %+v, want 2 entries and 3 groups", stats)
	}
}

func TestCopiesWithDifferentRootsGiveOneRootWithAllEntries(t *testing.T) {
	second := snapshot.UUID{15: 0x02}
	first := database(entry(version(hours(1), "a")))
	other := database(snapshot.Entry{UUID: second, EntryVersion: version(hours(1), "b")})
	other.Root.UUID = snapshot.UUID{15: 0x77} // smaller than rootID

	for _, order := range [][]*snapshot.Snapshot{{first, other}, {other, first}} {
		merged := mergeOf(t, order...).Merged

		if merged.Root.UUID != other.Root.UUID {
			t.Errorf("root = %s, want the smallest %s", merged.Root.UUID, other.Root.UUID)
		}
		if stats := merged.Stats(); stats.Entries != 2 || stats.Groups != 1 {
			t.Errorf("stats = %+v, want 2 entries and 1 group", stats)
		}
	}
}

// The same LocationChanged on the same group still has a value to pick for the previous
// parent, and the pick must not depend on the order of the copies.
func TestTheSameMoveWithAndWithoutAPreviousParentGivesTheSameResultInAnyOrder(t *testing.T) {
	without := database(movedEntry(5, nil))
	with := database(movedEntry(5, &homeID))

	forward := onlyEntry(t, mergeOf(t, without, with).Merged)
	backward := onlyEntry(t, mergeOf(t, with, without).Merged)

	for name, merged := range map[string]*snapshot.Entry{"forward": forward, "backward": backward} {
		if merged.PreviousParentGroup == nil || *merged.PreviousParentGroup != homeID {
			t.Errorf("%s: previous parent = %v, want %s", name, merged.PreviousParentGroup, homeID)
		}
	}
}

func TestAGroupHasTheFieldsOfItsNewestVersion(t *testing.T) {
	versionOfGroup := func(name string, modified float64) *snapshot.Snapshot {
		return &snapshot.Snapshot{Root: snapshot.Group{UUID: rootID, Name: "Root", Groups: []snapshot.Group{
			{UUID: homeID, Name: name, Times: snapshot.Times{LastModification: hours(modified)}},
		}}}
	}
	older, newer := versionOfGroup("older", 1), versionOfGroup("newer", 2)

	for _, order := range [][]*snapshot.Snapshot{{older, newer}, {newer, older}} {
		if got := mergeOf(t, order...).Merged.Root.Groups[0].Name; got != "newer" {
			t.Errorf("the group has the name %q, want %q", got, "newer")
		}
	}
}

func TestAGroupLiesWhereItsNewestMoveSaysAndKeepsTheMaximumUsage(t *testing.T) {
	under := func(parent snapshot.UUID, located float64, access float64, count int64) *snapshot.Snapshot {
		moved := snapshot.Group{UUID: mailID, Name: "moved", Times: snapshot.Times{
			LocationChanged: hours(located), LastAccess: hours(access), UsageCount: count,
		}}
		home := snapshot.Group{UUID: homeID, Name: "Home"}
		work := snapshot.Group{UUID: workID, Name: "Work"}
		if parent == homeID {
			home.Groups = []snapshot.Group{moved}
		} else {
			work.Groups = []snapshot.Group{moved}
		}
		return &snapshot.Snapshot{Root: snapshot.Group{UUID: rootID, Name: "Root", Groups: []snapshot.Group{home, work}}}
	}
	first, second := under(homeID, 5, 9, 1), under(workID, 8, 2, 7)

	for _, order := range [][]*snapshot.Snapshot{{first, second}, {second, first}} {
		root := mergeOf(t, order...).Merged.Root
		home, work := root.Groups[0], root.Groups[1]
		if len(home.Groups) != 0 || len(work.Groups) != 1 {
			t.Fatalf("Home has %d groups and Work has %d, want 0 and 1", len(home.Groups), len(work.Groups))
		}
		moved := work.Groups[0]
		if !moved.Times.LocationChanged.Equal(hours(8)) {
			t.Errorf("location changed = %s, want %s", moved.Times.LocationChanged, hours(8))
		}
		if !moved.Times.LastAccess.Equal(hours(9)) || moved.Times.UsageCount != 7 {
			t.Errorf("last access = %s, usage count = %d, want %s and 7", moved.Times.LastAccess, moved.Times.UsageCount, hours(9))
		}
	}
}

// No group may disappear, also when two copies put two groups under each other.
func TestGroupsInACycleStayInTheTree(t *testing.T) {
	a, b := snapshot.UUID{15: 0xA1}, snapshot.UUID{15: 0xA2}
	withChild := func(parent, child snapshot.UUID, located float64) *snapshot.Snapshot {
		inner := snapshot.Group{UUID: child, Name: "inner", Times: snapshot.Times{LocationChanged: hours(located)}}
		outer := snapshot.Group{UUID: parent, Name: "outer", Groups: []snapshot.Group{inner}}
		return &snapshot.Snapshot{Root: snapshot.Group{UUID: rootID, Name: "Root", Groups: []snapshot.Group{outer}}}
	}

	merged := mergeOf(t, withChild(a, b, 5), withChild(b, a, 5)).Merged

	if got := merged.Stats().Groups; got != 3 {
		t.Errorf("the tree has %d groups, want 3", got)
	}
}

func TestAttachmentsOfAllVersionsAreInTheResult(t *testing.T) {
	bytesOld, bytesNew := []byte("old bytes"), []byte("new bytes")
	idOld, idNew := snapshot.NewAttachmentID(bytesOld), snapshot.NewAttachmentID(bytesNew)
	old := version(hours(1), "old")
	old.Attachments = []snapshot.AttachmentRef{{Name: "a.bin", ID: idOld}}
	newer := version(hours(2), "new")
	newer.Attachments = []snapshot.AttachmentRef{{Name: "a.bin", ID: idNew}}
	first := database(entry(old))
	first.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{idOld: {Content: bytesOld, Protected: true}}
	second := database(entry(newer, old))
	second.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{
		idOld: {Content: bytesOld},
		idNew: {Content: bytesNew},
	}

	for _, order := range [][]*snapshot.Snapshot{{first, second}, {second, first}} {
		merged := mergeOf(t, order...).Merged

		want := map[snapshot.AttachmentID]snapshot.Attachment{
			idOld: {Content: bytesOld, Protected: true},
			idNew: {Content: bytesNew},
		}
		if !reflect.DeepEqual(merged.Attachments, want) {
			t.Errorf("attachments = %+v, want %+v", merged.Attachments, want)
		}
	}
}

func TestAttachmentNoEntryRefersToIsNotInTheResult(t *testing.T) {
	alone := database(entry(version(hours(1), "a")))
	alone.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{
		snapshot.NewAttachmentID([]byte("x")): {Content: []byte("x")},
	}

	if got := mergeOf(t, alone).Merged.Attachments; len(got) != 0 {
		t.Errorf("attachments = %+v, want none", got)
	}
}

func TestSettingsComeFromTheCopyWithTheNewestChange(t *testing.T) {
	older := database()
	older.Meta = snapshot.Meta{Color: "older", SettingsChanged: hours(1)}
	newer := database()
	newer.Meta = snapshot.Meta{Color: "newer", SettingsChanged: hours(2)}
	tied := database()
	tied.Meta = snapshot.Meta{Color: "another", SettingsChanged: hours(2)}

	for _, order := range [][]*snapshot.Snapshot{{older, newer, tied}, {tied, newer, older}, {newer, older, tied}} {
		if got := mergeOf(t, order...).Merged.Meta.Color; got != "newer" {
			t.Errorf("color = %q, want the same winner %q in any order", got, "newer")
		}
	}
}

func TestTheMergedCopyHasNoFormatOfItsOwn(t *testing.T) {
	one := database()
	one.Format = snapshot.Format{Major: 4, Minor: 1, Cipher: snapshot.CipherAES256}

	if got := mergeOf(t, one).Merged.Format; got != (snapshot.Format{}) {
		t.Errorf("format = %+v, want empty: the writer decides it", got)
	}
}

// The order in the file must not depend on the order of the copies.
func TestEntriesAndGroupsLieInAFixedOrder(t *testing.T) {
	second := snapshot.UUID{15: 0x02}
	third := snapshot.UUID{15: 0x03}
	titled := func(id snapshot.UUID, title string) snapshot.Entry {
		v := version(hours(1), "x")
		v.Fields[0].Value = title
		return snapshot.Entry{UUID: id, EntryVersion: v}
	}
	first := database(titled(third, "A"), titled(mailID, "B"))
	other := database(titled(second, "A"))
	// Only a plain group and the bin can have the same name: other groups would join.
	first.Meta.RecycleBin = homeID
	first.Root.Groups = []snapshot.Group{{UUID: workID, Name: "Zeta"}, {UUID: homeID, Name: "Zeta"}, {UUID: snapshot.UUID{15: 0xA3}, Name: "Alpha"}}

	result := mustRun(t, merge.Input{Copies: named(first, other), Now: now, Key: testKey})

	var entries []snapshot.UUID
	for i := range result.Merged.Root.Entries {
		entries = append(entries, result.Merged.Root.Entries[i].UUID)
	}
	if want := []snapshot.UUID{second, third, mailID}; !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %v, want by title, then by UUID: %v", entries, want)
	}
	var groups []snapshot.UUID
	for i := range result.Merged.Root.Groups {
		groups = append(groups, result.Merged.Root.Groups[i].UUID)
	}
	if want := []snapshot.UUID{{15: 0xA3}, homeID, workID}; !reflect.DeepEqual(groups, want) {
		t.Errorf("groups = %v, want by name, then by UUID: %v", groups, want)
	}
}

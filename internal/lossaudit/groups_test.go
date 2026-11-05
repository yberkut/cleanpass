package lossaudit_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

var (
	workID = snapshot.UUID{15: 0xA1}
	homeID = snapshot.UUID{15: 0xA2}
	keysID = snapshot.UUID{15: 0xA3}
)

func group(id snapshot.UUID, modified time.Time, notes string) snapshot.Group {
	return snapshot.Group{UUID: id, Name: "Group", Notes: notes, Times: snapshot.Times{LastModification: modified}}
}

// withGroups is a copy with the given groups under its root group.
func withGroups(groups ...snapshot.Group) *snapshot.Snapshot {
	return &snapshot.Snapshot{Root: snapshot.Group{UUID: rootID, Name: "Root", Groups: groups}}
}

func groupLost(id snapshot.UUID) lossaudit.Loss {
	return lossaudit.Loss{Kind: lossaudit.GroupLost, Object: id}
}

func groupFieldsLost(id snapshot.UUID) lossaudit.Loss {
	return lossaudit.Loss{Kind: lossaudit.GroupFieldsLost, Object: id}
}

func TestAKeptGroupIsNoLoss(t *testing.T) {
	work := group(workID, hours(1), "notes")

	wantLosses(t, audit(withGroups(work), withGroups(work)))
}

func TestAMissingGroupIsALoss(t *testing.T) {
	got := audit(withGroups(), withGroups(group(workID, hours(1), "notes")))

	wantLosses(t, got, groupLost(workID))
}

func TestAGroupBelowAnotherGroupCounts(t *testing.T) {
	work := group(workID, hours(1), "")
	work.Groups = []snapshot.Group{group(keysID, hours(1), "")}

	wantLosses(t, audit(withGroups(group(workID, hours(1), "")), withGroups(work)), groupLost(keysID))
}

// A move is not a loss. The tree may change its shape.
func TestAGroupMayLieAnywhere(t *testing.T) {
	work := group(workID, hours(1), "")
	work.Groups = []snapshot.Group{group(keysID, hours(1), "")}

	wantLosses(t, audit(withGroups(group(workID, hours(1), ""), group(keysID, hours(1), "")), withGroups(work)))
	wantLosses(t, audit(withGroups(work), withGroups(group(workID, hours(1), ""), group(keysID, hours(1), ""))))
}

func TestAGroupKeepsTheFieldsOfItsNewestVersion(t *testing.T) {
	older, newer := withGroups(group(workID, hours(1), "old")), withGroups(group(workID, hours(2), "new"))

	wantLosses(t, audit(newer, older, newer))
	wantLosses(t, audit(newer, newer, older))
	wantLosses(t, audit(older, older, newer), groupFieldsLost(workID))
	wantLosses(t, audit(older, newer, older), groupFieldsLost(workID))
}

// The file has no history of groups. Between two versions with the same time either will do.
func TestEitherVersionOfAGroupWithTheSameTimeWillDo(t *testing.T) {
	first, second := withGroups(group(workID, hours(1), "first")), withGroups(group(workID, hours(1), "second"))

	wantLosses(t, audit(first, first, second))
	wantLosses(t, audit(second, first, second))
	wantLosses(t, audit(withGroups(group(workID, hours(1), "third")), first, second), groupFieldsLost(workID))
}

// All root groups are one group, whatever their UUID.
func TestTheRootGroupsAreOneGroup(t *testing.T) {
	first := database(entry(mailID, version(hours(1), "a")))
	second := database(entry(mailID, version(hours(1), "a")))
	second.Root.UUID = snapshot.UUID{15: 0xF1}

	wantLosses(t, audit(first, first, second))
}

func TestTheRootGroupKeepsTheFieldsOfItsNewestVersion(t *testing.T) {
	first, second := database(), database()
	second.Root.UUID = snapshot.UUID{15: 0xF1}
	second.Root.Notes = "new notes"
	second.Root.Times.LastModification = hours(2)

	wantLosses(t, audit(first, first, second), groupFieldsLost(rootID))
}

// named gives the group another name, and with it another path.
func named(g snapshot.Group, name string) snapshot.Group {
	g.Name = name
	return g
}

// The spec joins groups with the same path. The audit finds the path by itself, from the names in
// the copies and in the result, and takes the word of no engine for it.
func TestAGroupOnTheSamePathLivesOnInTheGroupThere(t *testing.T) {
	input := withGroups(group(workID, hours(1), ""), group(homeID, hours(1), ""))

	wantLosses(t, audit(withGroups(group(workID, hours(1), "")), input))
	wantLosses(t, audit(withGroups(group(homeID, hours(1), "")), input))
}

// Between two versions with the same time either will do, and so does the path of either name.
func TestEitherNameOfAGroupWithTheSameTimeGivesItAPath(t *testing.T) {
	home := named(group(homeID, hours(1), ""), "Home")
	asHome := withGroups(named(group(workID, hours(2), ""), "Home"), home)
	asWork := withGroups(named(group(workID, hours(2), ""), "Work"), home)
	asOffice := withGroups(named(group(workID, hours(2), ""), "Office"), home)

	wantLosses(t, audit(withGroups(home), asHome, asWork))
	wantLosses(t, audit(withGroups(home), asWork, asOffice), groupLost(workID))
}

// One copy moved keys into work, and the other moved work into keys. A merge puts one of them
// under the root to break the cycle, and there it can join a group with its name. So work has two
// places, and in each of them a group with its name. The groups the copies hold under work and
// that keep their UUID stand under the group work went into, because the result is a tree.
//
// The results are those of the Merge Engine, which keeps the smallest UUID of a join.
func TestAGroupOfACycleWentIntoTheGroupItsChildrenStandIn(t *testing.T) {
	ids := func(last byte) snapshot.UUID { return snapshot.UUID{15: last} }
	top, newWork := named(group(ids(0xB1), hours(1), ""), "Work"), named(group(ids(0xB2), hours(1), ""), "Work")
	work, keys := named(group(ids(0xB3), hours(1), ""), "Work"), named(group(ids(0xB4), hours(1), ""), "Keys")
	newKeys := named(group(ids(0xB5), hours(1), ""), "Keys")
	under := func(parent snapshot.Group, children ...snapshot.Group) snapshot.Group {
		parent.Groups = children
		return parent
	}
	moved := func(g snapshot.Group, at time.Time) snapshot.Group {
		g.Times.LocationChanged = at
		return g
	}
	// copies gives the two copies. The older move gives way.
	copies := func(keysMoved, workMoved time.Time) []*snapshot.Snapshot {
		return []*snapshot.Snapshot{
			withGroups(top, under(work, newKeys, moved(keys, keysMoved))),
			withGroups(top, under(keys, moved(work, workMoved), newWork)),
		}
	}

	t.Run("work went under the root", func(t *testing.T) {
		// work joined top there, and newKeys joined keys.
		result := withGroups(under(top, under(keys, newWork)))

		wantLosses(t, audit(result, copies(hours(3), hours(2))...))
	})
	t.Run("keys went under the root", func(t *testing.T) {
		// work joined newWork under keys.
		result := withGroups(top, under(keys, under(newWork, newKeys)))

		wantLosses(t, audit(result, copies(hours(2), hours(3))...))
	})
}

func TestAGroupGoneFromItsPathIsALoss(t *testing.T) {
	input := withGroups(group(homeID, hours(1), ""))

	wantLosses(t, audit(withGroups(), input), groupLost(homeID))
	wantLosses(t, audit(withGroups(named(group(workID, hours(1), ""), "Other")), input), groupLost(homeID))
}

func TestTheSameNameUnderAnotherParentIsAnotherPath(t *testing.T) {
	inWork, inHome := group(keysID, hours(1), ""), group(snapshot.UUID{15: 0xA4}, hours(1), "")
	work, home := named(group(workID, hours(1), ""), "Work"), named(group(homeID, hours(1), ""), "Home")
	work.Groups, home.Groups = []snapshot.Group{inWork}, []snapshot.Group{inHome}
	keptHome := home
	keptHome.Groups = nil

	wantLosses(t, audit(withGroups(work, keptHome), withGroups(work, home)), groupLost(inHome.UUID))
}

func TestJoiningOneGroupDoesNotCoverAnother(t *testing.T) {
	input := withGroups(group(workID, hours(1), ""), group(homeID, hours(1), ""), named(group(keysID, hours(1), ""), "Keys"))

	wantLosses(t, audit(withGroups(group(workID, hours(1), "")), input), groupLost(keysID))
}

func TestGroupsOnOnePathJoinThroughAnyNumber(t *testing.T) {
	input := withGroups(group(workID, hours(1), ""), group(homeID, hours(1), ""), group(keysID, hours(1), ""))

	wantLosses(t, audit(withGroups(group(keysID, hours(1), "")), input))
}

// A group joined by its path must hold what each of the groups holds. Its time is the newest one.
func TestAGroupJoinedByPathKeepsWhatEachGroupHolds(t *testing.T) {
	older, newer := group(workID, hours(1), "old"), group(homeID, hours(2), "")
	newer.Tags = "tag"
	input := withGroups(older, newer)
	joined := group(workID, hours(2), "old")
	joined.Tags = "tag"
	withoutNotes, withoutTags := joined, joined
	withoutNotes.Notes, withoutTags.Tags = "", ""

	wantLosses(t, audit(withGroups(joined), input))
	wantLosses(t, audit(withGroups(withoutNotes), input), groupFieldsLost(workID))
	wantLosses(t, audit(withGroups(withoutTags), input), groupFieldsLost(homeID))
}

// Two groups with different notes cannot be one group without a loss.
func TestGroupsWithDifferentNotesCannotJoin(t *testing.T) {
	input := withGroups(group(workID, hours(1), "old"), group(homeID, hours(2), "new"))

	wantLosses(t, audit(withGroups(group(workID, hours(2), "new")), input), groupFieldsLost(workID))
	wantLosses(t, audit(withGroups(group(workID, hours(1), "old"), group(homeID, hours(2), "new")), input))
}

// A joinedChange is one way a joined group can differ from one of the groups it is made of.
type joinedChange struct {
	name  string
	apply func(g *snapshot.Group)
}

// lostWhenJoined lose what a person set on a group that another group took in.
var lostWhenJoined = []joinedChange{
	{"notes", func(g *snapshot.Group) { g.Notes = "other" }},
	{"icon", func(g *snapshot.Group) { g.IconID = 9 }},
	{"custom icon", func(g *snapshot.Group) { g.CustomIcon = snapshot.UUID{9} }},
	{"expiry", func(g *snapshot.Group) { g.Times.Expires, g.Times.Expiry = true, hours(99) }},
	{"auto-type sequence", func(g *snapshot.Group) { g.DefaultAutoTypeSequence = "{OTHER}" }},
	{"auto-type setting", func(g *snapshot.Group) { g.EnableAutoType = nil }},
	{"search setting", func(g *snapshot.Group) { g.EnableSearching = yes() }},
	{"tag", func(g *snapshot.Group) { g.Tags = "alpha" }},
	{"custom data value", func(g *snapshot.Group) { g.CustomData[0].Value = "other" }},
	{"custom data removed", func(g *snapshot.Group) { g.CustomData = g.CustomData[1:] }},
}

// keptWhenJoined lose nothing: what the other group adds, and what is no setting of a person.
var keptWhenJoined = []joinedChange{
	{"creation", func(g *snapshot.Group) { g.Times.Creation = hours(-99) }},
	{"time of the change", func(g *snapshot.Group) { g.Times.LastModification = hours(50) }},
	{"more custom data", func(g *snapshot.Group) {
		g.CustomData = append(g.CustomData, snapshot.CustomDataItem{Key: "added", Value: "1"})
	}},
}

func TestAJoinedGroupLosesNoPartOfEither(t *testing.T) {
	// An expiry that is on, so that its time counts.
	full := func() snapshot.Group {
		g := fullGroup()
		g.Times.Expires = true
		return g
	}
	plain := group(homeID, hours(5), "")
	plain.IconID = 48
	plain.Name = fullGroup().Name
	for _, c := range lostWhenJoined {
		t.Run(c.name, func(t *testing.T) {
			changed := full()
			c.apply(&changed)

			wantLosses(t, audit(withGroups(changed), withGroups(full(), plain)), groupFieldsLost(workID))
		})
	}
	for _, c := range keptWhenJoined {
		t.Run(c.name, func(t *testing.T) {
			changed := full()
			c.apply(&changed)

			wantLosses(t, audit(withGroups(changed), withGroups(full(), plain)))
		})
	}
}

// A field the group it took in leaves empty is no field of that group: the standard icon of a
// group with no custom icon, an expiry that is off, and a setting that comes from the parent.
// A joined group holds nothing that none of its groups held: such a value belongs to another
// group, which has lost it.
func TestAJoinedGroupHoldsNothingFromNowhere(t *testing.T) {
	input := withGroups(group(workID, hours(1), ""), group(homeID, hours(2), ""))
	notes, tags := group(workID, hours(2), "from another group"), group(workID, hours(2), "")
	tags.Tags = "alien"

	wantLosses(t, audit(withGroups(notes), input), groupFieldsLost(workID))
	wantLosses(t, audit(withGroups(tags), input), groupFieldsLost(workID))
}

func TestAnEmptyFieldOfAJoinedGroupIsNoLoss(t *testing.T) {
	plain := group(homeID, hours(5), "")
	plain.IconID, plain.Times.Expiry = 48, hours(7)
	result := fullGroup()
	result.Times.Expires, result.Times.Expiry = false, hours(1)

	wantLosses(t, audit(withGroups(result), withGroups(result, plain)))
}

// Of the versions of a group with one time, the one that holds all the others has to stay, or
// what only it holds is gone. Without such a version, any of them will do.
func TestOfVersionsWithOneTimeTheOneThatHoldsTheOthersMustStay(t *testing.T) {
	rich, poor := group(workID, hours(1), "notes"), group(workID, hours(1), "")
	rich.Tags = "tag"

	wantLosses(t, audit(withGroups(rich), withGroups(rich), withGroups(poor)))
	wantLosses(t, audit(withGroups(poor), withGroups(rich), withGroups(poor)), groupFieldsLost(workID))
}

// rememberGroups is a copy with the memory that an earlier merge joined these groups.
func rememberGroups(s *snapshot.Snapshot, a, b snapshot.UUID) *snapshot.Snapshot {
	s.Meta.CustomData = append(s.Meta.CustomData, snapshot.CustomDataItem{Key: "cleanpass/groups/" + a.String() + "/" + b.String(), Value: hours(100).Format(time.RFC3339Nano)})
	return s
}

// The groups an earlier merge joined are versions of one group, wherever they stand by now. The
// audit reads that from the copies: the newest version has to stay.
func TestGroupsAnEarlierMergeJoinedAreOneGroup(t *testing.T) {
	old := withGroups(named(group(homeID, hours(1), "old"), "Old name"))
	remembered := func(g snapshot.Group) *snapshot.Snapshot { return rememberGroups(withGroups(g), workID, homeID) }
	memory := remembered(group(workID, hours(2), "new"))

	wantLosses(t, audit(remembered(group(workID, hours(2), "new")), memory, old))
	wantLosses(t, audit(remembered(group(workID, hours(1), "old")), memory, old), groupFieldsLost(workID))
	wantLosses(t, audit(withGroups(group(workID, hours(2), "new")), withGroups(group(workID, hours(2), "new")), old), groupLost(homeID))
	wantLosses(t, audit(remembered(group(workID, hours(2), "new")), old), groupLost(homeID))
}

// All recycle bins are one group, whatever their names and places.
func TestAllBinsAreOneGroup(t *testing.T) {
	first, second := withGroups(named(group(workID, hours(1), ""), "Recycle Bin")), withGroups(named(group(homeID, hours(2), ""), "Кошик"))
	first.Meta.RecycleBin, second.Meta.RecycleBin = workID, homeID
	result := withGroups(named(group(workID, hours(2), ""), "Кошик"))
	result.Meta.RecycleBin = workID

	wantLosses(t, audit(result, first, second))
	second.Meta.RecycleBin = snapshot.UUID{}
	wantLosses(t, audit(first, first, second), groupLost(homeID))
}

// One copy holds two groups named Home in work in its bin. The other copy holds work outside the
// bin, with a third Home in it. Work stays alive, so a merge puts the two into the bin, and there
// they join. Their place is the bin only: in the live Home they would come back to life.
//
// The result is that of the Merge Engine: the join keeps the smallest UUID and the fields of the
// newest group, also its time of creation.
func TestADeletedGroupOfALiveParentWentIntoTheBin(t *testing.T) {
	ids := func(last byte) snapshot.UUID { return snapshot.UUID{15: last} }
	created := func(g snapshot.Group, at time.Time) snapshot.Group {
		g.Times.Creation = at
		return g
	}
	under := func(parent snapshot.Group, children ...snapshot.Group) snapshot.Group {
		parent.Groups = children
		return parent
	}
	bin, work := named(group(ids(0xC1), hours(1), ""), "Recycle Bin"), named(group(ids(0xC2), hours(1), ""), "Work")
	older := created(named(group(ids(0xC3), hours(1), ""), "Home"), hours(1))
	newer := created(named(group(ids(0xC4), hours(2), ""), "Home"), hours(2))
	live := created(named(group(ids(0xC5), hours(1), ""), "Home"), hours(1))
	deleted, kept := withGroups(under(bin, under(work, older, newer))), withGroups(under(work, live), bin)
	joined := newer
	joined.UUID = older.UUID
	result := withGroups(under(bin, joined), under(work, live))
	for _, s := range []*snapshot.Snapshot{deleted, kept, result} {
		s.Meta.RecycleBin = bin.UUID
	}

	wantLosses(t, audit(result, deleted, kept))
}

// The bin joins no plain group with its name: what lies in that group is alive.
func TestAPlainGroupDoesNotJoinTheBin(t *testing.T) {
	bin := withGroups(named(group(workID, hours(1), ""), "Bin"))
	bin.Meta.RecycleBin = workID
	plain := withGroups(named(group(homeID, hours(1), ""), "Bin"))

	wantLosses(t, audit(bin, bin, plain), groupLost(homeID))
}

func TestAGroupTheHumanDeletedIsNoLoss(t *testing.T) {
	input := withGroups(named(group(workID, hours(1), ""), "Work"), group(homeID, hours(1), ""))

	got := allowedAudit(lossaudit.Allowed{Deleted: []snapshot.UUID{workID}}, withGroups(group(homeID, hours(1), "")), input)

	wantLosses(t, got)
}

// Deleting a group is not a decision about what it holds.
func TestDeletingAGroupDoesNotCoverItsEntries(t *testing.T) {
	work := group(workID, hours(1), "")
	work.Entries = []snapshot.Entry{entry(mailID, version(hours(1), "a"))}

	got := allowedAudit(lossaudit.Allowed{Deleted: []snapshot.UUID{workID}}, withGroups(), withGroups(work))

	wantLosses(t, got, versionLost(mailID, hours(1)))
}

// A groupChange is one way a group in the result can differ from the newest version of it.
type groupChange struct {
	name string
	// touches names the field of snapshot.Group this change proves something about.
	touches string
	apply   func(g *snapshot.Group)
}

// lostGroupChanges lose something a person set on the group.
var lostGroupChanges = []groupChange{
	{"name", "Name", func(g *snapshot.Group) { g.Name = "other" }},
	{"notes", "Notes", func(g *snapshot.Group) { g.Notes = "other" }},
	{"icon", "IconID", func(g *snapshot.Group) { g.IconID = 9 }},
	{"custom icon", "CustomIcon", func(g *snapshot.Group) { g.CustomIcon = snapshot.UUID{9} }},
	{"creation", "Times.Creation", func(g *snapshot.Group) { g.Times.Creation = hours(-99) }},
	{"expiry", "Times.Expiry", func(g *snapshot.Group) { g.Times.Expiry = hours(99) }},
	{"expires", "Times.Expires", func(g *snapshot.Group) { g.Times.Expires = !g.Times.Expires }},
	{"auto-type sequence", "DefaultAutoTypeSequence", func(g *snapshot.Group) { g.DefaultAutoTypeSequence = "{OTHER}" }},
	{"auto-type setting", "EnableAutoType", func(g *snapshot.Group) { g.EnableAutoType = nil }},
	{"search setting", "EnableSearching", func(g *snapshot.Group) { g.EnableSearching = yes() }},
	{"tag", "Tags", func(g *snapshot.Group) { g.Tags = "other" }},
	{"custom data value", "CustomData", func(g *snapshot.Group) { g.CustomData[0].Value = "other" }},
	{"custom data key", "CustomData", func(g *snapshot.Group) { g.CustomData[0].Key = "other" }},
	{"custom data removed", "CustomData", func(g *snapshot.Group) { g.CustomData = g.CustomData[1:] }},
}

// sameGroupChanges lose nothing: usage, the state of the window, the place in the tree, and
// what a plain save changes.
var sameGroupChanges = []groupChange{
	{"time of the change", "Times.LastModification", func(g *snapshot.Group) { g.Times.LastModification = hours(50) }},
	{"last access", "Times.LastAccess", func(g *snapshot.Group) { g.Times.LastAccess = hours(50) }},
	{"usage count", "Times.UsageCount", func(g *snapshot.Group) { g.Times.UsageCount = 50 }},
	{"location changed", "Times.LocationChanged", func(g *snapshot.Group) { g.Times.LocationChanged = hours(50) }},
	{"previous parent", "PreviousParentGroup", func(g *snapshot.Group) { g.PreviousParentGroup = &snapshot.UUID{9} }},
	{"expanded", "IsExpanded", func(g *snapshot.Group) { g.IsExpanded = !g.IsExpanded }},
	{"top visible entry", "LastTopVisibleEntry", func(g *snapshot.Group) { g.LastTopVisibleEntry = snapshot.UUID{9} }},
	{"more custom data", "CustomData", func(g *snapshot.Group) {
		g.CustomData = append(g.CustomData, snapshot.CustomDataItem{Key: "added", Value: "1"})
	}},
	{"_LAST_MODIFIED", "CustomData", func(g *snapshot.Group) {
		g.CustomData = append(g.CustomData, snapshot.CustomDataItem{Key: snapshot.CustomDataLastModified, Value: "Mon"})
	}},
	{"time of a custom data item", "CustomData", func(g *snapshot.Group) { g.CustomData[0].LastModification = hours(50) }},
	{"order of custom data", "CustomData", func(g *snapshot.Group) { slices.Reverse(g.CustomData) }},
	{"spelling of the tags", "Tags", func(g *snapshot.Group) { g.Tags = "gamma;alpha" }},
	{"an entry more", "Entries", func(g *snapshot.Group) {
		g.Entries = []snapshot.Entry{entry(bankID, version(hours(1), "b"))}
	}},
	{"a group more", "Groups", func(g *snapshot.Group) { g.Groups = []snapshot.Group{group(homeID, hours(1), "")} }},
}

// fullGroup has a value in everything that can change. Each call gives its own slices.
func fullGroup() snapshot.Group {
	g := group(workID, hours(1), "notes")
	g.IconID = 3
	g.CustomIcon = snapshot.UUID{3}
	g.Times.Creation, g.Times.Expiry = hours(-10), hours(90)
	g.DefaultAutoTypeSequence = "{PASSWORD}"
	g.EnableAutoType, g.EnableSearching = yes(), no()
	g.Tags = "alpha,gamma"
	g.CustomData = []snapshot.CustomDataItem{{Key: "plugin", Value: "1"}, {Key: "other", Value: "2"}}
	return g
}

func TestAChangedPartOfAGroupIsALoss(t *testing.T) {
	for _, c := range lostGroupChanges {
		t.Run(c.name, func(t *testing.T) {
			changed := fullGroup()
			c.apply(&changed)

			wantLosses(t, audit(withGroups(changed), withGroups(fullGroup())), groupFieldsLost(workID))
		})
	}
}

func TestAChangedPartOfAGroupThatLosesNothingIsNoLoss(t *testing.T) {
	for _, c := range sameGroupChanges {
		t.Run(c.name, func(t *testing.T) {
			changed := fullGroup()
			c.apply(&changed)

			wantLosses(t, audit(withGroups(changed), withGroups(fullGroup())))
		})
	}
}

// A service item of the newest version may be gone, since a save renews it anyway.
func TestServiceDataOfAGroupMayGo(t *testing.T) {
	input := fullGroup()
	input.CustomData = append(input.CustomData, snapshot.CustomDataItem{Key: snapshot.CustomDataRandomSlug, Value: "abc"})

	wantLosses(t, audit(withGroups(fullGroup()), withGroups(input)))
}

// A new field of a group must be sorted into one of the two lists above, or the audit would
// miss its loss without any test noticing.
func TestEveryPartOfAGroupIsClassified(t *testing.T) {
	proven := map[string]bool{"UUID": true} // TestAMissingGroupIsALoss
	for _, c := range slices.Concat(lostGroupChanges, sameGroupChanges) {
		proven[c.touches] = true
	}
	groupType := reflect.TypeFor[snapshot.Group]()
	for field := range groupType.Fields() {
		if field.Type == reflect.TypeFor[snapshot.Times]() {
			for inner := range field.Type.Fields() {
				if name := field.Name + "." + inner.Name; !proven[name] {
					t.Errorf("no test says whether the audit covers %s", name)
				}
			}
			continue
		}
		if !proven[field.Name] {
			t.Errorf("no test says whether the audit covers %s", field.Name)
		}
	}
}

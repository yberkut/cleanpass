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

func mergedGroups(from, to snapshot.UUID) lossaudit.Allowed {
	return lossaudit.Allowed{MergedGroups: map[snapshot.UUID]snapshot.UUID{from: to}}
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

func TestAGroupMergedByPathLivesOnInTheSurvivor(t *testing.T) {
	input := withGroups(group(workID, hours(1), ""), group(homeID, hours(1), ""))

	got := allowedAudit(mergedGroups(homeID, workID), withGroups(group(workID, hours(1), "")), input)

	wantLosses(t, got)
}

func TestAGroupMergedByPathNeedsItsSurvivor(t *testing.T) {
	input := withGroups(group(homeID, hours(1), ""))

	wantLosses(t, allowedAudit(mergedGroups(homeID, workID), withGroups(), input), groupLost(workID))
	wantLosses(t, allowedAudit(mergedGroups(homeID, workID), input, input), groupLost(workID))
}

func TestMergingOneGroupDoesNotCoverAnother(t *testing.T) {
	input := withGroups(group(workID, hours(1), ""), group(homeID, hours(1), ""), group(keysID, hours(1), ""))

	got := allowedAudit(mergedGroups(homeID, workID), withGroups(group(workID, hours(1), "")), input)

	wantLosses(t, got, groupLost(keysID))
}

// Merged groups are one group, so the survivor shows the newest fields of any of them.
func TestGroupsMergedByPathKeepTheNewestFields(t *testing.T) {
	input := withGroups(group(workID, hours(1), "old"), group(homeID, hours(2), "new"))

	wantLosses(t, allowedAudit(mergedGroups(homeID, workID), withGroups(group(workID, hours(2), "new")), input))
	wantLosses(t, allowedAudit(mergedGroups(homeID, workID), withGroups(group(workID, hours(1), "old")), input), groupFieldsLost(workID))
}

func TestGroupsMergeThroughAChain(t *testing.T) {
	input := withGroups(group(workID, hours(1), ""), group(homeID, hours(1), ""), group(keysID, hours(1), ""))
	chain := lossaudit.Allowed{MergedGroups: map[snapshot.UUID]snapshot.UUID{homeID: workID, workID: keysID}}

	wantLosses(t, allowedAudit(chain, withGroups(group(keysID, hours(1), "")), input))
}

func TestGroupsMergedIntoARootGroupLiveOnInTheRoot(t *testing.T) {
	input := withGroups(group(homeID, hours(1), ""))
	input.Root.Times.LastModification = hours(2)

	wantLosses(t, allowedAudit(mergedGroups(homeID, rootID), database(), input))
}

func TestALoopOfMergedGroupsHidesNoLoss(t *testing.T) {
	input := withGroups(group(workID, hours(1), ""), group(homeID, hours(1), ""))
	loop := lossaudit.Allowed{MergedGroups: map[snapshot.UUID]snapshot.UUID{workID: homeID, homeID: workID}}

	wantLosses(t, allowedAudit(loop, withGroups(), input), groupLost(workID), groupLost(homeID))
}

func TestAGroupTheHumanDeletedIsNoLoss(t *testing.T) {
	input := withGroups(group(workID, hours(1), ""), group(homeID, hours(1), ""))

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
	for i := range groupType.NumField() {
		field := groupType.Field(i)
		if field.Type == reflect.TypeFor[snapshot.Times]() {
			for j := range field.Type.NumField() {
				if name := field.Name + "." + field.Type.Field(j).Name; !proven[name] {
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

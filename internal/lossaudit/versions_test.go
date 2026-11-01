package lossaudit_test

import (
	"testing"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

func TestAResultThatHoldsEveryVersionHasNoLoss(t *testing.T) {
	older, newer := version(hours(1), "old"), version(hours(2), "new")

	got := audit(
		database(entry(mailID, newer, older)),
		database(entry(mailID, older)),
		database(entry(mailID, newer, older)),
	)

	wantLosses(t, got)
}

func TestNoCopiesLoseNothing(t *testing.T) {
	result := database(entry(mailID, version(hours(1), "a")))
	result.Meta.DatabaseName = "name"

	wantLosses(t, audit(result))
}

func TestAMissingHistoryVersionIsALoss(t *testing.T) {
	older, newer := version(hours(1), "old"), version(hours(2), "new")

	got := audit(database(entry(mailID, newer)), database(entry(mailID, newer, older)))

	wantLosses(t, got, versionLost(mailID, hours(1)))
}

func TestAMissingMainVersionIsALoss(t *testing.T) {
	older, newer := version(hours(1), "old"), version(hours(2), "new")

	got := audit(database(entry(mailID, older)), database(entry(mailID, older)), database(entry(mailID, newer)))

	wantLosses(t, got, versionLost(mailID, hours(2)))
}

func TestAnEntryThatIsGoneLosesEachOfItsVersions(t *testing.T) {
	older, newer := version(hours(1), "old"), version(hours(2), "new")

	got := audit(database(), database(entry(mailID, newer, older)))

	wantLosses(t, got, versionLost(mailID, hours(1)), versionLost(mailID, hours(2)))
}

func TestAVersionUnderAnotherEntryDoesNotCount(t *testing.T) {
	v := version(hours(1), "same")

	got := audit(database(entry(bankID, v)), database(entry(mailID, v), entry(bankID, v)))

	wantLosses(t, got, versionLost(mailID, hours(1)))
}

func TestAVersionWithTheSameContentAtAnotherTimeDoesNotCount(t *testing.T) {
	got := audit(database(entry(mailID, version(hours(2), "same"))), database(entry(mailID, version(hours(1), "same"))))

	wantLosses(t, got, versionLost(mailID, hours(1)))
}

func TestAVersionWithOtherContentAtTheSameTimeDoesNotCount(t *testing.T) {
	got := audit(database(entry(mailID, version(hours(1), "other"))), database(entry(mailID, version(hours(1), "secret"))))

	wantLosses(t, got, versionLost(mailID, hours(1)))
}

// The same lost version in several copies is one loss, and the order of the copies does not
// change the list.
func TestAVersionLostFromSeveralCopiesIsOneLoss(t *testing.T) {
	first := database(entry(mailID, version(hours(2), "b"), version(hours(1), "a")), entry(bankID, version(hours(3), "c")))
	second := database(entry(bankID, version(hours(3), "c")), entry(mailID, version(hours(2), "b")))

	got := audit(database(), first, second)
	backward := audit(database(), second, first)

	wantLosses(t, got, versionLost(mailID, hours(1)), versionLost(mailID, hours(2)), versionLost(bankID, hours(3)))
	wantLosses(t, backward, got...)
}

// An entry can lie in any group of a copy, and a merge can move it.
func TestAVersionCountsInAnyGroup(t *testing.T) {
	held := []snapshot.Entry{entry(mailID, version(hours(1), "secret"))}
	tree := func(inKeys, inHome []snapshot.Entry) *snapshot.Snapshot {
		return withGroups(snapshot.Group{UUID: workID, Groups: []snapshot.Group{
			{UUID: keysID, Entries: inKeys},
			{UUID: homeID, Entries: inHome},
		}})
	}
	input := tree(held, nil)

	wantLosses(t, audit(tree(nil, nil), input), versionLost(mailID, hours(1)))
	wantLosses(t, audit(tree(nil, held), input))
}

package lossaudit_test

import (
	"testing"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

func allowedAudit(allowed lossaudit.Allowed, result *snapshot.Snapshot, copies ...*snapshot.Snapshot) []lossaudit.Loss {
	return lossaudit.Audit(lossaudit.Input{Copies: copies, Result: result, Allowed: allowed})
}

func TestAnEntryTheHumanDeletedIsNoLoss(t *testing.T) {
	input := database(entry(mailID, version(hours(2), "new"), version(hours(1), "old")))

	got := allowedAudit(lossaudit.Allowed{Deleted: []snapshot.UUID{mailID}}, database(), input)

	wantLosses(t, got)
}

func TestADecisionToDeleteOneEntryDoesNotCoverAnother(t *testing.T) {
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	got := allowedAudit(lossaudit.Allowed{Deleted: []snapshot.UUID{mailID}}, database(), input)

	wantLosses(t, got, versionLost(bankID, hours(2)))
}

func TestVersionsOfAnAbsorbedEntryLiveOnUnderTheSurvivor(t *testing.T) {
	result := database(entry(bankID, version(hours(2), "b"), version(hours(1), "a")))
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	got := allowedAudit(lossaudit.Allowed{Absorbed: map[snapshot.UUID]snapshot.UUID{mailID: bankID}}, result, input)

	wantLosses(t, got)
}

func TestAbsorbingDoesNotLetAVersionGo(t *testing.T) {
	result := database(entry(bankID, version(hours(2), "b")))
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	got := allowedAudit(lossaudit.Allowed{Absorbed: map[snapshot.UUID]snapshot.UUID{mailID: bankID}}, result, input)

	wantLosses(t, got, versionLost(mailID, hours(1)))
}

func TestAbsorbingDoesNotCoverEntriesTheDecisionDoesNotName(t *testing.T) {
	result := database(entry(bankID, version(hours(2), "b"), version(hours(1), "a")))
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	got := allowedAudit(lossaudit.Allowed{Absorbed: map[snapshot.UUID]snapshot.UUID{bankID: mailID}}, result, input)

	wantLosses(t, got, versionLost(mailID, hours(1)))
}

func TestThirdEntryAbsorbedThroughAChainLivesOnUnderTheLastSurvivor(t *testing.T) {
	third := snapshot.UUID{15: 0x03}
	result := database(entry(third, version(hours(3), "c"), version(hours(1), "a"), version(hours(2), "b")))
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")), entry(third, version(hours(3), "c")))

	got := allowedAudit(lossaudit.Allowed{Absorbed: map[snapshot.UUID]snapshot.UUID{mailID: bankID, bankID: third}}, result, input)

	wantLosses(t, got)
}

// A pair the human merged and then the human deleted: all of it goes by that decision.
func TestDeletingTheSurvivorCoversWhatItAbsorbed(t *testing.T) {
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	got := allowedAudit(lossaudit.Allowed{
		Absorbed: map[snapshot.UUID]snapshot.UUID{mailID: bankID},
		Deleted:  []snapshot.UUID{bankID},
	}, database(), input)

	wantLosses(t, got)
}

func TestDeletingTheAbsorbedEntryDoesNotCoverTheSurvivor(t *testing.T) {
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	got := allowedAudit(lossaudit.Allowed{
		Absorbed: map[snapshot.UUID]snapshot.UUID{mailID: bankID},
		Deleted:  []snapshot.UUID{mailID},
	}, database(), input)

	wantLosses(t, got, versionLost(bankID, hours(2)))
}

// A decision with a loop is a bug of the caller. It must neither hang nor hide a loss.
func TestALoopOfAbsorbedEntriesHidesNoLoss(t *testing.T) {
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))
	loop := lossaudit.Allowed{Absorbed: map[snapshot.UUID]snapshot.UUID{mailID: bankID, bankID: mailID}}

	wantLosses(t, allowedAudit(loop, database(), input), versionLost(mailID, hours(1)), versionLost(bankID, hours(2)))
	wantLosses(t, allowedAudit(loop, input, input))

	self := lossaudit.Allowed{Absorbed: map[snapshot.UUID]snapshot.UUID{mailID: mailID}}
	wantLosses(t, allowedAudit(self, database(), database(entry(mailID, version(hours(1), "a")))), versionLost(mailID, hours(1)))
}

// A merge the engine did not make still loses nothing if the versions are in the result.
func TestAVersionUnderItsOwnEntryCountsEvenIfTheDecisionMovedIt(t *testing.T) {
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	got := allowedAudit(lossaudit.Allowed{Absorbed: map[snapshot.UUID]snapshot.UUID{mailID: bankID}}, input, input)

	wantLosses(t, got)
}

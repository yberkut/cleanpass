package lossaudit_test

import (
	"testing"
	"time"

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

// pair is the decision of the human in this merge to merge two entries into one.
func pair(a, b snapshot.UUID) lossaudit.Allowed {
	return lossaudit.Allowed{MergedPairs: [][2]snapshot.UUID{{a, b}}}
}

func TestVersionsOfAMergedPairLiveOnUnderEitherEntry(t *testing.T) {
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	wantLosses(t, allowedAudit(pair(mailID, bankID), database(entry(bankID, version(hours(2), "b"), version(hours(1), "a"))), input))
	wantLosses(t, allowedAudit(pair(bankID, mailID), database(entry(mailID, version(hours(2), "b"), version(hours(1), "a"))), input))
}

func TestMergingAPairDoesNotLetAVersionGo(t *testing.T) {
	result := database(entry(bankID, version(hours(2), "b")))
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	wantLosses(t, allowedAudit(pair(mailID, bankID), result, input), versionLost(mailID, hours(1)))
}

// An engine that merges entries with no decision about them loses them.
func TestAMergeNoDecisionNamesIsALoss(t *testing.T) {
	third := snapshot.UUID{15: 0x03}
	result := database(entry(third, version(hours(3), "c"), version(hours(1), "a")), entry(bankID, version(hours(2), "b")))
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")), entry(third, version(hours(3), "c")))

	wantLosses(t, allowedAudit(pair(mailID, bankID), result, input), versionLost(mailID, hours(1)))
	wantLosses(t, audit(result, input), versionLost(mailID, hours(1)))
}

func TestPairsMergeThroughAChain(t *testing.T) {
	third := snapshot.UUID{15: 0x03}
	result := database(entry(third, version(hours(3), "c"), version(hours(1), "a"), version(hours(2), "b")))
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")), entry(third, version(hours(3), "c")))
	chain := lossaudit.Allowed{MergedPairs: [][2]snapshot.UUID{{mailID, bankID}, {bankID, third}}}

	wantLosses(t, allowedAudit(chain, result, input))
}

// A merged pair is one entry, so a decision to delete it covers both.
func TestDeletingAMergedPairCoversBothEntries(t *testing.T) {
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	for _, deleted := range []snapshot.UUID{mailID, bankID} {
		allowed := pair(mailID, bankID)
		allowed.Deleted = []snapshot.UUID{deleted}

		wantLosses(t, allowedAudit(allowed, database(), input))
	}
}

// A decision with a pair of one entry is a bug of the caller. It lets nothing go.
func TestAPairOfOneEntryLetsNothingGo(t *testing.T) {
	input := database(entry(mailID, version(hours(1), "a")))

	wantLosses(t, allowedAudit(pair(mailID, mailID), database(), input), versionLost(mailID, hours(1)))
}

// A merge the engine did not make still loses nothing if the versions are in the result.
func TestAVersionUnderItsOwnEntryCountsEvenIfTheDecisionMergedIt(t *testing.T) {
	input := database(entry(mailID, version(hours(1), "a")), entry(bankID, version(hours(2), "b")))

	wantLosses(t, allowedAudit(pair(mailID, bankID), input, input))
}

// rememberPair is a copy with the memory that an earlier merge made one entry of these two.
func rememberPair(s *snapshot.Snapshot, a, b snapshot.UUID) *snapshot.Snapshot {
	s.Meta.CustomData = append(s.Meta.CustomData, snapshot.CustomDataItem{Key: "cleanpass/entries/" + a.String() + "/" + b.String(), Value: hours(100).Format(time.RFC3339Nano)})
	return s
}

// An old copy still holds an entry an earlier merge took into another one. The audit reads that
// decision from the copies, never from the result or the engine.
func TestAPairAnEarlierMergeMergedLivesOnInTheOtherEntry(t *testing.T) {
	merged := entry(bankID, version(hours(2), "b"), version(hours(1), "a"))
	old := database(entry(mailID, version(hours(1), "a")))
	result := rememberPair(database(merged), bankID, mailID)

	wantLosses(t, audit(result, rememberPair(database(merged), bankID, mailID), old))
	wantLosses(t, audit(database(merged), database(merged), old), versionLost(mailID, hours(1)))
	wantLosses(t, audit(result, old), versionLost(mailID, hours(1)))
}

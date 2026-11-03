package lossaudit_test

import (
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

func withDeletions(s *snapshot.Snapshot, deletions ...snapshot.Deletion) *snapshot.Snapshot {
	s.Deletions = deletions
	return s
}

func deletion(id snapshot.UUID, at time.Time) snapshot.Deletion {
	return snapshot.Deletion{UUID: id, Time: at}
}

func deletionLost(id snapshot.UUID, at time.Time) lossaudit.Loss {
	return lossaudit.Loss{Kind: lossaudit.DeletionLost, Object: id, Time: at}
}

func TestAKeptDeletionRecordIsNoLoss(t *testing.T) {
	record := deletion(mailID, hours(1))

	wantLosses(t, audit(withDeletions(database(), record), withDeletions(database(), record)))
}

func TestAMissingDeletionRecordIsALoss(t *testing.T) {
	got := audit(database(), withDeletions(database(), deletion(mailID, hours(1))))

	wantLosses(t, got, deletionLost(mailID, hours(1)))
}

func TestDeletionRecordsJoinByUUIDWithTheNewestTime(t *testing.T) {
	older, newer := withDeletions(database(), deletion(mailID, hours(1))), withDeletions(database(), deletion(mailID, hours(2)))

	wantLosses(t, audit(withDeletions(database(), deletion(mailID, hours(2))), older, newer))
	wantLosses(t, audit(withDeletions(database(), deletion(mailID, hours(1))), older, newer), deletionLost(mailID, hours(2)))
	wantLosses(t, audit(database(), older, newer), deletionLost(mailID, hours(2)))
	wantLosses(t, audit(database(), newer, older), deletionLost(mailID, hours(2)))
}

func TestARecordOfAnotherObjectDoesNotCount(t *testing.T) {
	got := audit(withDeletions(database(), deletion(bankID, hours(9))), withDeletions(database(), deletion(mailID, hours(1))))

	wantLosses(t, got, deletionLost(mailID, hours(1)))
}

// The newest of several records of one object in the result decides.
func TestTheResultMayHoldARecordTwice(t *testing.T) {
	result := withDeletions(database(), deletion(mailID, hours(2)), deletion(mailID, hours(1)))

	wantLosses(t, audit(result, withDeletions(database(), deletion(mailID, hours(2)))))
}

// The spec joins the records and lets only a decision to delete remove data. That decision
// removes the entry, not the record, or an old copy would bring the entry back.
func TestADecisionToDeleteDoesNotLetTheRecordGo(t *testing.T) {
	input := withDeletions(database(entry(mailID, version(hours(1), "a"))), deletion(mailID, hours(2)))

	got := allowedAudit(lossaudit.Allowed{Deleted: []snapshot.UUID{mailID}}, database(), input)

	wantLosses(t, got, deletionLost(mailID, hours(2)))
}

func TestAnEntryThatStaysDoesNotLetItsRecordGo(t *testing.T) {
	e := entry(mailID, version(hours(1), "a"))

	got := audit(database(e), database(e), withDeletions(database(), deletion(mailID, hours(2))))

	wantLosses(t, got, deletionLost(mailID, hours(2)))
}

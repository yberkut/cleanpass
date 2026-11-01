package lossaudit_test

import (
	"testing"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

var (
	content   = []byte("attachment bytes")
	contentID = snapshot.NewAttachmentID(content)
)

// withAttachment is a version that refers to content.
func withAttachment(v snapshot.EntryVersion) snapshot.EntryVersion {
	v.Attachments = []snapshot.AttachmentRef{{Name: "file.bin", ID: contentID}}
	return v
}

// holding adds the bytes of content to a copy.
func holding(s *snapshot.Snapshot, protected bool) *snapshot.Snapshot {
	s.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{contentID: {Content: content, Protected: protected}}
	return s
}

func attachmentLost(entry snapshot.UUID) lossaudit.Loss {
	return lossaudit.Loss{Kind: lossaudit.AttachmentLost, Object: entry, Attachment: contentID}
}

func TestAResultThatHoldsTheBytesHasNoAttachmentLoss(t *testing.T) {
	e := entry(mailID, withAttachment(version(hours(1), "a")))

	wantLosses(t, audit(holding(database(e), false), holding(database(e), false)))
}

func TestMissingBytesOfAnAttachmentAreALoss(t *testing.T) {
	e := entry(mailID, withAttachment(version(hours(1), "a")))

	wantLosses(t, audit(database(e), holding(database(e), false)), attachmentLost(mailID))
}

func TestChangedBytesOfAnAttachmentAreALoss(t *testing.T) {
	e := entry(mailID, withAttachment(version(hours(1), "a")))
	result := database(e)
	result.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{contentID: {Content: []byte("other bytes")}}

	wantLosses(t, audit(result, holding(database(e), false)), attachmentLost(mailID))
}

// Empty bytes are a real content. Having them under the right ID is enough.
func TestEmptyBytesAreKeptToo(t *testing.T) {
	empty := snapshot.NewAttachmentID(nil)
	v := version(hours(1), "a")
	v.Attachments = []snapshot.AttachmentRef{{Name: "empty.bin", ID: empty}}
	stored := map[snapshot.AttachmentID]snapshot.Attachment{empty: {}}
	input, result := database(entry(mailID, v)), database(entry(mailID, v))
	input.Attachments, result.Attachments = stored, stored

	wantLosses(t, audit(result, input))

	result.Attachments = nil
	wantLosses(t, audit(result, input), lossaudit.Loss{Kind: lossaudit.AttachmentLost, Object: mailID, Attachment: empty})
}

func TestLostProtectionOfAnAttachmentIsALoss(t *testing.T) {
	e := entry(mailID, withAttachment(version(hours(1), "a")))

	wantLosses(t, audit(holding(database(e), false), holding(database(e), true)), attachmentLost(mailID))
	wantLosses(t, audit(holding(database(e), true), holding(database(e), true)))
	wantLosses(t, audit(holding(database(e), true), holding(database(e), false)))
}

// The bytes of a version in history are as dear as those of a main version.
func TestAttachmentOfAHistoryVersionCounts(t *testing.T) {
	e := entry(mailID, version(hours(2), "b"), withAttachment(version(hours(1), "a")))

	wantLosses(t, audit(database(e), holding(database(e), false)), attachmentLost(mailID))
}

// The bytes can lie in one copy while the version lies in another.
func TestBytesFromAnotherCopyCount(t *testing.T) {
	e := entry(mailID, withAttachment(version(hours(1), "a")))

	wantLosses(t, audit(database(e), database(e), holding(database(), false)), attachmentLost(mailID))
}

func TestBytesOfAnAttachmentOfADeletedEntryMayGo(t *testing.T) {
	input := holding(database(entry(mailID, withAttachment(version(hours(1), "a")))), false)

	got := allowedAudit(lossaudit.Allowed{Deleted: []snapshot.UUID{mailID}}, database(), input)

	wantLosses(t, got)
}

func TestBytesSharedWithAKeptEntryStay(t *testing.T) {
	kept := entry(bankID, withAttachment(version(hours(2), "b")))
	input := holding(database(entry(mailID, withAttachment(version(hours(1), "a"))), kept), false)

	got := allowedAudit(lossaudit.Allowed{Deleted: []snapshot.UUID{mailID}}, database(kept), input)

	wantLosses(t, got, attachmentLost(bankID))
}

// A copy can forget a version with its last reference, at a history limit. Bytes with no
// reference at all in a file stop at the Gateway.
func TestBytesNoVersionRefersToAreNoLoss(t *testing.T) {
	wantLosses(t, audit(database(), holding(database(), false)))
}

// The Gateway refuses a reference with no bytes, so this only says that the audit does not
// invent a loss of something no copy held.
func TestAReferenceNoCopyHasBytesForIsNoLoss(t *testing.T) {
	e := entry(mailID, withAttachment(version(hours(1), "a")))

	wantLosses(t, audit(database(e), database(e)))
}

// One lost content is one loss. It names the first entry by UUID, so the order of the copies
// changes nothing.
func TestBytesLostFromSeveralEntriesAreOneLoss(t *testing.T) {
	first := holding(database(entry(bankID, withAttachment(version(hours(2), "b")))), false)
	second := holding(database(entry(mailID, withAttachment(version(hours(1), "a")))), false)
	result := database(entry(bankID, withAttachment(version(hours(2), "b"))), entry(mailID, withAttachment(version(hours(1), "a"))))

	wantLosses(t, audit(result, first, second), attachmentLost(mailID))
	wantLosses(t, audit(result, second, first), attachmentLost(mailID))
}

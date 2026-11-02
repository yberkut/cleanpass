package lossaudit_test

import (
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// remembering is a copy with the memory that the human deleted the entry with versions of these
// times. The audit reads this by itself, so nothing else has to name the entry.
func remembering(entryID snapshot.UUID, times ...time.Time) *snapshot.Snapshot {
	s := database()
	for _, at := range times {
		s.Meta.CustomData = append(s.Meta.CustomData, snapshot.CustomDataItem{
			Key:   "cleanpass/accepted/" + entryID.String() + "/" + at.UTC().Format(time.RFC3339Nano),
			Value: hours(100).Format(time.RFC3339Nano),
		})
	}
	return s
}

func TestAnEntryTheMemoryNamesWithAllItsVersionsIsNoLoss(t *testing.T) {
	old := database(entry(mailID, version(hours(2), "new"), version(hours(1), "older")))
	memory := remembering(mailID, hours(1), hours(2))

	wantLosses(t, audit(memory, memory, old))
}

// A clock that runs behind writes an old time on a new edit, so a date does not say enough.
func TestAVersionWithATimeTheMemoryDoesNotNameIsALoss(t *testing.T) {
	old := database(entry(mailID, version(hours(2), "new"), version(hours(1), "older"), version(hours(-5), "an edit nobody saw")))
	memory := remembering(mailID, hours(1), hours(2))

	wantLosses(t, audit(memory, memory, old), versionLost(mailID, hours(-5)))
}

// The result is what the audit checks, so it proves nothing about itself.
func TestAMemoryOnlyTheResultHoldsLetsNothingGo(t *testing.T) {
	old := database(entry(mailID, version(hours(2), "new")))
	result := remembering(mailID, hours(2))

	wantLosses(t, audit(result, old), versionLost(mailID, hours(2)))
}

func TestAMemoryAboutAnotherEntryLetsNothingGo(t *testing.T) {
	old := database(entry(mailID, version(hours(2), "new")))
	memory := remembering(bankID, hours(2))

	wantLosses(t, audit(memory, memory, old), versionLost(mailID, hours(2)))
}

// A rejection is the opposite of a decision to delete, under a key that looks much the same.
func TestOtherKeysOfTheMemoryLetNothingGo(t *testing.T) {
	old := database(entry(mailID, version(hours(2), "new")))
	for _, key := range []string{
		"cleanpass/rejected/" + mailID.String() + "/" + hours(2).Format(time.RFC3339Nano),
		"cleanpass/accepted/" + mailID.String(),
		"accepted/" + mailID.String() + "/" + hours(2).Format(time.RFC3339Nano),
	} {
		t.Run(key, func(t *testing.T) {
			memory := database()
			memory.Meta.CustomData = []snapshot.CustomDataItem{{Key: key, Value: "x"}}

			wantLosses(t, audit(memory, memory, old), versionLost(mailID, hours(2)))
		})
	}
}

// An entry that went into another one goes with it: the human saw its versions there.
func TestTheMemoryCoversAnEntryThatWentIntoTheDeletedOne(t *testing.T) {
	old := database(entry(mailID, version(hours(2), "absorbed")))
	memory := remembering(bankID, hours(2))
	allowed := lossaudit.Allowed{Absorbed: map[snapshot.UUID]snapshot.UUID{mailID: bankID}}

	wantLosses(t, allowedAudit(allowed, memory, memory, old))
}

// The bytes of a version that went are not needed. The bytes of the same entry's kept version are.
func TestTheMemoryLetsTheBytesOfAVersionGoOnlyWithTheVersion(t *testing.T) {
	content := []byte("bytes of the version")
	id := snapshot.NewAttachmentID(content)
	attached := func(modified time.Time, password string) snapshot.EntryVersion {
		v := version(modified, password)
		v.Attachments = []snapshot.AttachmentRef{{Name: "a.bin", ID: id}}
		return v
	}
	old := database(entry(mailID, attached(hours(2), "new")))
	old.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{id: {Content: content}}
	memory := remembering(mailID, hours(2))

	t.Run("the version went", func(t *testing.T) {
		wantLosses(t, audit(memory, memory, old))
	})
	t.Run("the version stayed in the bin", func(t *testing.T) {
		result := database(entry(mailID, attached(hours(2), "new")))
		result.Meta.CustomData = memory.Meta.CustomData

		wantLosses(t, audit(result, memory, old), lossaudit.Loss{Kind: lossaudit.AttachmentLost, Object: mailID, Attachment: id})
	})
}

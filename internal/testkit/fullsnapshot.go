package testkit

import (
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// FullSnapshotEntryTitle is the title of the entry in FullSnapshot.
const FullSnapshotEntryTitle = "Повний запис"

// FullSnapshot gives a snapshot in which every part holds a value that is not the zero one,
// and all of it can live in a file. A test of the Snapshot package checks that, so a new part
// of a Snapshot lands here and in every test that goes through this function.
func FullSnapshot() *snapshot.Snapshot {
	moment := func(seconds int) time.Time {
		return time.Date(2026, 3, 1, 10, 0, seconds, 0, time.UTC)
	}
	id := func(last byte) snapshot.UUID {
		var uuid snapshot.UUID
		uuid[15] = last
		return uuid
	}
	yes, no := true, false
	previous := id(2)
	times := func(base int) snapshot.Times {
		return snapshot.Times{
			Creation:         moment(base),
			LastModification: moment(base + 1),
			LastAccess:       moment(base + 2),
			Expiry:           moment(base + 3),
			Expires:          true,
			UsageCount:       int64(base + 4),
			LocationChanged:  moment(base + 5),
		}
	}
	data := func(key string) []snapshot.CustomDataItem {
		return []snapshot.CustomDataItem{{Key: key, Value: key + "-значення", LastModification: moment(50)}}
	}
	attachment := []byte("full attachment bytes")
	attachmentID := snapshot.NewAttachmentID(attachment)
	version := func(base int, password string) snapshot.EntryVersion {
		return snapshot.EntryVersion{
			IconID:              3,
			CustomIcon:          id(9),
			ForegroundColor:     "#112233",
			BackgroundColor:     "#445566",
			OverrideURL:         "cmd://calc",
			QualityCheck:        &no,
			Tags:                "alpha;бета",
			PreviousParentGroup: &previous,
			Times:               times(base),
			Fields: []snapshot.Field{
				{Key: "Title", Value: FullSnapshotEntryTitle},
				{Key: "Password", Value: password, Protected: true},
			},
			Attachments: []snapshot.AttachmentRef{{Name: "full.bin", ID: attachmentID}},
			AutoType: snapshot.AutoType{
				Enabled:         true,
				Obfuscation:     1,
				DefaultSequence: "{PASSWORD}",
				Associations:    []snapshot.AutoTypeAssociation{{Window: "*Firefox*", KeystrokeSequence: "{USERNAME}"}},
			},
			CustomData: data("entry"),
		}
	}
	group := func(uuid snapshot.UUID, name string, base int) snapshot.Group {
		return snapshot.Group{
			UUID:                    uuid,
			Name:                    name,
			Notes:                   name + " нотатки",
			IconID:                  48,
			CustomIcon:              id(9),
			Times:                   times(base),
			IsExpanded:              true,
			DefaultAutoTypeSequence: "{USERNAME}",
			EnableAutoType:          &yes,
			EnableSearching:         &no,
			LastTopVisibleEntry:     id(1),
			PreviousParentGroup:     &previous,
			Tags:                    "group-tag",
			CustomData:              data("group"),
		}
	}
	root := group(id(2), "Корінь", 10)
	root.Entries = []snapshot.Entry{{
		UUID:         id(1),
		EntryVersion: version(30, "SECRET-MARKER-full-new"),
		History:      []snapshot.EntryVersion{version(20, "SECRET-MARKER-full-old")},
	}}
	root.Groups = []snapshot.Group{group(id(3), "Дочірня", 15)}

	return &snapshot.Snapshot{
		// Format and the other parts that do not live in the XML say what the file was, and
		// a write gets its format separately. These values make the format of a read match.
		Format: snapshot.Format{
			Major: 4, Minor: 1, Cipher: snapshot.CipherAES256,
			KDF: snapshot.KDF{Kind: snapshot.KDFAES, Rounds: 1000},
		},
		Meta: snapshot.Meta{
			Generator:                  "cleanpass-tests",
			SettingsChanged:            moment(1),
			DatabaseName:               "База",
			DatabaseNameChanged:        moment(2),
			DatabaseDescription:        "Опис",
			DatabaseDescriptionChanged: moment(3),
			DefaultUserName:            "default-user",
			DefaultUserNameChanged:     moment(4),
			MaintenanceHistoryDays:     365,
			Color:                      "#FF0000",
			MasterKeyChanged:           moment(5),
			MasterKeyChangeRec:         90,
			MasterKeyChangeForce:       -2,
			MemoryProtection:           snapshot.MemoryProtection{Title: true, UserName: true, Password: true, URL: true, Notes: true},
			CustomIcons:                []snapshot.CustomIcon{{UUID: id(9), Data: "iVBORw0KGgo=", Name: "my icon", LastModification: moment(6)}},
			RecycleBinEnabled:          true,
			RecycleBin:                 id(3),
			RecycleBinChanged:          moment(7),
			EntryTemplatesGroup:        id(3),
			EntryTemplatesGroupChanged: moment(8),
			HistoryMaxItems:            25,
			HistoryMaxSize:             7340032,
			LastSelectedGroup:          id(3),
			LastTopVisibleGroup:        id(2),
			CustomData:                 data("meta"),
		},
		Root:        root,
		Deletions:   []snapshot.Deletion{{UUID: id(7), Time: moment(40)}},
		Attachments: map[snapshot.AttachmentID]snapshot.Attachment{attachmentID: {Content: attachment, Protected: true}},
	}
}

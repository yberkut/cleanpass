package gateway_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/testkit"
)

// richSnapshot opens the rich fixture, made by the real keepassxc-cli.
func richSnapshot(t *testing.T) (*snapshot.Snapshot, testkit.DB) {
	t.Helper()
	db := testkit.DB{Path: filepath.Join(t.TempDir(), "rich.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	snap, err := gateway.Open(db.Path, gateway.Credentials{Password: []byte(db.Password)})
	if err != nil {
		t.Fatal(err)
	}
	return snap, db
}

func at(t *testing.T, rfc3339 string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func wantTime(t *testing.T, name string, got time.Time, rfc3339 string) {
	t.Helper()
	if !got.Equal(at(t, rfc3339)) {
		t.Errorf("%s = %s, want %s", name, got, rfc3339)
	}
}

func wantEqual[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func wantBoolPointer(t *testing.T, name string, got *bool, want bool) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func field(t *testing.T, v *snapshot.EntryVersion, key string) snapshot.Field {
	t.Helper()
	for _, f := range v.Fields {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("no field %q", key)
	return snapshot.Field{}
}

func customValue(t *testing.T, items []snapshot.CustomDataItem, key string) string {
	t.Helper()
	for _, item := range items {
		if item.Key == key {
			return item.Value
		}
	}
	t.Fatalf("no custom data %q", key)
	return ""
}

// The UUIDs of the rich fixture.
var (
	rootUUID    = snapshot.UUID{15: 0x10}
	workUUID    = snapshot.UUID{15: 0x20}
	binUUID     = snapshot.UUID{15: 0x30}
	iconUUID    = snapshot.UUID{14: 0x09, 15: 0x01}
	mailUUID    = snapshot.UUID{14: 0x01, 15: 0x00}
	deletedUUID = snapshot.UUID{14: 0x07, 15: 0x77}
)

// The main version of an entry must carry everything the content fingerprint and the
// set of versions of the Merge Engine are built from.
func TestOpenCarriesEverythingOfAnEntryVersion(t *testing.T) {
	snap, _ := richSnapshot(t)
	mail := &snap.Root.Entries[0]

	wantEqual(t, "UUID", mail.UUID, mailUUID)
	wantEqual(t, "custom icon", mail.CustomIcon, iconUUID)
	wantEqual(t, "foreground", mail.ForegroundColor, "#112233")
	wantEqual(t, "background", mail.BackgroundColor, "#445566")
	wantEqual(t, "override URL", mail.OverrideURL, "cmd://calc")
	wantEqual(t, "tags", mail.Tags, "alpha,gamma,бета") // KeePassXC sorts tags on import
	wantBoolPointer(t, "quality check", mail.QualityCheck, false)
	if mail.PreviousParentGroup == nil || *mail.PreviousParentGroup != workUUID {
		t.Errorf("previous parent = %v, want %v", mail.PreviousParentGroup, workUUID)
	}

	wantTime(t, "last modification", mail.Times.LastModification, "2026-03-15T10:00:00Z")
	wantTime(t, "creation", mail.Times.Creation, "2026-01-01T10:00:02Z")
	wantTime(t, "last access", mail.Times.LastAccess, "2026-04-02T10:00:00Z")
	wantTime(t, "expiry", mail.Times.Expiry, "2030-01-01T10:00:02Z")
	wantTime(t, "location changed", mail.Times.LocationChanged, "2026-02-01T10:00:02Z")
	wantEqual(t, "expires", mail.Times.Expires, true)
	wantEqual(t, "usage count", mail.Times.UsageCount, 7)

	for _, want := range []snapshot.Field{
		{Key: "Title", Value: `Пошта <&> "quoted" 😀`},
		{Key: "UserName", Value: "user@example.com"},
		{Key: "Password", Value: "  SECRET-MARKER-поточний пароль  ", Protected: true},
		{Key: "URL", Value: "https://mail.example.com/login?a=1&b=2"},
		{Key: "Notes", Value: "рядок 1\n\n  рядок 3 з відступом\n"},
		{Key: "otp", Value: "otpauth://totp/Example:user?secret=JBSWY3DPEHPK3PXP&period=30&digits=6&issuer=Example", Protected: true},
		{Key: "KPEX_PASSKEY_PRIVATE_KEY_PEM", Value: "SECRET-MARKER-passkey", Protected: true},
		{Key: "KPEX_PASSKEY_USER_HANDLE", Value: "aGFuZGxl", Protected: true},
		{Key: "Власне поле", Value: "власне значення"},
		{Key: "EmptyField", Value: ""},
	} {
		wantEqual(t, "field "+want.Key, field(t, &mail.EntryVersion, want.Key), want)
	}

	wantEqual(t, "auto-type enabled", mail.AutoType.Enabled, true)
	wantEqual(t, "auto-type obfuscation", mail.AutoType.Obfuscation, 1)
	wantEqual(t, "auto-type sequence", mail.AutoType.DefaultSequence, "{PASSWORD}{TAB}")
	if want := []snapshot.AutoTypeAssociation{{Window: "*Firefox*", KeystrokeSequence: "{USERNAME}{TAB}{PASSWORD}"}}; !slices.Equal(mail.AutoType.Associations, want) {
		t.Errorf("auto-type associations = %v, want %v", mail.AutoType.Associations, want)
	}
	wantEqual(t, "browser data", customValue(t, mail.CustomData, "KPXC_BROWSER_SETTINGS"), `{"Allow":["example.com"]}`)
}

func TestOpenCarriesAllVersionsOfAnEntry(t *testing.T) {
	snap, _ := richSnapshot(t)
	mail := &snap.Root.Entries[0]
	if len(mail.History) != 1 {
		t.Fatalf("history has %d versions, want 1", len(mail.History))
	}
	old := &mail.History[0]
	wantEqual(t, "title", field(t, old, "Title").Value, "Пошта стара")
	wantEqual(t, "password", field(t, old, "Password"), snapshot.Field{Key: "Password", Value: "SECRET-MARKER-старий пароль", Protected: true})
	wantTime(t, "last modification", old.Times.LastModification, "2026-02-20T10:00:00Z")
	wantEqual(t, "usage count", old.Times.UsageCount, 3)
	wantEqual(t, "attachment name", old.Attachments[0].Name, "old.bin")
}

func TestOpenIdentifiesAttachmentsByContent(t *testing.T) {
	snap, db := richSnapshot(t)
	onMail := snap.Root.Entries[0].Attachments[0]
	onOther := snap.Root.Groups[0].Entries[0].Attachments[0]
	inHistory := snap.Root.Entries[0].History[0].Attachments[0]

	wantEqual(t, "name on the first entry", onMail.Name, "file.bin")
	wantEqual(t, "name on the second entry", onOther.Name, "same-content.bin")
	if onMail.ID != onOther.ID {
		t.Error("the same bytes under two names have different IDs")
	}
	if onMail.ID == inHistory.ID {
		t.Error("different bytes have the same ID")
	}
	wantEqual(t, "attachments in the store", len(snap.Attachments), 2)

	// The real KeePassXC is the judge of the bytes, because its XML export has none.
	exported := filepath.Join(t.TempDir(), "exported.bin")
	db.CLI(t, "attachment-export", "/Робота/Друге", "same-content.bin", exported)
	want, err := os.ReadFile(exported)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snap.Attachments[onOther.ID].Content, want) {
		t.Errorf("attachment bytes = %q, keepassxc-cli gives %q", snap.Attachments[onOther.ID].Content, want)
	}
	if onOther.ID != snapshot.NewAttachmentID(want) {
		t.Error("the ID is not the identity of the content")
	}
}

func TestOpenCarriesTheGroupTree(t *testing.T) {
	snap, _ := richSnapshot(t)
	root := &snap.Root
	wantEqual(t, "root UUID", root.UUID, rootUUID)
	wantEqual(t, "root name", root.Name, "Root")
	wantEqual(t, "root notes", root.Notes, "Кореневі нотатки")
	wantEqual(t, "root icon", root.IconID, 48)
	wantEqual(t, "root expanded", root.IsExpanded, true)
	wantEqual(t, "root sequence", root.DefaultAutoTypeSequence, "{USERNAME}{ENTER}")
	wantBoolPointer(t, "root auto-type", root.EnableAutoType, true)
	wantBoolPointer(t, "root searching", root.EnableSearching, false)
	wantEqual(t, "root top entry", root.LastTopVisibleEntry, mailUUID)
	wantEqual(t, "root custom data", customValue(t, root.CustomData, "group-custom"), "group-value")
	wantTime(t, "root last modification", root.Times.LastModification, "2026-03-01T10:00:01Z")
	wantTime(t, "root location changed", root.Times.LocationChanged, "2026-02-01T10:00:01Z")

	if len(root.Groups) != 2 {
		t.Fatalf("root has %d groups, want 2", len(root.Groups))
	}
	work, bin := &root.Groups[0], &root.Groups[1]
	wantEqual(t, "work UUID", work.UUID, workUUID)
	wantEqual(t, "work name", work.Name, "Робота")
	wantEqual(t, "work tags", work.Tags, "group-tag")
	if work.EnableAutoType != nil || work.EnableSearching != nil {
		t.Errorf("work inherits auto-type and searching, got %v and %v", work.EnableAutoType, work.EnableSearching)
	}
	if work.PreviousParentGroup == nil || *work.PreviousParentGroup != rootUUID {
		t.Errorf("work previous parent = %v, want %v", work.PreviousParentGroup, rootUUID)
	}
	wantEqual(t, "bin UUID", bin.UUID, binUUID)
	wantEqual(t, "bin entries", len(bin.Entries), 1)
}

func TestOpenCarriesTheMetadataAndDeletions(t *testing.T) {
	snap, _ := richSnapshot(t)
	meta := &snap.Meta
	wantEqual(t, "name", meta.DatabaseName, "Синтетична база")
	wantTime(t, "name changed", meta.DatabaseNameChanged, "2026-02-02T10:00:01Z")
	wantEqual(t, "description", meta.DatabaseDescription, "Опис бази")
	wantTime(t, "description changed", meta.DatabaseDescriptionChanged, "2026-02-02T10:00:02Z")
	wantEqual(t, "default user", meta.DefaultUserName, "default-user")
	wantTime(t, "default user changed", meta.DefaultUserNameChanged, "2026-02-02T10:00:03Z")
	wantEqual(t, "color", meta.Color, "#FF0000")
	wantTime(t, "master key changed", meta.MasterKeyChanged, "2026-02-03T10:00:00Z")
	wantEqual(t, "master key rec", meta.MasterKeyChangeRec, 90)
	wantEqual(t, "master key force", meta.MasterKeyChangeForce, -1)
	wantEqual(t, "maintenance days", meta.MaintenanceHistoryDays, 365)
	wantEqual(t, "protect password", meta.MemoryProtection.Password, true)
	wantEqual(t, "protect title", meta.MemoryProtection.Title, false)
	wantEqual(t, "recycle bin enabled", meta.RecycleBinEnabled, true)
	wantEqual(t, "recycle bin", meta.RecycleBin, binUUID)
	wantTime(t, "recycle bin changed", meta.RecycleBinChanged, "2026-02-05T10:00:00Z")
	wantTime(t, "templates changed", meta.EntryTemplatesGroupChanged, "2026-02-05T10:00:01Z")
	wantEqual(t, "selected group", meta.LastSelectedGroup, workUUID)
	wantEqual(t, "top group", meta.LastTopVisibleGroup, rootUUID)
	wantEqual(t, "history items", meta.HistoryMaxItems, 25)
	wantEqual(t, "history size", meta.HistoryMaxSize, 7340032)
	wantTime(t, "settings changed", meta.SettingsChanged, "2026-02-06T10:00:00Z")
	wantEqual(t, "custom data", customValue(t, meta.CustomData, "meta-custom-key"), "meta-custom-value")

	if len(meta.CustomIcons) != 1 {
		t.Fatalf("%d custom icons, want 1", len(meta.CustomIcons))
	}
	wantEqual(t, "icon UUID", meta.CustomIcons[0].UUID, iconUUID)
	wantEqual(t, "icon name", meta.CustomIcons[0].Name, "my icon")
	wantTime(t, "icon changed", meta.CustomIcons[0].LastModification, "2026-02-04T10:00:00Z")

	if len(snap.Deletions) != 1 {
		t.Fatalf("%d deletions, want 1", len(snap.Deletions))
	}
	wantEqual(t, "deleted UUID", snap.Deletions[0].UUID, deletedUUID)
	wantTime(t, "deletion time", snap.Deletions[0].Time, "2026-02-10T10:00:00Z")
}

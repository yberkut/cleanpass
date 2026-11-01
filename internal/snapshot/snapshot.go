// Package snapshot is the neutral model of one copy of a KeePass database.
//
// It depends only on the standard library. The KDBX Gateway fills it from a file,
// the Merge Engine and the Loss Audit read it, and the Gateway writes it back. So it
// carries everything a copy holds, not only what a report shows today.
//
// A Snapshot holds secret values in clear text. It must never reach the Report.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// UUID identifies an entry, a group or a custom icon. The zero value means "none".
type UUID [16]byte

// IsZero reports whether the UUID means "none".
func (u UUID) IsZero() bool { return u == UUID{} }

// String returns the UUID in hexadecimal. It is not a secret.
func (u UUID) String() string { return hex.EncodeToString(u[:]) }

// AttachmentID identifies an attachment by its content: the SHA-256 of its bytes.
// The same bytes always have the same ID, in any copy and under any file name.
type AttachmentID [sha256.Size]byte

// NewAttachmentID returns the ID of the given bytes.
func NewAttachmentID(content []byte) AttachmentID { return sha256.Sum256(content) }

// Snapshot is one copy of a database.
type Snapshot struct {
	Format Format
	Meta   Meta
	Root   Group
	// Deletions are the records about deleted objects, in file order.
	Deletions []Deletion
	// Attachments holds the bytes of every attachment that an entry version refers to.
	Attachments map[AttachmentID]Attachment
}

// Format says how the file is stored.
type Format struct {
	// Major and Minor are the KDBX version, for example 4 and 1.
	Major, Minor uint16
	Cipher       Cipher
	KDF          KDF
}

// Cipher is the outer encryption of the file.
type Cipher int

// The ciphers of KDBX.
const (
	CipherAES256 Cipher = iota + 1
	CipherChaCha20
	CipherTwofish
)

// KDFKind is the key derivation function of the file.
type KDFKind int

// The key derivation functions of KDBX.
const (
	KDFAES KDFKind = iota + 1
	KDFArgon2d
	KDFArgon2id
)

// KDF holds the kind and the cost of the key derivation.
type KDF struct {
	Kind KDFKind
	// Rounds is the cost of KDFAES.
	Rounds uint64
	// MemoryBytes, Iterations and Parallelism are the cost of the Argon2 kinds.
	MemoryBytes uint64
	Iterations  uint64
	Parallelism uint32
}

// Meta holds the settings of the database. The *Changed times decide which copy
// wins each setting in a merge.
type Meta struct {
	Generator                  string
	SettingsChanged            time.Time
	DatabaseName               string
	DatabaseNameChanged        time.Time
	DatabaseDescription        string
	DatabaseDescriptionChanged time.Time
	DefaultUserName            string
	DefaultUserNameChanged     time.Time
	MaintenanceHistoryDays     int64
	Color                      string
	MasterKeyChanged           time.Time
	MasterKeyChangeRec         int64
	MasterKeyChangeForce       int64
	MemoryProtection           MemoryProtection
	CustomIcons                []CustomIcon
	RecycleBinEnabled          bool
	RecycleBin                 UUID
	RecycleBinChanged          time.Time
	EntryTemplatesGroup        UUID
	EntryTemplatesGroupChanged time.Time
	HistoryMaxItems            int64
	HistoryMaxSize             int64
	LastSelectedGroup          UUID
	LastTopVisibleGroup        UUID
	CustomData                 []CustomDataItem
}

// MemoryProtection says which standard fields are protected in memory.
type MemoryProtection struct {
	Title, UserName, Password, URL, Notes bool
}

// CustomIcon is a custom icon of the database.
type CustomIcon struct {
	UUID UUID
	// Data is the image in base64, as the file stores it.
	Data             string
	Name             string
	LastModification time.Time
}

// CustomDataItem is one key and value of the custom data of the database, a group
// or an entry. LastModification is zero if the file has no such time.
type CustomDataItem struct {
	Key              string
	Value            string
	LastModification time.Time
}

// Keys of the service custom data that KeePassXC itself renews when it saves a file.
const (
	// CustomDataLastModified holds the time of the last change as text in the
	// language of the system.
	CustomDataLastModified = "_LAST_MODIFIED"
	// CustomDataRandomSlug holds random bytes that change on every save.
	CustomDataRandomSlug = "KPXC_RANDOM_SLUG"
)

// IsService reports whether KeePassXC itself renews this item when it saves a file.
func (c *CustomDataItem) IsService() bool {
	return c.Key == CustomDataLastModified || c.Key == CustomDataRandomSlug
}

// Deletion is a record that an object with this UUID was deleted.
type Deletion struct {
	UUID UUID
	Time time.Time
}

// Attachment is the content of an attachment.
type Attachment struct {
	Content []byte
	// Protected says the file marks these bytes as protected in memory.
	Protected bool
}

// Times are the time stamps of a group or an entry version. A zero time means the
// file has no such value.
type Times struct {
	Creation         time.Time
	LastModification time.Time
	LastAccess       time.Time
	Expiry           time.Time
	Expires          bool
	UsageCount       int64
	LocationChanged  time.Time
}

// Group is a group with its child groups and entries, in file order.
type Group struct {
	UUID                    UUID
	Name                    string
	Notes                   string
	IconID                  int64
	CustomIcon              UUID
	Times                   Times
	IsExpanded              bool
	DefaultAutoTypeSequence string
	// EnableAutoType and EnableSearching are nil when the group inherits the value.
	EnableAutoType      *bool
	EnableSearching     *bool
	LastTopVisibleEntry UUID
	PreviousParentGroup *UUID
	Tags                string
	CustomData          []CustomDataItem
	Entries             []Entry
	Groups              []Group
}

// Walk calls visit for this group and for every group below it, parents first.
func (g *Group) Walk(visit func(*Group)) {
	visit(g)
	for i := range g.Groups {
		g.Groups[i].Walk(visit)
	}
}

// Entry is an entry with its main version and its older versions.
type Entry struct {
	UUID UUID
	// EntryVersion is the main version of the entry.
	EntryVersion
	// History holds the older versions, in file order.
	History []EntryVersion
}

// EntryVersion is everything one version of an entry holds.
type EntryVersion struct {
	IconID              int64
	CustomIcon          UUID
	ForegroundColor     string
	BackgroundColor     string
	OverrideURL         string
	QualityCheck        *bool
	Tags                string
	PreviousParentGroup *UUID
	Times               Times
	// Fields holds all string fields in file order, also the empty ones.
	Fields      []Field
	Attachments []AttachmentRef
	AutoType    AutoType
	CustomData  []CustomDataItem
}

// Field is a string field of an entry. Value is in clear text.
type Field struct {
	Key       string
	Value     string
	Protected bool
}

// AttachmentRef is one attachment of an entry version: a file name and a content.
type AttachmentRef struct {
	Name string
	ID   AttachmentID
}

// AutoType holds the Auto-Type settings of an entry version.
type AutoType struct {
	Enabled         bool
	Obfuscation     int64
	DefaultSequence string
	Associations    []AutoTypeAssociation
}

// AutoTypeAssociation is a keystroke sequence for a window.
type AutoTypeAssociation struct {
	Window            string
	KeystrokeSequence string
}

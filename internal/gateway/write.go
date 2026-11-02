package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tobischo/gokeepasslib/v3"
	w "github.com/tobischo/gokeepasslib/v3/wrappers"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The errors of Write. They mean a bug in the caller or in this tool, not a problem with a
// copy, so nothing gets written.
var (
	// ErrAttachmentBytesMissing means a version refers to an attachment without bytes.
	ErrAttachmentBytesMissing = errors.New("the bytes of an attachment are missing")
	// ErrFormatUnknown means a cipher or a KDF the file format does not have.
	ErrFormatUnknown = errors.New("unknown cipher or KDF")
)

// The output is always KDBX 4.1: it can hold every field of a Snapshot, and KeePassXC reads it.
const (
	outputMajor = 4
	outputMinor = 1
)

// The cost for a copy that has no KDF of KDBX 4 to take over. The KeePassXC window sets the
// same memory and measures the iterations for about one second on the machine. A file has
// to work the same way on every machine, so the iterations are fixed. They take about half
// a second on a desktop PC.
const (
	defaultArgon2Memory      = 64 * 1024 * 1024
	defaultArgon2Iterations  = 30
	defaultArgon2Parallelism = 2
)

// The sizes of the random values.
const (
	masterSeedSize     = 32
	innerStreamKeySize = 64
	ivSizeCBC          = 16
	ivSizeChaCha20     = 12
)

// memoryProtectedFlag is the mark of a protected attachment in the inner header.
const memoryProtectedFlag = 1

const argon2FileVersion = argon2Version

// OutputFormat gives the format of a result whose keys come from a copy with the given
// format: its cipher and its KDF, and for a KDBX 3 copy the defaults of KeePassXC.
func OutputFormat(keysFrom snapshot.Format) snapshot.Format {
	result := snapshot.Format{Major: outputMajor, Minor: outputMinor, Cipher: keysFrom.Cipher, KDF: keysFrom.KDF}
	if keysFrom.Major != outputMajor {
		result.Cipher = snapshot.CipherAES256
		result.KDF = snapshot.KDF{
			Kind:        snapshot.KDFArgon2id,
			MemoryBytes: defaultArgon2Memory,
			Iterations:  defaultArgon2Iterations,
			Parallelism: defaultArgon2Parallelism,
		}
	}
	return result
}

// Write saves the Snapshot to out as a KDBX 4 file with the cipher and the KDF of format and
// with new random values. It leaves the Snapshot as it is.
func Write(out io.Writer, snap *snapshot.Snapshot, format snapshot.Format, creds Credentials) (err error) {
	// The library panics on some values. A failed write must end in an error, and what the
	// panic says stays out of it.
	defer func() {
		if recover() != nil {
			err = errors.New("the library panicked")
		}
	}()
	libCreds, err := newLibraryCredentials(creds)
	if err != nil {
		return err
	}
	db, err := toDatabase(snap, format)
	if err != nil {
		return err
	}
	db.Credentials = libCreds
	if err := freshen(db); err != nil {
		return err
	}
	// The new salt is a new challenge, so the response of the source file is of no use here.
	if err := addResponse(libCreds, creds.YubiKey, db.Header.FileHeaders.KdfParameters.Salt[:]); err != nil {
		return err
	}
	// The values are in clear text here. Encode first unlocks whatever it finds with the
	// current stream key, then locks again. So it needs values already locked with that key:
	// this Lock must come after the key is new, and it must not be left out.
	if err := db.LockProtectedEntries(); err != nil {
		return err
	}
	return gokeepasslib.NewEncoder(out).Encode(db)
}

// ReadBackError means a written file does not hold what it should. Place names a part of a
// database, for example Root.Entries[0].Fields[1].Value, and never a value.
type ReadBackError struct{ Place string }

func (e *ReadBackError) Error() string { return "the written file differs at " + e.Place }

// ReadBack reads a written file and compares it with what it should hold. It gives what the
// file really holds, so that the next check looks at the file and not at a promise.
func ReadBack(path string, want *snapshot.Snapshot, format snapshot.Format, creds Credentials) (*snapshot.Snapshot, error) {
	got, err := Open(path, creds)
	if err != nil {
		return nil, err
	}
	if got.Format != format {
		return nil, &ReadBackError{Place: "Format"}
	}
	if len(got.FieldLosses) > 0 {
		return nil, &ReadBackError{Place: "FieldLosses"}
	}
	if place := snapshot.Difference(want, got); place != "" {
		return nil, &ReadBackError{Place: place}
	}
	return got, nil
}

// freshen makes the random values KeePassXC also makes on every save. A new database of the
// library has its own, but what a file holds must not depend on the defaults of the library.
func freshen(db *gokeepasslib.Database) error {
	headers := db.Header.FileHeaders
	inner := db.Content.InnerHeader
	headers.MasterSeed = make([]byte, masterSeedSize)
	inner.InnerRandomStreamKey = make([]byte, innerStreamKeySize)
	inner.InnerRandomStreamID = gokeepasslib.ChaChaStreamID
	for _, part := range [][]byte{
		headers.MasterSeed, headers.EncryptionIV, headers.KdfParameters.Salt[:], inner.InnerRandomStreamKey,
	} {
		if _, err := rand.Read(part); err != nil {
			return err
		}
	}
	return nil
}

func toDatabase(snap *snapshot.Snapshot, format snapshot.Format) (*gokeepasslib.Database, error) {
	db := gokeepasslib.NewDatabase(gokeepasslib.WithDatabaseKDBXVersion41())
	if err := setFormat(db.Header.FileHeaders, format); err != nil {
		return nil, err
	}
	e := encoder{snap: snap, ids: map[snapshot.AttachmentID]int{}}
	root, err := e.group(&snap.Root)
	if err != nil {
		return nil, err
	}
	db.Content.Meta = metaToLibrary(&snap.Meta)
	db.Content.Root = &gokeepasslib.RootData{
		Groups:         []gokeepasslib.Group{root},
		DeletedObjects: deletionsToLibrary(snap.Deletions),
	}
	db.Content.InnerHeader.Binaries = e.binaries
	return db, nil
}

func setFormat(headers *gokeepasslib.FileHeaders, format snapshot.Format) error {
	switch format.Cipher {
	case snapshot.CipherAES256:
		headers.CipherID, headers.EncryptionIV = gokeepasslib.CipherAES, make([]byte, ivSizeCBC)
	case snapshot.CipherChaCha20:
		headers.CipherID, headers.EncryptionIV = gokeepasslib.CipherChaCha20, make([]byte, ivSizeChaCha20)
	case snapshot.CipherTwofish:
		headers.CipherID, headers.EncryptionIV = gokeepasslib.CipherTwoFish, make([]byte, ivSizeCBC)
	default:
		return ErrFormatUnknown
	}
	kdf := format.KDF
	argon2 := gokeepasslib.KdfParameters{
		Memory:      kdf.MemoryBytes,
		Iterations:  kdf.Iterations,
		Parallelism: kdf.Parallelism,
		Version:     argon2FileVersion,
	}
	switch kdf.Kind {
	case snapshot.KDFAES:
		headers.KdfParameters = &gokeepasslib.KdfParameters{UUID: gokeepasslib.KdfAES4, Rounds: kdf.Rounds}
	case snapshot.KDFArgon2d:
		argon2.UUID = gokeepasslib.KdfArgon2
		headers.KdfParameters = &argon2
	case snapshot.KDFArgon2id:
		argon2.UUID = gokeepasslib.KdfArgon2id
		headers.KdfParameters = &argon2
	default:
		return ErrFormatUnknown
	}
	return nil
}

func metaToLibrary(m *snapshot.Meta) *gokeepasslib.MetaData {
	return &gokeepasslib.MetaData{
		Generator:                  m.Generator,
		SettingsChanged:            timeToLibrary(m.SettingsChanged),
		DatabaseName:               m.DatabaseName,
		DatabaseNameChanged:        timeToLibrary(m.DatabaseNameChanged),
		DatabaseDescription:        m.DatabaseDescription,
		DatabaseDescriptionChanged: timeToLibrary(m.DatabaseDescriptionChanged),
		DefaultUserName:            m.DefaultUserName,
		DefaultUserNameChanged:     timeToLibrary(m.DefaultUserNameChanged),
		MaintenanceHistoryDays:     m.MaintenanceHistoryDays,
		Color:                      m.Color,
		MasterKeyChanged:           timeToLibrary(m.MasterKeyChanged),
		MasterKeyChangeRec:         m.MasterKeyChangeRec,
		MasterKeyChangeForce:       m.MasterKeyChangeForce,
		MemoryProtection: gokeepasslib.MemProtection{
			ProtectTitle:    w.NewBoolWrapper(m.MemoryProtection.Title),
			ProtectUserName: w.NewBoolWrapper(m.MemoryProtection.UserName),
			ProtectPassword: w.NewBoolWrapper(m.MemoryProtection.Password),
			ProtectURL:      w.NewBoolWrapper(m.MemoryProtection.URL),
			ProtectNotes:    w.NewBoolWrapper(m.MemoryProtection.Notes),
		},
		CustomIcons:                customIconsToLibrary(m.CustomIcons),
		RecycleBinEnabled:          w.NewBoolWrapper(m.RecycleBinEnabled),
		RecycleBinUUID:             gokeepasslib.UUID(m.RecycleBin),
		RecycleBinChanged:          timeToLibrary(m.RecycleBinChanged),
		EntryTemplatesGroup:        uuidToText(m.EntryTemplatesGroup),
		EntryTemplatesGroupChanged: timeToLibrary(m.EntryTemplatesGroupChanged),
		HistoryMaxItems:            m.HistoryMaxItems,
		HistoryMaxSize:             m.HistoryMaxSize,
		LastSelectedGroup:          uuidToText(m.LastSelectedGroup),
		LastTopVisibleGroup:        uuidToText(m.LastTopVisibleGroup),
		CustomData:                 customDataToLibrary(m.CustomData),
	}
}

func customIconsToLibrary(icons []snapshot.CustomIcon) []gokeepasslib.CustomIcon {
	result := make([]gokeepasslib.CustomIcon, 0, len(icons))
	for i := range icons {
		result = append(result, gokeepasslib.CustomIcon{
			UUID:                 gokeepasslib.UUID(icons[i].UUID),
			Data:                 icons[i].Data,
			Name:                 icons[i].Name,
			LastModificationTime: timeToLibrary(icons[i].LastModification),
		})
	}
	return result
}

func customDataToLibrary(items []snapshot.CustomDataItem) []gokeepasslib.CustomData {
	result := make([]gokeepasslib.CustomData, 0, len(items))
	for i := range items {
		result = append(result, gokeepasslib.CustomData{
			Key:                  items[i].Key,
			Value:                items[i].Value,
			LastModificationTime: timeToLibrary(items[i].LastModification),
		})
	}
	return result
}

func deletionsToLibrary(deletions []snapshot.Deletion) []gokeepasslib.DeletedObjectData {
	result := make([]gokeepasslib.DeletedObjectData, 0, len(deletions))
	for i := range deletions {
		// The library cannot write a record without a time, and the zero time is a valid
		// value in the file: it comes back as the zero time.
		result = append(result, gokeepasslib.DeletedObjectData{
			UUID:         gokeepasslib.UUID(deletions[i].UUID),
			DeletionTime: &w.TimeWrapper{Time: deletions[i].Time},
		})
	}
	return result
}

// encoder carries the state of one conversion from a Snapshot to the library. It gives every
// content one number, so the same bytes land in the file once, whatever the number of
// references.
type encoder struct {
	snap     *snapshot.Snapshot
	ids      map[snapshot.AttachmentID]int
	binaries gokeepasslib.Binaries
}

func (e *encoder) group(g *snapshot.Group) (gokeepasslib.Group, error) {
	result := gokeepasslib.Group{
		UUID:                    gokeepasslib.UUID(g.UUID),
		Name:                    g.Name,
		Notes:                   g.Notes,
		IconID:                  g.IconID,
		CustomIconUUID:          gokeepasslib.UUID(g.CustomIcon),
		Times:                   timesToLibrary(&g.Times),
		IsExpanded:              w.NewBoolWrapper(g.IsExpanded),
		DefaultAutoTypeSequence: g.DefaultAutoTypeSequence,
		EnableAutoType:          nullableToLibrary(g.EnableAutoType),
		EnableSearching:         nullableToLibrary(g.EnableSearching),
		LastTopVisibleEntry:     uuidToText(g.LastTopVisibleEntry),
		PreviousParentGroup:     uuidPointerToLibrary(g.PreviousParentGroup),
		Tags:                    g.Tags,
		CustomData:              customDataToLibrary(g.CustomData),
		Entries:                 make([]gokeepasslib.Entry, 0, len(g.Entries)),
		Groups:                  make([]gokeepasslib.Group, 0, len(g.Groups)),
	}
	for i := range g.Entries {
		entry, err := e.entry(&g.Entries[i])
		if err != nil {
			return result, err
		}
		result.Entries = append(result.Entries, entry)
	}
	for i := range g.Groups {
		child, err := e.group(&g.Groups[i])
		if err != nil {
			return result, err
		}
		result.Groups = append(result.Groups, child)
	}
	return result, nil
}

func (e *encoder) entry(entry *snapshot.Entry) (gokeepasslib.Entry, error) {
	result, err := e.version(entry.UUID, &entry.EntryVersion)
	if err != nil {
		return result, err
	}
	if len(entry.History) == 0 {
		return result, nil
	}
	older := make([]gokeepasslib.Entry, 0, len(entry.History))
	for i := range entry.History {
		version, err := e.version(entry.UUID, &entry.History[i])
		if err != nil {
			return result, err
		}
		older = append(older, version)
	}
	result.Histories = []gokeepasslib.History{{Entries: older}}
	return result, nil
}

func (e *encoder) version(id snapshot.UUID, v *snapshot.EntryVersion) (gokeepasslib.Entry, error) {
	result := gokeepasslib.Entry{
		UUID:                gokeepasslib.UUID(id),
		IconID:              v.IconID,
		CustomIconUUID:      gokeepasslib.UUID(v.CustomIcon),
		ForegroundColor:     v.ForegroundColor,
		BackgroundColor:     v.BackgroundColor,
		OverrideURL:         v.OverrideURL,
		QualityCheck:        boolPointerToLibrary(v.QualityCheck),
		Tags:                v.Tags,
		PreviousParentGroup: uuidPointerToLibrary(v.PreviousParentGroup),
		Times:               timesToLibrary(&v.Times),
		Values:              make([]gokeepasslib.ValueData, 0, len(v.Fields)),
		AutoType:            autoTypeToLibrary(&v.AutoType),
		CustomData:          customDataToLibrary(v.CustomData),
	}
	for i := range v.Fields {
		result.Values = append(result.Values, gokeepasslib.ValueData{
			Key: v.Fields[i].Key,
			Value: gokeepasslib.V{
				Content:   v.Fields[i].Value,
				Protected: w.NewBoolWrapper(v.Fields[i].Protected),
			},
		})
	}
	for i := range v.Attachments {
		number, err := e.attachmentNumber(v.Attachments[i].ID)
		if err != nil {
			return result, err
		}
		result.Binaries = append(result.Binaries, gokeepasslib.NewBinaryReference(v.Attachments[i].Name, number))
	}
	return result, nil
}

// attachmentNumber gives the number of the bytes in the file, and stores them on first use.
func (e *encoder) attachmentNumber(id snapshot.AttachmentID) (int, error) {
	if number, seen := e.ids[id]; seen {
		return number, nil
	}
	attachment, ok := e.snap.Attachments[id]
	if !ok {
		return 0, fmt.Errorf("%w: %x", ErrAttachmentBytesMissing, id[:4])
	}
	number := len(e.binaries)
	binary := gokeepasslib.Binary{ID: number, Content: attachment.Content, Compressed: w.NewBoolWrapper(false)}
	if attachment.Protected {
		binary.MemoryProtection = memoryProtectedFlag
	}
	e.binaries = append(e.binaries, binary)
	e.ids[id] = number
	return number, nil
}

func autoTypeToLibrary(a *snapshot.AutoType) gokeepasslib.AutoTypeData {
	result := gokeepasslib.AutoTypeData{
		Enabled:                 w.NewBoolWrapper(a.Enabled),
		DataTransferObfuscation: a.Obfuscation,
		DefaultSequence:         a.DefaultSequence,
	}
	for i := range a.Associations {
		result.Associations = append(result.Associations, gokeepasslib.AutoTypeAssociation{
			Window:            a.Associations[i].Window,
			KeystrokeSequence: a.Associations[i].KeystrokeSequence,
		})
	}
	return result
}

func timesToLibrary(t *snapshot.Times) gokeepasslib.TimeData {
	return gokeepasslib.TimeData{
		CreationTime:         timeToLibrary(t.Creation),
		LastModificationTime: timeToLibrary(t.LastModification),
		LastAccessTime:       timeToLibrary(t.LastAccess),
		ExpiryTime:           timeToLibrary(t.Expiry),
		Expires:              w.NewBoolWrapper(t.Expires),
		UsageCount:           t.UsageCount,
		LocationChanged:      timeToLibrary(t.LocationChanged),
	}
}

// timeToLibrary leaves a zero time out of the file, the way it came in.
func timeToLibrary(t time.Time) *w.TimeWrapper {
	if t.IsZero() {
		return nil
	}
	return &w.TimeWrapper{Time: t}
}

func nullableToLibrary(b *bool) w.NullableBoolWrapper {
	if b == nil {
		return w.NullableBoolWrapper{}
	}
	return w.NewNullableBoolWrapper(*b)
}

func boolPointerToLibrary(b *bool) *w.BoolWrapper {
	if b == nil {
		return nil
	}
	value := w.NewBoolWrapper(*b)
	return &value
}

func uuidPointerToLibrary(u *snapshot.UUID) *gokeepasslib.UUID {
	if u == nil {
		return nil
	}
	value := gokeepasslib.UUID(*u)
	return &value
}

// uuidToText gives the text of a UUID the library keeps as base64. The KDBX schema wants a
// value in these elements even when they point at nothing.
func uuidToText(id snapshot.UUID) string {
	if id.IsZero() {
		return gokeepasslib.ZeroUUIDText
	}
	return base64.StdEncoding.EncodeToString(id[:])
}

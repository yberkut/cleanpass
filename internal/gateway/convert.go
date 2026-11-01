package gateway

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/tobischo/gokeepasslib/v3"
	w "github.com/tobischo/gokeepasslib/v3/wrappers"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// converter carries the state of one conversion from the library to a Snapshot.
type converter struct {
	db          *gokeepasslib.Database
	attachments map[snapshot.AttachmentID]snapshot.Attachment
}

// fromDatabase makes a Snapshot of an unlocked database.
func fromDatabase(db *gokeepasslib.Database) (*snapshot.Snapshot, error) {
	if len(db.Content.Root.Groups) != 1 {
		return nil, fmt.Errorf("%w: %d root groups", ErrCorrupt, len(db.Content.Root.Groups))
	}
	format, err := formatOf(db)
	if err != nil {
		return nil, err
	}
	c := converter{db: db, attachments: map[snapshot.AttachmentID]snapshot.Attachment{}}
	root, err := c.group(&db.Content.Root.Groups[0])
	if err != nil {
		return nil, err
	}
	return &snapshot.Snapshot{
		Format:      format,
		Meta:        metaOf(db.Content.Meta),
		Root:        root,
		Deletions:   deletionsOf(db.Content.Root.DeletedObjects),
		Attachments: c.attachments,
	}, nil
}

func formatOf(db *gokeepasslib.Database) (snapshot.Format, error) {
	format := snapshot.Format{
		Major: db.Header.Signature.MajorVersion,
		Minor: db.Header.Signature.MinorVersion,
	}
	headers := db.Header.FileHeaders
	switch {
	case bytes.Equal(headers.CipherID, gokeepasslib.CipherAES):
		format.Cipher = snapshot.CipherAES256
	case bytes.Equal(headers.CipherID, gokeepasslib.CipherChaCha20):
		format.Cipher = snapshot.CipherChaCha20
	case bytes.Equal(headers.CipherID, gokeepasslib.CipherTwoFish):
		format.Cipher = snapshot.CipherTwofish
	default:
		return format, fmt.Errorf("%w: unknown cipher", ErrCorrupt)
	}

	if !db.Header.IsKdbx4() {
		format.KDF = snapshot.KDF{Kind: snapshot.KDFAES, Rounds: headers.TransformRounds}
		return format, nil
	}
	params := headers.KdfParameters
	if params == nil {
		return format, fmt.Errorf("%w: no KDF parameters", ErrCorrupt)
	}
	argon := snapshot.KDF{
		MemoryBytes: params.Memory,
		Iterations:  params.Iterations,
		Parallelism: params.Parallelism,
	}
	switch {
	case bytes.Equal(params.UUID, gokeepasslib.KdfAES4), bytes.Equal(params.UUID, gokeepasslib.KdfAES3):
		format.KDF = snapshot.KDF{Kind: snapshot.KDFAES, Rounds: params.Rounds}
	case bytes.Equal(params.UUID, gokeepasslib.KdfArgon2):
		argon.Kind = snapshot.KDFArgon2d
		format.KDF = argon
	case bytes.Equal(params.UUID, gokeepasslib.KdfArgon2id):
		argon.Kind = snapshot.KDFArgon2id
		format.KDF = argon
	default:
		return format, fmt.Errorf("%w: unknown KDF", ErrCorrupt)
	}
	return format, nil
}

func metaOf(m *gokeepasslib.MetaData) snapshot.Meta {
	return snapshot.Meta{
		Generator:                  m.Generator,
		SettingsChanged:            timeOf(m.SettingsChanged),
		DatabaseName:               m.DatabaseName,
		DatabaseNameChanged:        timeOf(m.DatabaseNameChanged),
		DatabaseDescription:        m.DatabaseDescription,
		DatabaseDescriptionChanged: timeOf(m.DatabaseDescriptionChanged),
		DefaultUserName:            m.DefaultUserName,
		DefaultUserNameChanged:     timeOf(m.DefaultUserNameChanged),
		MaintenanceHistoryDays:     m.MaintenanceHistoryDays,
		Color:                      m.Color,
		MasterKeyChanged:           timeOf(m.MasterKeyChanged),
		MasterKeyChangeRec:         m.MasterKeyChangeRec,
		MasterKeyChangeForce:       m.MasterKeyChangeForce,
		MemoryProtection: snapshot.MemoryProtection{
			Title:    m.MemoryProtection.ProtectTitle.Bool,
			UserName: m.MemoryProtection.ProtectUserName.Bool,
			Password: m.MemoryProtection.ProtectPassword.Bool,
			URL:      m.MemoryProtection.ProtectURL.Bool,
			Notes:    m.MemoryProtection.ProtectNotes.Bool,
		},
		CustomIcons:                customIconsOf(m.CustomIcons),
		RecycleBinEnabled:          m.RecycleBinEnabled.Bool,
		RecycleBin:                 snapshot.UUID(m.RecycleBinUUID),
		RecycleBinChanged:          timeOf(m.RecycleBinChanged),
		EntryTemplatesGroup:        uuidFromText(m.EntryTemplatesGroup),
		EntryTemplatesGroupChanged: timeOf(m.EntryTemplatesGroupChanged),
		HistoryMaxItems:            m.HistoryMaxItems,
		HistoryMaxSize:             m.HistoryMaxSize,
		LastSelectedGroup:          uuidFromText(m.LastSelectedGroup),
		LastTopVisibleGroup:        uuidFromText(m.LastTopVisibleGroup),
		CustomData:                 customDataOf(m.CustomData),
	}
}

func customIconsOf(icons []gokeepasslib.CustomIcon) []snapshot.CustomIcon {
	result := make([]snapshot.CustomIcon, 0, len(icons))
	for i := range icons {
		result = append(result, snapshot.CustomIcon{
			UUID:             snapshot.UUID(icons[i].UUID),
			Data:             icons[i].Data,
			Name:             icons[i].Name,
			LastModification: timeOf(icons[i].LastModificationTime),
		})
	}
	return result
}

func customDataOf(items []gokeepasslib.CustomData) []snapshot.CustomDataItem {
	result := make([]snapshot.CustomDataItem, 0, len(items))
	for i := range items {
		result = append(result, snapshot.CustomDataItem{
			Key:              items[i].Key,
			Value:            items[i].Value,
			LastModification: timeOf(items[i].LastModificationTime),
		})
	}
	return result
}

func deletionsOf(objects []gokeepasslib.DeletedObjectData) []snapshot.Deletion {
	result := make([]snapshot.Deletion, 0, len(objects))
	for i := range objects {
		result = append(result, snapshot.Deletion{
			UUID: snapshot.UUID(objects[i].UUID),
			Time: timeOf(objects[i].DeletionTime),
		})
	}
	return result
}

func (c *converter) group(g *gokeepasslib.Group) (snapshot.Group, error) {
	result := snapshot.Group{
		UUID:                    snapshot.UUID(g.UUID),
		Name:                    g.Name,
		Notes:                   g.Notes,
		IconID:                  g.IconID,
		CustomIcon:              snapshot.UUID(g.CustomIconUUID),
		Times:                   timesOf(&g.Times),
		IsExpanded:              g.IsExpanded.Bool,
		DefaultAutoTypeSequence: g.DefaultAutoTypeSequence,
		EnableAutoType:          nullableOf(g.EnableAutoType),
		EnableSearching:         nullableOf(g.EnableSearching),
		LastTopVisibleEntry:     uuidFromText(g.LastTopVisibleEntry),
		PreviousParentGroup:     uuidPointerOf(g.PreviousParentGroup),
		Tags:                    g.Tags,
		CustomData:              customDataOf(g.CustomData),
		Entries:                 make([]snapshot.Entry, 0, len(g.Entries)),
		Groups:                  make([]snapshot.Group, 0, len(g.Groups)),
	}
	for i := range g.Entries {
		entry, err := c.entry(&g.Entries[i])
		if err != nil {
			return result, err
		}
		result.Entries = append(result.Entries, entry)
	}
	for i := range g.Groups {
		child, err := c.group(&g.Groups[i])
		if err != nil {
			return result, err
		}
		result.Groups = append(result.Groups, child)
	}
	return result, nil
}

func (c *converter) entry(e *gokeepasslib.Entry) (snapshot.Entry, error) {
	version, err := c.version(e)
	if err != nil {
		return snapshot.Entry{}, err
	}
	result := snapshot.Entry{UUID: snapshot.UUID(e.UUID), EntryVersion: version}
	if err := c.appendHistory(&result.History, e); err != nil {
		return result, err
	}
	return result, nil
}

// appendHistory flattens all History blocks of e, so that no version is left behind
// even in a file that nests them.
func (c *converter) appendHistory(history *[]snapshot.EntryVersion, e *gokeepasslib.Entry) error {
	for i := range e.Histories {
		for j := range e.Histories[i].Entries {
			old := &e.Histories[i].Entries[j]
			version, err := c.version(old)
			if err != nil {
				return err
			}
			*history = append(*history, version)
			if err := c.appendHistory(history, old); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *converter) version(e *gokeepasslib.Entry) (snapshot.EntryVersion, error) {
	result := snapshot.EntryVersion{
		IconID:              e.IconID,
		CustomIcon:          snapshot.UUID(e.CustomIconUUID),
		ForegroundColor:     e.ForegroundColor,
		BackgroundColor:     e.BackgroundColor,
		OverrideURL:         e.OverrideURL,
		QualityCheck:        boolPointerOf(e.QualityCheck),
		Tags:                e.Tags,
		PreviousParentGroup: uuidPointerOf(e.PreviousParentGroup),
		Times:               timesOf(&e.Times),
		Fields:              make([]snapshot.Field, 0, len(e.Values)),
		AutoType:            autoTypeOf(&e.AutoType),
		CustomData:          customDataOf(e.CustomData),
	}
	for i := range e.Values {
		result.Fields = append(result.Fields, snapshot.Field{
			Key:       e.Values[i].Key,
			Value:     e.Values[i].Value.Content,
			Protected: e.Values[i].Value.Protected.Bool,
		})
	}
	for i := range e.Binaries {
		ref, err := c.attachment(&e.Binaries[i])
		if err != nil {
			return result, err
		}
		result.Attachments = append(result.Attachments, ref)
	}
	return result, nil
}

// attachment stores the bytes under their content ID and returns the reference.
func (c *converter) attachment(ref *gokeepasslib.BinaryReference) (snapshot.AttachmentRef, error) {
	binary := c.db.FindBinary(ref.Value.ID)
	if binary == nil {
		return snapshot.AttachmentRef{}, fmt.Errorf("%w: attachment %d is missing", ErrCorrupt, ref.Value.ID)
	}
	content, err := attachmentBytes(c.db, binary)
	if err != nil {
		return snapshot.AttachmentRef{}, fmt.Errorf("%w: attachment %d: %w", ErrCorrupt, ref.Value.ID, err)
	}
	id := snapshot.NewAttachmentID(content)
	stored := c.attachments[id]
	stored.Content = content
	stored.Protected = stored.Protected || binary.MemoryProtection != 0 ||
		(binary.Protected != nil && binary.Protected.Bool)
	c.attachments[id] = stored
	return snapshot.AttachmentRef{Name: ref.Name, ID: id}, nil
}

// attachmentBytes gives the true bytes of an attachment. KDBX 4 stores them as they are.
// GetContentBytes does not look at the format: it decodes any content that happens to be
// valid base64, so a text file "abcd" would turn into three other bytes and nothing would say so.
func attachmentBytes(db *gokeepasslib.Database, binary *gokeepasslib.Binary) ([]byte, error) {
	if db.Header.IsKdbx4() {
		return binary.Content, nil
	}
	return binary.GetContentBytes()
}

func autoTypeOf(a *gokeepasslib.AutoTypeData) snapshot.AutoType {
	result := snapshot.AutoType{
		Enabled:         a.Enabled.Bool,
		Obfuscation:     a.DataTransferObfuscation,
		DefaultSequence: a.DefaultSequence,
	}
	for i := range a.Associations {
		result.Associations = append(result.Associations, snapshot.AutoTypeAssociation{
			Window:            a.Associations[i].Window,
			KeystrokeSequence: a.Associations[i].KeystrokeSequence,
		})
	}
	return result
}

func timesOf(t *gokeepasslib.TimeData) snapshot.Times {
	return snapshot.Times{
		Creation:         timeOf(t.CreationTime),
		LastModification: timeOf(t.LastModificationTime),
		LastAccess:       timeOf(t.LastAccessTime),
		Expiry:           timeOf(t.ExpiryTime),
		Expires:          t.Expires.Bool,
		UsageCount:       t.UsageCount,
		LocationChanged:  timeOf(t.LocationChanged),
	}
}

// timeOf gives a time in UTC. A file of KDBX 4 cannot hold parts of a second, so a time from
// KDBX 3 with parts of a second would come back from a save as another time.
func timeOf(t *w.TimeWrapper) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.Time.UTC().Truncate(time.Second)
}

func nullableOf(b w.NullableBoolWrapper) *bool {
	if !b.Valid {
		return nil
	}
	value := b.Bool
	return &value
}

func boolPointerOf(b *w.BoolWrapper) *bool {
	if b == nil {
		return nil
	}
	value := b.Bool
	return &value
}

func uuidPointerOf(u *gokeepasslib.UUID) *snapshot.UUID {
	if u == nil {
		return nil
	}
	value := snapshot.UUID(*u)
	return &value
}

// uuidFromText reads a UUID that the library keeps as base64 text. These elements
// only remember the view in the KeePassXC window, so a bad value counts as "none".
func uuidFromText(text string) snapshot.UUID {
	var id snapshot.UUID
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil || len(decoded) != len(id) {
		return snapshot.UUID{}
	}
	copy(id[:], decoded)
	return id
}

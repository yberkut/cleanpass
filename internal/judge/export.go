package judge

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"runtime"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// This file reads what keepassxc-cli export prints. It is a reader of its own on purpose: the
// Judge does not trust the Gateway, so it shares no code with it for this.
//
// The text holds protected values in clear text. It stays in memory, and no error here says
// anything about it. An error of the XML package can quote the text, so none of them gets out.

var errExportBroken = errors.New("the export cannot be read")

type exportFile struct {
	Meta exportMeta `xml:"Meta"`
	Root struct {
		Groups    []exportGroup    `xml:"Group"`
		Deletions []exportDeletion `xml:"DeletedObjects>DeletedObject"`
	} `xml:"Root"`
}

type exportMeta struct {
	Generator                  string `xml:"Generator"`
	SettingsChanged            string `xml:"SettingsChanged"`
	DatabaseName               string `xml:"DatabaseName"`
	DatabaseNameChanged        string `xml:"DatabaseNameChanged"`
	DatabaseDescription        string `xml:"DatabaseDescription"`
	DatabaseDescriptionChanged string `xml:"DatabaseDescriptionChanged"`
	DefaultUserName            string `xml:"DefaultUserName"`
	DefaultUserNameChanged     string `xml:"DefaultUserNameChanged"`
	MaintenanceHistoryDays     int64  `xml:"MaintenanceHistoryDays"`
	Color                      string `xml:"Color"`
	MasterKeyChanged           string `xml:"MasterKeyChanged"`
	MasterKeyChangeRec         int64  `xml:"MasterKeyChangeRec"`
	MasterKeyChangeForce       int64  `xml:"MasterKeyChangeForce"`
	MemoryProtection           struct {
		ProtectTitle    string `xml:"ProtectTitle"`
		ProtectUserName string `xml:"ProtectUserName"`
		ProtectPassword string `xml:"ProtectPassword"`
		ProtectURL      string `xml:"ProtectURL"`
		ProtectNotes    string `xml:"ProtectNotes"`
	} `xml:"MemoryProtection"`
	CustomIcons                []exportIcon `xml:"CustomIcons>Icon"`
	RecycleBinEnabled          string       `xml:"RecycleBinEnabled"`
	RecycleBinUUID             string       `xml:"RecycleBinUUID"`
	RecycleBinChanged          string       `xml:"RecycleBinChanged"`
	EntryTemplatesGroup        string       `xml:"EntryTemplatesGroup"`
	EntryTemplatesGroupChanged string       `xml:"EntryTemplatesGroupChanged"`
	HistoryMaxItems            int64        `xml:"HistoryMaxItems"`
	HistoryMaxSize             int64        `xml:"HistoryMaxSize"`
	LastSelectedGroup          string       `xml:"LastSelectedGroup"`
	LastTopVisibleGroup        string       `xml:"LastTopVisibleGroup"`
	CustomData                 []exportItem `xml:"CustomData>Item"`
}

type exportIcon struct {
	UUID                 string `xml:"UUID"`
	Data                 string `xml:"Data"`
	Name                 string `xml:"Name"`
	LastModificationTime string `xml:"LastModificationTime"`
}

type exportItem struct {
	Key                  string `xml:"Key"`
	Value                string `xml:"Value"`
	LastModificationTime string `xml:"LastModificationTime"`
}

type exportDeletion struct {
	UUID         string `xml:"UUID"`
	DeletionTime string `xml:"DeletionTime"`
}

type exportTimes struct {
	Creation         string `xml:"CreationTime"`
	LastModification string `xml:"LastModificationTime"`
	LastAccess       string `xml:"LastAccessTime"`
	Expiry           string `xml:"ExpiryTime"`
	Expires          string `xml:"Expires"`
	UsageCount       int64  `xml:"UsageCount"`
	LocationChanged  string `xml:"LocationChanged"`
}

type exportGroup struct {
	UUID                    string        `xml:"UUID"`
	Name                    string        `xml:"Name"`
	Notes                   string        `xml:"Notes"`
	IconID                  int64         `xml:"IconID"`
	CustomIconUUID          string        `xml:"CustomIconUUID"`
	Times                   exportTimes   `xml:"Times"`
	IsExpanded              string        `xml:"IsExpanded"`
	DefaultAutoTypeSequence string        `xml:"DefaultAutoTypeSequence"`
	EnableAutoType          string        `xml:"EnableAutoType"`
	EnableSearching         string        `xml:"EnableSearching"`
	LastTopVisibleEntry     string        `xml:"LastTopVisibleEntry"`
	PreviousParentGroup     string        `xml:"PreviousParentGroup"`
	Tags                    string        `xml:"Tags"`
	CustomData              []exportItem  `xml:"CustomData>Item"`
	Entries                 []exportEntry `xml:"Entry"`
	Groups                  []exportGroup `xml:"Group"`
}

type exportEntry struct {
	UUID                string       `xml:"UUID"`
	IconID              int64        `xml:"IconID"`
	CustomIconUUID      string       `xml:"CustomIconUUID"`
	ForegroundColor     string       `xml:"ForegroundColor"`
	BackgroundColor     string       `xml:"BackgroundColor"`
	OverrideURL         string       `xml:"OverrideURL"`
	QualityCheck        *string      `xml:"QualityCheck"`
	Tags                string       `xml:"Tags"`
	PreviousParentGroup string       `xml:"PreviousParentGroup"`
	Times               exportTimes  `xml:"Times"`
	Strings             []exportText `xml:"String"`
	Binaries            []struct {
		Key string `xml:"Key"`
	} `xml:"Binary"`
	AutoType struct {
		Enabled                 string `xml:"Enabled"`
		DataTransferObfuscation int64  `xml:"DataTransferObfuscation"`
		DefaultSequence         string `xml:"DefaultSequence"`
		Associations            []struct {
			Window            string `xml:"Window"`
			KeystrokeSequence string `xml:"KeystrokeSequence"`
		} `xml:"Association"`
	} `xml:"AutoType"`
	CustomData []exportItem  `xml:"CustomData>Item"`
	History    []exportEntry `xml:"History>Entry"`
}

type exportText struct {
	Key   string `xml:"Key"`
	Value struct {
		Text            string `xml:",chardata"`
		ProtectInMemory string `xml:"ProtectInMemory,attr"`
		Protected       string `xml:"Protected,attr"`
	} `xml:"Value"`
}

// parseExport turns the text of an export into a Snapshot without the bytes of attachments: the
// export does not hold them.
func parseExport(raw []byte) (*snapshot.Snapshot, error) {
	// A standard stream of a Windows program turns each line end into two characters, also
	// inside a value. Without this step the parser would see one line break more.
	if runtime.GOOS == "windows" {
		raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	}
	var file exportFile
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var start xml.StartElement
	for start.Name.Local == "" {
		token, err := decoder.Token()
		if err != nil {
			return nil, errExportBroken
		}
		if element, isStart := token.(xml.StartElement); isStart {
			start = element
		}
	}
	if start.Name.Local != "KeePassFile" || decoder.DecodeElement(&file, &start) != nil || len(file.Root.Groups) != 1 {
		return nil, errExportBroken
	}
	reader := exportReader{}
	result := &snapshot.Snapshot{
		Meta:      reader.meta(&file.Meta),
		Root:      reader.group(&file.Root.Groups[0]),
		Deletions: reader.deletions(file.Root.Deletions),
	}
	if reader.broken {
		return nil, errExportBroken
	}
	return result, nil
}

// exportReader turns text into values and remembers whether any text was not what it should be.
type exportReader struct{ broken bool }

// dotNetEpochOffset is the number of seconds from year 1, where times of KDBX 4 start, to 1970.
const dotNetEpochOffset = 62135596800

// time reads a time in the form of KDBX 4 (seconds from year 1, in base64) or the form KeePassXC
// prints for a file of the old format. An empty text means no time.
func (r *exportReader) time(text string) time.Time {
	if text == "" {
		return time.Time{}
	}
	if decoded, err := base64.StdEncoding.DecodeString(text); err == nil && len(decoded) == 8 {
		return time.Unix(int64(binary.LittleEndian.Uint64(decoded))-dotNetEpochOffset, 0).UTC()
	}
	moment, err := time.Parse(time.RFC3339, text)
	if err != nil {
		r.broken = true
		return time.Time{}
	}
	return moment.UTC()
}

func (r *exportReader) uuid(text string) snapshot.UUID {
	var id snapshot.UUID
	if text == "" {
		return id
	}
	decoded, err := base64.StdEncoding.DecodeString(text)
	if err != nil || len(decoded) != len(id) {
		r.broken = true
		return id
	}
	copy(id[:], decoded)
	return id
}

func (r *exportReader) uuidPointer(text string) *snapshot.UUID {
	if text == "" {
		return nil
	}
	id := r.uuid(text)
	return &id
}

func (r *exportReader) bool(text string) bool {
	switch strings.ToLower(text) {
	case "true":
		return true
	case "false", "":
		return false
	}
	r.broken = true
	return false
}

// triState reads the setting of a group that can come from its parent.
func (r *exportReader) triState(text string) *bool {
	if text == "" || strings.EqualFold(text, "null") {
		return nil
	}
	value := r.bool(text)
	return &value
}

func (r *exportReader) meta(m *exportMeta) snapshot.Meta {
	result := snapshot.Meta{
		Generator:                  m.Generator,
		SettingsChanged:            r.time(m.SettingsChanged),
		DatabaseName:               m.DatabaseName,
		DatabaseNameChanged:        r.time(m.DatabaseNameChanged),
		DatabaseDescription:        m.DatabaseDescription,
		DatabaseDescriptionChanged: r.time(m.DatabaseDescriptionChanged),
		DefaultUserName:            m.DefaultUserName,
		DefaultUserNameChanged:     r.time(m.DefaultUserNameChanged),
		MaintenanceHistoryDays:     m.MaintenanceHistoryDays,
		Color:                      m.Color,
		MasterKeyChanged:           r.time(m.MasterKeyChanged),
		MasterKeyChangeRec:         m.MasterKeyChangeRec,
		MasterKeyChangeForce:       m.MasterKeyChangeForce,
		MemoryProtection: snapshot.MemoryProtection{
			Title:    r.bool(m.MemoryProtection.ProtectTitle),
			UserName: r.bool(m.MemoryProtection.ProtectUserName),
			Password: r.bool(m.MemoryProtection.ProtectPassword),
			URL:      r.bool(m.MemoryProtection.ProtectURL),
			Notes:    r.bool(m.MemoryProtection.ProtectNotes),
		},
		RecycleBinEnabled:          r.bool(m.RecycleBinEnabled),
		RecycleBin:                 r.uuid(m.RecycleBinUUID),
		RecycleBinChanged:          r.time(m.RecycleBinChanged),
		EntryTemplatesGroup:        r.uuid(m.EntryTemplatesGroup),
		EntryTemplatesGroupChanged: r.time(m.EntryTemplatesGroupChanged),
		HistoryMaxItems:            m.HistoryMaxItems,
		HistoryMaxSize:             m.HistoryMaxSize,
		LastSelectedGroup:          r.uuid(m.LastSelectedGroup),
		LastTopVisibleGroup:        r.uuid(m.LastTopVisibleGroup),
		CustomData:                 r.items(m.CustomData),
	}
	for i := range m.CustomIcons {
		icon := &m.CustomIcons[i]
		result.CustomIcons = append(result.CustomIcons, snapshot.CustomIcon{
			UUID:             r.uuid(icon.UUID),
			Data:             icon.Data,
			Name:             icon.Name,
			LastModification: r.time(icon.LastModificationTime),
		})
	}
	return result
}

func (r *exportReader) items(items []exportItem) []snapshot.CustomDataItem {
	var result []snapshot.CustomDataItem
	for i := range items {
		result = append(result, snapshot.CustomDataItem{
			Key:              items[i].Key,
			Value:            items[i].Value,
			LastModification: r.time(items[i].LastModificationTime),
		})
	}
	return result
}

func (r *exportReader) deletions(deletions []exportDeletion) []snapshot.Deletion {
	var result []snapshot.Deletion
	for i := range deletions {
		result = append(result, snapshot.Deletion{UUID: r.uuid(deletions[i].UUID), Time: r.time(deletions[i].DeletionTime)})
	}
	return result
}

func (r *exportReader) times(t *exportTimes) snapshot.Times {
	return snapshot.Times{
		Creation:         r.time(t.Creation),
		LastModification: r.time(t.LastModification),
		LastAccess:       r.time(t.LastAccess),
		Expiry:           r.time(t.Expiry),
		Expires:          r.bool(t.Expires),
		UsageCount:       t.UsageCount,
		LocationChanged:  r.time(t.LocationChanged),
	}
}

func (r *exportReader) group(g *exportGroup) snapshot.Group {
	result := snapshot.Group{
		UUID:                    r.uuid(g.UUID),
		Name:                    g.Name,
		Notes:                   g.Notes,
		IconID:                  g.IconID,
		CustomIcon:              r.uuid(g.CustomIconUUID),
		Times:                   r.times(&g.Times),
		IsExpanded:              r.bool(g.IsExpanded),
		DefaultAutoTypeSequence: g.DefaultAutoTypeSequence,
		EnableAutoType:          r.triState(g.EnableAutoType),
		EnableSearching:         r.triState(g.EnableSearching),
		LastTopVisibleEntry:     r.uuid(g.LastTopVisibleEntry),
		PreviousParentGroup:     r.uuidPointer(g.PreviousParentGroup),
		Tags:                    g.Tags,
		CustomData:              r.items(g.CustomData),
	}
	for i := range g.Entries {
		result.Entries = append(result.Entries, r.entry(&g.Entries[i]))
	}
	for i := range g.Groups {
		result.Groups = append(result.Groups, r.group(&g.Groups[i]))
	}
	return result
}

func (r *exportReader) entry(e *exportEntry) snapshot.Entry {
	result := snapshot.Entry{UUID: r.uuid(e.UUID), EntryVersion: r.version(e)}
	for i := range e.History {
		// A version takes the UUID of its entry. Another one would be another entry's version.
		if r.uuid(e.History[i].UUID) != result.UUID {
			r.broken = true
		}
		result.History = append(result.History, r.version(&e.History[i]))
	}
	return result
}

func (r *exportReader) version(e *exportEntry) snapshot.EntryVersion {
	result := snapshot.EntryVersion{
		IconID:              e.IconID,
		CustomIcon:          r.uuid(e.CustomIconUUID),
		ForegroundColor:     e.ForegroundColor,
		BackgroundColor:     e.BackgroundColor,
		OverrideURL:         e.OverrideURL,
		Tags:                e.Tags,
		PreviousParentGroup: r.uuidPointer(e.PreviousParentGroup),
		Times:               r.times(&e.Times),
		AutoType: snapshot.AutoType{
			Enabled:         r.bool(e.AutoType.Enabled),
			Obfuscation:     e.AutoType.DataTransferObfuscation,
			DefaultSequence: e.AutoType.DefaultSequence,
		},
		CustomData: r.items(e.CustomData),
	}
	if e.QualityCheck != nil {
		value := r.bool(*e.QualityCheck)
		result.QualityCheck = &value
	}
	for i := range e.Strings {
		text := &e.Strings[i]
		result.Fields = append(result.Fields, snapshot.Field{
			Key:       text.Key,
			Value:     text.Value.Text,
			Protected: r.bool(text.Value.ProtectInMemory) || r.bool(text.Value.Protected),
		})
	}
	for i := range e.Binaries {
		result.Attachments = append(result.Attachments, snapshot.AttachmentRef{Name: e.Binaries[i].Key})
	}
	for i := range e.AutoType.Associations {
		association := &e.AutoType.Associations[i]
		result.AutoType.Associations = append(result.AutoType.Associations, snapshot.AutoTypeAssociation{
			Window:            association.Window,
			KeystrokeSequence: association.KeystrokeSequence,
		})
	}
	return result
}

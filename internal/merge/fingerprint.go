package merge

import (
	"bytes"
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// fingerprint is the content fingerprint of a version: an HMAC with the Key of this run.
// It is never written and never shown, so no type of the Result holds one.
type fingerprint [sha256.Size]byte

// fingerprintOf covers what contentOf encodes.
func (k Key) fingerprintOf(v *snapshot.EntryVersion) fingerprint {
	mac := hmac.New(sha256.New, k[:])
	mac.Write(contentOf(v))
	return fingerprint(mac.Sum(nil))
}

// contentOf encodes what the fingerprint covers: all string fields, the attachments by
// content, tags, colors, icon, Auto-Type, custom data and the other values a person sets.
//
// It leaves out what can change without an edit of the content:
//   - LastModification, the other half of a version;
//   - LastAccess and UsageCount, which change when somebody reads the entry;
//   - LocationChanged and PreviousParentGroup, which change when somebody moves it;
//   - service custom data and the time of a custom data item, which change on any save.
//
// The order of fields, attachments, associations, tags and custom data does not count.
// Other programs write them in another order without an edit.
func contentOf(v *snapshot.EntryVersion) []byte {
	var e encoding
	encodeFields(&e, v)
	for _, part := range contentParts {
		part.encode(&e, v)
	}
	return e
}

// exactOf encodes a version with everything contentOf leaves out, except the usage
// values. Two versions with the same content can differ here. Taking the smallest gives
// the same result in any order of the copies.
func exactOf(v *snapshot.EntryVersion) []byte {
	e := encoding(contentOf(v))
	e.text(v.Tags)
	for i := range v.Fields {
		e.text(v.Fields[i].Key)
	}
	for i := range v.Attachments {
		e.text(v.Attachments[i].Name)
	}
	for i := range v.AutoType.Associations {
		e.text(v.AutoType.Associations[i].Window)
	}
	for i := range v.CustomData {
		item := &v.CustomData[i]
		e.text(item.Key)
		e.text(item.Value)
		e.moment(item.LastModification)
	}
	return e
}

// Attribute is a part of a version other than a string field.
type Attribute int

// The attributes of a version.
const (
	AttributeIcon Attribute = iota + 1
	AttributeColors
	AttributeOverrideURL
	AttributeQualityCheck
	AttributeTags
	AttributeCreation
	AttributeExpiry
	AttributeAttachments
	AttributeAutoType
	AttributeCustomData
)

// contentParts lists everything of a version the fingerprint covers besides the string
// fields. A difference between two versions is always named by one of these or by a field.
var contentParts = []struct {
	attribute Attribute
	encode    func(*encoding, *snapshot.EntryVersion)
}{
	{AttributeIcon, func(e *encoding, v *snapshot.EntryVersion) {
		e.number(v.IconID)
		e.uuid(v.CustomIcon)
	}},
	{AttributeColors, func(e *encoding, v *snapshot.EntryVersion) {
		e.text(v.ForegroundColor)
		e.text(v.BackgroundColor)
	}},
	{AttributeOverrideURL, func(e *encoding, v *snapshot.EntryVersion) { e.text(v.OverrideURL) }},
	{AttributeQualityCheck, func(e *encoding, v *snapshot.EntryVersion) { e.optional(v.QualityCheck) }},
	{AttributeTags, func(e *encoding, v *snapshot.EntryVersion) {
		tags := splitTags(v.Tags)
		e.count(len(tags))
		for _, tag := range tags {
			e.text(tag)
		}
	}},
	{AttributeCreation, func(e *encoding, v *snapshot.EntryVersion) { e.moment(v.Times.Creation) }},
	{AttributeExpiry, func(e *encoding, v *snapshot.EntryVersion) {
		e.moment(v.Times.Expiry)
		e.flag(v.Times.Expires)
	}},
	{AttributeAttachments, func(e *encoding, v *snapshot.EntryVersion) {
		sorted := slices.SortedFunc(slices.Values(v.Attachments), func(a, b snapshot.AttachmentRef) int {
			return cmp.Or(strings.Compare(a.Name, b.Name), bytes.Compare(a.ID[:], b.ID[:]))
		})
		e.count(len(sorted))
		for i := range sorted {
			e.text(sorted[i].Name)
			e.bytes(sorted[i].ID[:])
		}
	}},
	{AttributeAutoType, func(e *encoding, v *snapshot.EntryVersion) {
		e.flag(v.AutoType.Enabled)
		e.number(v.AutoType.Obfuscation)
		e.text(v.AutoType.DefaultSequence)
		sorted := slices.SortedFunc(slices.Values(v.AutoType.Associations), func(a, b snapshot.AutoTypeAssociation) int {
			return cmp.Or(strings.Compare(a.Window, b.Window), strings.Compare(a.KeystrokeSequence, b.KeystrokeSequence))
		})
		e.count(len(sorted))
		for i := range sorted {
			e.text(sorted[i].Window)
			e.text(sorted[i].KeystrokeSequence)
		}
	}},
	{AttributeCustomData, func(e *encoding, v *snapshot.EntryVersion) {
		items := contentCustomData(v.CustomData)
		e.count(len(items))
		for i := range items {
			e.text(items[i].Key)
			e.text(items[i].Value)
		}
	}},
}

func encodeFields(e *encoding, v *snapshot.EntryVersion) {
	sorted := slices.SortedFunc(slices.Values(v.Fields), compareFields)
	e.count(len(sorted))
	for i := range sorted {
		e.text(sorted[i].Key)
		e.text(sorted[i].Value)
		e.flag(sorted[i].Protected)
	}
}

func compareFields(a, b snapshot.Field) int {
	return cmp.Or(strings.Compare(a.Key, b.Key), strings.Compare(a.Value, b.Value), compareFlags(a.Protected, b.Protected))
}

func compareFlags(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// contentCustomData gives the items a person or a plugin has set, sorted by key. It leaves
// out the items KeePassXC renews on every save, because those change without an edit.
func contentCustomData(items []snapshot.CustomDataItem) []snapshot.CustomDataItem {
	result := make([]snapshot.CustomDataItem, 0, len(items))
	for i := range items {
		if !items[i].IsService() {
			result = append(result, items[i])
		}
	}
	slices.SortFunc(result, func(a, b snapshot.CustomDataItem) int {
		return cmp.Or(strings.Compare(a.Key, b.Key), strings.Compare(a.Value, b.Value))
	})
	return result
}

// splitTags gives the tags as a sorted set. KeePassXC takes both separators and sorts the
// tags when it loads a file, so the same tags can have several spellings.
func splitTags(tags string) []string {
	parts := strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == ';' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if tag := strings.TrimSpace(part); tag != "" {
			result = append(result, tag)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

// encoding is a byte form of values in which two different sequences of values never give
// the same bytes, because each value starts with its length or has a fixed form.
type encoding []byte

func (e *encoding) count(n int)          { *e = binary.AppendUvarint(*e, uint64(n)) }
func (e *encoding) number(n int64)       { *e = binary.AppendVarint(*e, n) }
func (e *encoding) uuid(u snapshot.UUID) { *e = append(*e, u[:]...) }

func (e *encoding) bytes(b []byte) {
	e.count(len(b))
	*e = append(*e, b...)
}

func (e *encoding) text(s string) {
	e.count(len(s))
	*e = append(*e, s...)
}

func (e *encoding) flag(b bool) {
	if b {
		*e = append(*e, 1)
	} else {
		*e = append(*e, 0)
	}
}

// optional encodes nil, false and true as three different values.
func (e *encoding) optional(b *bool) {
	e.flag(b != nil)
	if b != nil {
		e.flag(*b)
	}
}

func (e *encoding) moment(t time.Time) {
	e.number(t.Unix())
	e.number(int64(t.Nanosecond()))
}

func compareUUIDs(a, b snapshot.UUID) int { return bytes.Compare(a[:], b[:]) }

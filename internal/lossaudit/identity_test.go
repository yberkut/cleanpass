package lossaudit_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// A change is one way a version in the result can differ from the version in a copy.
type change struct {
	name string
	// touches names the field of snapshot.EntryVersion this change proves something about.
	touches string
	apply   func(v *snapshot.EntryVersion)
}

func yes() *bool { value := true; return &value }

func no() *bool { value := false; return &value }

// lostChanges make another version: the spec says the fingerprint of a version covers all
// fields, attachments by content, tags, colors, icon, Auto-Type and CustomData.
var lostChanges = []change{
	{"time", "Times.LastModification", func(v *snapshot.EntryVersion) { v.Times.LastModification = hours(99) }},
	{"field value", "Fields", func(v *snapshot.EntryVersion) { v.Fields[0].Value = "other" }},
	{"field name", "Fields", func(v *snapshot.EntryVersion) { v.Fields[0].Key = "Other" }},
	{"field protection", "Fields", func(v *snapshot.EntryVersion) { v.Fields[0].Protected = !v.Fields[0].Protected }},
	{"secret value", "Fields", func(v *snapshot.EntryVersion) { v.Fields[2].Value = "other secret" }},
	{"field removed", "Fields", func(v *snapshot.EntryVersion) { v.Fields = v.Fields[1:] }},
	{"empty field removed", "Fields", func(v *snapshot.EntryVersion) { v.Fields = v.Fields[:len(v.Fields)-1] }},
	{"field added", "Fields", func(v *snapshot.EntryVersion) {
		v.Fields = append(v.Fields, snapshot.Field{Key: "Custom", Value: "value"})
	}},
	{"value moved between name and value", "Fields", func(v *snapshot.EntryVersion) {
		v.Fields[0].Key, v.Fields[0].Value = "TitleM", "ail"
	}},
	{"attachment content", "Attachments", func(v *snapshot.EntryVersion) {
		v.Attachments[0].ID = snapshot.NewAttachmentID([]byte("other bytes"))
	}},
	{"attachment name", "Attachments", func(v *snapshot.EntryVersion) { v.Attachments[0].Name = "other.bin" }},
	{"attachment removed", "Attachments", func(v *snapshot.EntryVersion) { v.Attachments = v.Attachments[1:] }},
	{"tag changed", "Tags", func(v *snapshot.EntryVersion) { v.Tags = "alpha,other" }},
	{"tag removed", "Tags", func(v *snapshot.EntryVersion) { v.Tags = "alpha" }},
	{"foreground color", "ForegroundColor", func(v *snapshot.EntryVersion) { v.ForegroundColor = "#000000" }},
	{"background color", "BackgroundColor", func(v *snapshot.EntryVersion) { v.BackgroundColor = "#000000" }},
	{"icon", "IconID", func(v *snapshot.EntryVersion) { v.IconID = 9 }},
	{"custom icon", "CustomIcon", func(v *snapshot.EntryVersion) { v.CustomIcon = snapshot.UUID{1} }},
	{"override URL", "OverrideURL", func(v *snapshot.EntryVersion) { v.OverrideURL = "cmd://other" }},
	{"quality check removed", "QualityCheck", func(v *snapshot.EntryVersion) { v.QualityCheck = nil }},
	{"quality check flipped", "QualityCheck", func(v *snapshot.EntryVersion) { v.QualityCheck = yes() }},
	{"creation", "Times.Creation", func(v *snapshot.EntryVersion) { v.Times.Creation = hours(-99) }},
	{"expiry", "Times.Expiry", func(v *snapshot.EntryVersion) { v.Times.Expiry = hours(99) }},
	{"expires", "Times.Expires", func(v *snapshot.EntryVersion) { v.Times.Expires = !v.Times.Expires }},
	{"auto-type enabled", "AutoType.Enabled", func(v *snapshot.EntryVersion) { v.AutoType.Enabled = !v.AutoType.Enabled }},
	{"auto-type obfuscation", "AutoType.Obfuscation", func(v *snapshot.EntryVersion) { v.AutoType.Obfuscation = 5 }},
	{"auto-type sequence", "AutoType.DefaultSequence", func(v *snapshot.EntryVersion) { v.AutoType.DefaultSequence = "{OTHER}" }},
	{"auto-type window", "AutoType.Associations", func(v *snapshot.EntryVersion) { v.AutoType.Associations[0].Window = "other" }},
	{"auto-type keystrokes", "AutoType.Associations", func(v *snapshot.EntryVersion) {
		v.AutoType.Associations[0].KeystrokeSequence = "{OTHER}"
	}},
	{"auto-type association removed", "AutoType.Associations", func(v *snapshot.EntryVersion) {
		v.AutoType.Associations = v.AutoType.Associations[1:]
	}},
	{"custom data value", "CustomData", func(v *snapshot.EntryVersion) { v.CustomData[0].Value = "other" }},
	{"custom data key", "CustomData", func(v *snapshot.EntryVersion) { v.CustomData[0].Key = "other" }},
	{"custom data removed", "CustomData", func(v *snapshot.EntryVersion) { v.CustomData = v.CustomData[1:] }},
}

// sameChanges keep the version: the spec leaves LastAccessTime, UsageCount, LocationChanged
// and _LAST_MODIFIED out of the fingerprint, and the rest changes in a plain save too.
var sameChanges = []change{
	{"last access", "Times.LastAccess", func(v *snapshot.EntryVersion) { v.Times.LastAccess = hours(50) }},
	{"usage count", "Times.UsageCount", func(v *snapshot.EntryVersion) { v.Times.UsageCount = 50 }},
	{"location changed", "Times.LocationChanged", func(v *snapshot.EntryVersion) { v.Times.LocationChanged = hours(50) }},
	{"previous parent", "PreviousParentGroup", func(v *snapshot.EntryVersion) { v.PreviousParentGroup = &snapshot.UUID{2} }},
	{"_LAST_MODIFIED", "CustomData", func(v *snapshot.EntryVersion) {
		v.CustomData = append(v.CustomData, snapshot.CustomDataItem{Key: snapshot.CustomDataLastModified, Value: "Mon"})
	}},
	{"KPXC_RANDOM_SLUG", "CustomData", func(v *snapshot.EntryVersion) {
		v.CustomData = append(v.CustomData, snapshot.CustomDataItem{Key: snapshot.CustomDataRandomSlug, Value: "abc"})
	}},
	{"time of a custom data item", "CustomData", func(v *snapshot.EntryVersion) { v.CustomData[0].LastModification = hours(50) }},
	{"order of fields", "Fields", func(v *snapshot.EntryVersion) { slices.Reverse(v.Fields) }},
	{"order of attachments", "Attachments", func(v *snapshot.EntryVersion) { slices.Reverse(v.Attachments) }},
	{"order of associations", "AutoType.Associations", func(v *snapshot.EntryVersion) { slices.Reverse(v.AutoType.Associations) }},
	{"order of custom data", "CustomData", func(v *snapshot.EntryVersion) { slices.Reverse(v.CustomData) }},
	{"order of tags", "Tags", func(v *snapshot.EntryVersion) { v.Tags = "gamma,alpha" }},
	{"separator of tags", "Tags", func(v *snapshot.EntryVersion) { v.Tags = "alpha;gamma" }},
	{"spaces around tags", "Tags", func(v *snapshot.EntryVersion) { v.Tags = " alpha , gamma " }},
	{"empty tag and repeated tag", "Tags", func(v *snapshot.EntryVersion) { v.Tags = "alpha,,gamma,alpha" }},
	{"time zone of the time", "Times.LastModification", func(v *snapshot.EntryVersion) {
		v.Times.LastModification = v.Times.LastModification.In(time.FixedZone("east", 7200))
	}},
}

// fullVersion has a value in everything that can change. Each call gives its own slices.
func fullVersion() snapshot.EntryVersion {
	v := version(hours(1), "secret")
	v.Fields = append(v.Fields, snapshot.Field{Key: "Empty"})
	v.Tags = "alpha,gamma"
	v.ForegroundColor, v.BackgroundColor = "#111111", "#222222"
	v.OverrideURL = "cmd://calc"
	v.QualityCheck = no()
	v.IconID = 3
	v.CustomIcon = snapshot.UUID{9}
	v.Times.Expiry = hours(90)
	v.Attachments = []snapshot.AttachmentRef{
		{Name: "a.bin", ID: snapshot.NewAttachmentID([]byte("a"))},
		{Name: "b.bin", ID: snapshot.NewAttachmentID([]byte("b"))},
	}
	v.AutoType = snapshot.AutoType{
		Enabled:         true,
		DefaultSequence: "{PASSWORD}",
		Associations: []snapshot.AutoTypeAssociation{
			{Window: "A", KeystrokeSequence: "{A}"},
			{Window: "B", KeystrokeSequence: "{B}"},
		},
	}
	v.CustomData = []snapshot.CustomDataItem{{Key: "KPXC_BROWSER", Value: "{}"}, {Key: "plugin", Value: "1"}}
	return v
}

func TestAChangedPartOfAHistoryVersionIsALoss(t *testing.T) {
	main := version(hours(10), "main")
	for _, c := range lostChanges {
		t.Run(c.name, func(t *testing.T) {
			changed := fullVersion()
			c.apply(&changed)

			got := audit(database(entry(mailID, main, changed)), database(entry(mailID, main, fullVersion())))

			wantLosses(t, got, versionLost(mailID, hours(1)))
		})
	}
}

func TestAChangedPartOfAMainVersionIsALoss(t *testing.T) {
	for _, c := range lostChanges {
		t.Run(c.name, func(t *testing.T) {
			changed := fullVersion()
			c.apply(&changed)

			got := audit(database(entry(mailID, changed)), database(entry(mailID, fullVersion())))

			wantLosses(t, got, versionLost(mailID, hours(1)))
		})
	}
}

func TestWhatAPlainSaveChangesIsNoLoss(t *testing.T) {
	for _, c := range sameChanges {
		t.Run(c.name, func(t *testing.T) {
			changed := fullVersion()
			c.apply(&changed)

			wantLosses(t, audit(database(entry(mailID, changed)), database(entry(mailID, fullVersion()))))
			wantLosses(t, audit(database(entry(mailID, fullVersion())), database(entry(mailID, changed))))
		})
	}
}

// A new field of a version must be sorted into one of the two lists above, or the audit
// would miss its loss without any test noticing.
func TestEveryPartOfAVersionIsClassified(t *testing.T) {
	proven := map[string]bool{}
	for _, c := range slices.Concat(lostChanges, sameChanges) {
		proven[c.touches] = true
	}
	version := reflect.TypeFor[snapshot.EntryVersion]()
	for field := range version.Fields() {
		if field.Type.Kind() == reflect.Struct {
			for inner := range field.Type.Fields() {
				if name := field.Name + "." + inner.Name; !proven[name] {
					t.Errorf("no test says whether the audit covers %s", name)
				}
			}
			continue
		}
		if !proven[field.Name] {
			t.Errorf("no test says whether the audit covers %s", field.Name)
		}
	}
}

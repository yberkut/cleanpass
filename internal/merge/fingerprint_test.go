package merge_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// A change is one way two versions with the same time can differ. The fingerprint is
// never shown, so a test can see it only through what the engine does: covered content
// gives two versions, anything else gives one.
type change struct {
	name string
	// touches names the field of snapshot.EntryVersion this change proves something about.
	touches string
	apply   func(v *snapshot.EntryVersion)
}

func yes() *bool { value := true; return &value }

func no() *bool { value := false; return &value }

var coveredChanges = []change{
	{"field value", "Fields", func(v *snapshot.EntryVersion) { v.Fields[0].Value = "other" }},
	{"field name", "Fields", func(v *snapshot.EntryVersion) { v.Fields[0].Key = "Other" }},
	{"field protection", "Fields", func(v *snapshot.EntryVersion) { v.Fields[0].Protected = !v.Fields[0].Protected }},
	{"secret value", "Fields", func(v *snapshot.EntryVersion) { v.Fields[2].Value = "other secret" }},
	{"new custom field", "Fields", func(v *snapshot.EntryVersion) {
		v.Fields = append(v.Fields, snapshot.Field{Key: "Custom", Value: "value"})
	}},
	{"new empty field", "Fields", func(v *snapshot.EntryVersion) {
		v.Fields = append(v.Fields, snapshot.Field{Key: "Custom"})
	}},
	{"value moved between name and value", "Fields", func(v *snapshot.EntryVersion) {
		v.Fields = []snapshot.Field{{Key: "ab", Value: "c"}}
	}},
	{"attachment bytes", "Attachments", func(v *snapshot.EntryVersion) {
		v.Attachments[0].ID = snapshot.NewAttachmentID([]byte("other bytes"))
	}},
	{"attachment name", "Attachments", func(v *snapshot.EntryVersion) { v.Attachments[0].Name = "other.bin" }},
	{"attachment removed", "Attachments", func(v *snapshot.EntryVersion) { v.Attachments = nil }},
	{"tag", "Tags", func(v *snapshot.EntryVersion) { v.Tags = "alpha,other" }},
	{"foreground color", "ForegroundColor", func(v *snapshot.EntryVersion) { v.ForegroundColor = "#000000" }},
	{"background color", "BackgroundColor", func(v *snapshot.EntryVersion) { v.BackgroundColor = "#000000" }},
	{"icon", "IconID", func(v *snapshot.EntryVersion) { v.IconID = 9 }},
	{"custom icon", "CustomIcon", func(v *snapshot.EntryVersion) { v.CustomIcon = snapshot.UUID{1} }},
	{"override URL", "OverrideURL", func(v *snapshot.EntryVersion) { v.OverrideURL = "cmd://other" }},
	{"quality check set", "QualityCheck", func(v *snapshot.EntryVersion) { v.QualityCheck = no() }},
	{"quality check to true", "QualityCheck", func(v *snapshot.EntryVersion) { v.QualityCheck = yes() }},
	{"expiry", "Times.Expiry", func(v *snapshot.EntryVersion) { v.Times.Expiry = hours(99) }},
	{"expires", "Times.Expires", func(v *snapshot.EntryVersion) { v.Times.Expires = !v.Times.Expires }},
	{"creation", "Times.Creation", func(v *snapshot.EntryVersion) { v.Times.Creation = hours(-99) }},
	{"auto-type enabled", "AutoType.Enabled", func(v *snapshot.EntryVersion) { v.AutoType.Enabled = !v.AutoType.Enabled }},
	{"auto-type obfuscation", "AutoType.Obfuscation", func(v *snapshot.EntryVersion) { v.AutoType.Obfuscation = 5 }},
	{"auto-type sequence", "AutoType.DefaultSequence", func(v *snapshot.EntryVersion) { v.AutoType.DefaultSequence = "{OTHER}" }},
	{"auto-type window", "AutoType.Associations", func(v *snapshot.EntryVersion) { v.AutoType.Associations[0].Window = "other" }},
	{"auto-type keystrokes", "AutoType.Associations", func(v *snapshot.EntryVersion) {
		v.AutoType.Associations[0].KeystrokeSequence = "{OTHER}"
	}},
	{"custom data value", "CustomData", func(v *snapshot.EntryVersion) { v.CustomData[0].Value = "other" }},
	{"custom data key", "CustomData", func(v *snapshot.EntryVersion) { v.CustomData[0].Key = "other" }},
	{"custom data added", "CustomData", func(v *snapshot.EntryVersion) {
		v.CustomData = append(v.CustomData, snapshot.CustomDataItem{Key: "KPXC_BROWSER_NEW", Value: "1"})
	}},
}

var notCoveredChanges = []change{
	{"last access", "Times.LastAccess", func(v *snapshot.EntryVersion) { v.Times.LastAccess = hours(50) }},
	{"usage count", "Times.UsageCount", func(v *snapshot.EntryVersion) { v.Times.UsageCount = 50 }},
	{"location changed", "Times.LocationChanged", func(v *snapshot.EntryVersion) { v.Times.LocationChanged = hours(50) }},
	{"previous parent", "PreviousParentGroup", func(v *snapshot.EntryVersion) { v.PreviousParentGroup = &snapshot.UUID{2} }},
	// The two service items of KeePassXC. A plain re-save changes both, on any level.
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
}

// fullVersion has a value in everything that can change. Each call gives its own slices.
func fullVersion() snapshot.EntryVersion {
	v := version(hours(1), "secret")
	v.Tags = "alpha,gamma"
	v.ForegroundColor, v.BackgroundColor = "#111111", "#222222"
	v.OverrideURL = "cmd://calc"
	v.IconID = 3
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

// versionsOf merges one copy with the full version and one with the changed version.
func versionsOf(t *testing.T, c change) int {
	t.Helper()
	changed := fullVersion()
	c.apply(&changed)
	merged := onlyEntry(t, mergeOf(t, database(entry(fullVersion())), database(entry(changed))).Merged)
	return 1 + len(merged.History)
}

func TestTheFingerprintCoversTheContent(t *testing.T) {
	for _, c := range coveredChanges {
		t.Run(c.name, func(t *testing.T) {
			if got := versionsOf(t, c); got != 2 {
				t.Errorf("the entry has %d versions, want 2 different versions", got)
			}
		})
	}
}

func TestTheFingerprintLeavesOutWhatChangesWithoutAnEdit(t *testing.T) {
	for _, c := range notCoveredChanges {
		t.Run(c.name, func(t *testing.T) {
			if got := versionsOf(t, c); got != 1 {
				t.Errorf("the entry has %d versions, want 1: this is the same version", got)
			}
		})
	}
}

// LastModification is the other half of a version, so it is not in the fingerprint.
const versionTime = "Times.LastModification"

// A new field of a version must be sorted into one of the two lists above, or the
// fingerprint would miss it without any test noticing.
func TestEveryPartOfAVersionIsClassified(t *testing.T) {
	proven := map[string]bool{versionTime: true}
	for _, c := range slices.Concat(coveredChanges, notCoveredChanges) {
		proven[c.touches] = true
	}
	version := reflect.TypeFor[snapshot.EntryVersion]()
	for i := range version.NumField() {
		field := version.Field(i)
		if field.Type.Kind() == reflect.Struct {
			for j := range field.Type.NumField() {
				if name := field.Name + "." + field.Type.Field(j).Name; !proven[name] {
					t.Errorf("no test says whether the fingerprint covers %s", name)
				}
			}
			continue
		}
		if !proven[field.Name] {
			t.Errorf("no test says whether the fingerprint covers %s", field.Name)
		}
	}
}

func TestSameTimeDifferentContentGivesTwoVersionsOrderedByTheKey(t *testing.T) {
	first := database(entry(version(hours(1), "first")))
	second := database(entry(version(hours(1), "second")))

	wins := map[string]int{}
	for i := range 64 {
		in := input(named(first, second)...)
		in.Key = merge.Key{31: byte(i + 1)}
		e := onlyEntry(t, mustRun(t, in).Merged)
		wins[fieldValue(&e.EntryVersion, "Password")]++
	}
	if wins["first"] == 0 || wins["second"] == 0 {
		t.Errorf("wins = %v: the order must depend on the key", wins)
	}
}

func TestAKeyOrdersTheSameVersionsTheSameWayInAnyOrderOfCopies(t *testing.T) {
	first := database(entry(version(hours(1), "first")))
	second := database(entry(version(hours(1), "second")))

	forward := onlyEntry(t, mergeOf(t, first, second).Merged)
	backward := onlyEntry(t, mergeOf(t, second, first).Merged)

	if !reflect.DeepEqual(forward, backward) {
		t.Errorf("forward = %+v, backward = %+v", forward, backward)
	}
}

func TestNewKeyIsRandom(t *testing.T) {
	first, second := merge.NewKey(), merge.NewKey()
	if first == (merge.Key{}) || first == second {
		t.Errorf("keys %x and %x", first, second)
	}
}

// A value derived from a fingerprint would change with the key. Nothing in the result may
// change, unless two versions have the same time and the key breaks the tie.
func TestNoFingerprintReachesTheResult(t *testing.T) {
	older, newer := version(hours(1), "old"), version(hours(2), "new")
	other := snapshot.UUID{15: 0x02}
	long := []snapshot.Entry{entry(older), {UUID: other, EntryVersion: version(hours(1), "a")}}
	lagging := []snapshot.Entry{entry(newer, older), {UUID: other, EntryVersion: version(hours(100), "b")}}

	in := input(named(database(long...), database(lagging...))...)
	first := mustRun(t, in)
	in.Key = merge.Key{9, 9, 9}
	second := mustRun(t, in)

	if !reflect.DeepEqual(first, second) {
		t.Errorf("the result depends on the key:\n%+v\n%+v", first, second)
	}
}

func TestRunRefusesInputThatCannotBeRight(t *testing.T) {
	one := named(database())
	for _, tc := range []struct {
		name string
		in   merge.Input
		want error
	}{
		{"no copies", merge.Input{Now: now, Key: testKey}, merge.ErrNoCopies},
		{"no key", merge.Input{Copies: one, Now: now}, merge.ErrNoKey},
		{"no time", merge.Input{Copies: one, Key: testKey}, merge.ErrNoTime},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := merge.Run(tc.in); err != tc.want {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

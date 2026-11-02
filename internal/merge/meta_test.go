package merge_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// withMeta is a copy with these settings.
func withMeta(meta snapshot.Meta) *snapshot.Snapshot {
	s := database()
	s.Meta = meta
	return s
}

// inAnyOrder runs the check on the merge of the copies, forward and backward.
func inAnyOrder(t *testing.T, copies []*snapshot.Snapshot, check func(t *testing.T, merged *snapshot.Snapshot)) {
	t.Helper()
	backward := slices.Clone(copies)
	slices.Reverse(backward)
	for _, order := range [][]*snapshot.Snapshot{copies, backward} {
		check(t, mergeOf(t, order...).Merged)
	}
}

// Each setting with a time of its own takes its newest value, so the copy with the newest name
// need not be the copy with the newest description.
func TestEachSettingWithATimeOfItsOwnTakesItsNewestValue(t *testing.T) {
	templates := snapshot.UUID{15: 0xC1}
	first := withMeta(snapshot.Meta{
		DatabaseName: "new name", DatabaseNameChanged: hours(9),
		DatabaseDescription: "old description", DatabaseDescriptionChanged: hours(1),
		DefaultUserName: "new user", DefaultUserNameChanged: hours(9),
		EntryTemplatesGroup: templates, EntryTemplatesGroupChanged: hours(9),
		MasterKeyChanged: hours(1), MasterKeyChangeRec: 10, MasterKeyChangeForce: 20,
	})
	second := withMeta(snapshot.Meta{
		DatabaseName: "old name", DatabaseNameChanged: hours(1),
		DatabaseDescription: "new description", DatabaseDescriptionChanged: hours(9),
		DefaultUserName: "old user", DefaultUserNameChanged: hours(1),
		EntryTemplatesGroupChanged: hours(1),
		MasterKeyChanged:           hours(9), MasterKeyChangeRec: 30, MasterKeyChangeForce: 40,
	})

	inAnyOrder(t, []*snapshot.Snapshot{first, second}, func(t *testing.T, merged *snapshot.Snapshot) {
		meta := merged.Meta
		if meta.DatabaseName != "new name" || !meta.DatabaseNameChanged.Equal(hours(9)) {
			t.Errorf("name = %q at %s", meta.DatabaseName, meta.DatabaseNameChanged)
		}
		if meta.DatabaseDescription != "new description" || !meta.DatabaseDescriptionChanged.Equal(hours(9)) {
			t.Errorf("description = %q at %s", meta.DatabaseDescription, meta.DatabaseDescriptionChanged)
		}
		if meta.DefaultUserName != "new user" || !meta.DefaultUserNameChanged.Equal(hours(9)) {
			t.Errorf("default user = %q at %s", meta.DefaultUserName, meta.DefaultUserNameChanged)
		}
		if meta.EntryTemplatesGroup != templates || !meta.EntryTemplatesGroupChanged.Equal(hours(9)) {
			t.Errorf("templates = %s at %s", meta.EntryTemplatesGroup, meta.EntryTemplatesGroupChanged)
		}
		if !meta.MasterKeyChanged.Equal(hours(9)) || meta.MasterKeyChangeRec != 30 || meta.MasterKeyChangeForce != 40 {
			t.Errorf("master key: %s, %d, %d", meta.MasterKeyChanged, meta.MasterKeyChangeRec, meta.MasterKeyChangeForce)
		}
	})
}

func TestTwoValuesOfASettingAtTheSameTimeGiveTheSameValueInAnyOrder(t *testing.T) {
	first := withMeta(snapshot.Meta{DatabaseName: "a", DatabaseNameChanged: hours(5)})
	second := withMeta(snapshot.Meta{DatabaseName: "b", DatabaseNameChanged: hours(5)})

	forward := mergeOf(t, first, second).Merged.Meta.DatabaseName
	backward := mergeOf(t, second, first).Merged.Meta.DatabaseName

	if forward != backward {
		t.Errorf("the name depends on the order of the copies: %q and %q", forward, backward)
	}
}

// What has no time of its own follows SettingsChanged, and the result holds the newest of those.
func TestTheOtherSettingsFollowSettingsChanged(t *testing.T) {
	older := withMeta(snapshot.Meta{SettingsChanged: hours(1), Color: "#111111", MaintenanceHistoryDays: 1})
	newer := withMeta(snapshot.Meta{SettingsChanged: hours(9), Color: "#999999", MaintenanceHistoryDays: 9})

	inAnyOrder(t, []*snapshot.Snapshot{older, newer}, func(t *testing.T, merged *snapshot.Snapshot) {
		meta := merged.Meta
		if meta.Color != "#999999" || meta.MaintenanceHistoryDays != 9 || !meta.SettingsChanged.Equal(hours(9)) {
			t.Errorf("settings = %s, %d at %s", meta.Color, meta.MaintenanceHistoryDays, meta.SettingsChanged)
		}
	})
}

func TestCustomIconsJoinByUUIDWithTheNewestContent(t *testing.T) {
	onlyFirst, onlySecond, both := snapshot.UUID{15: 0x51}, snapshot.UUID{15: 0x52}, snapshot.UUID{15: 0x53}
	first := withMeta(snapshot.Meta{CustomIcons: []snapshot.CustomIcon{
		{UUID: both, Data: "old", LastModification: hours(1)},
		{UUID: onlyFirst, Data: "first", LastModification: hours(3)},
	}})
	second := withMeta(snapshot.Meta{CustomIcons: []snapshot.CustomIcon{
		{UUID: onlySecond, Data: "second", LastModification: hours(2)},
		{UUID: both, Data: "new", Name: "named", LastModification: hours(9)},
	}})

	inAnyOrder(t, []*snapshot.Snapshot{first, second}, func(t *testing.T, merged *snapshot.Snapshot) {
		want := []snapshot.CustomIcon{
			{UUID: onlyFirst, Data: "first", LastModification: hours(3)},
			{UUID: onlySecond, Data: "second", LastModification: hours(2)},
			{UUID: both, Data: "new", Name: "named", LastModification: hours(9)},
		}
		if !reflect.DeepEqual(merged.Meta.CustomIcons, want) {
			t.Errorf("icons = %+v, want %+v", merged.Meta.CustomIcons, want)
		}
	})
}

func TestCustomDataJoinsByKeyWithTheNewestValue(t *testing.T) {
	first := withMeta(snapshot.Meta{CustomData: []snapshot.CustomDataItem{
		{Key: "both", Value: "old", LastModification: hours(1)},
		{Key: "only first", Value: "first", LastModification: hours(3)},
	}})
	second := withMeta(snapshot.Meta{CustomData: []snapshot.CustomDataItem{
		{Key: "only second", Value: "second"},
		{Key: "both", Value: "new", LastModification: hours(9)},
	}})

	inAnyOrder(t, []*snapshot.Snapshot{first, second}, func(t *testing.T, merged *snapshot.Snapshot) {
		want := []snapshot.CustomDataItem{
			{Key: "both", Value: "new", LastModification: hours(9)},
			{Key: "only first", Value: "first", LastModification: hours(3)},
			{Key: "only second", Value: "second"},
		}
		if !reflect.DeepEqual(merged.Meta.CustomData, want) {
			t.Errorf("custom data = %+v, want %+v", merged.Meta.CustomData, want)
		}
	})
}

// historyOf is an entry with this many versions, each with a value of this many bytes.
func historyOf(versions, size int) snapshot.Entry {
	all := make([]snapshot.EntryVersion, versions)
	for i := range all {
		all[i] = version(hours(float64(i)), string(make([]byte, size))+string(rune('a'+i)))
	}
	return entry(all[versions-1], all[:versions-1]...)
}

// limited is a copy with these limits of the history of KeePassXC.
func limited(items, size int64, entries ...snapshot.Entry) *snapshot.Snapshot {
	s := database(entries...)
	s.Meta.HistoryMaxItems, s.Meta.HistoryMaxSize = items, size
	return s
}

// The next edit puts the main version in the history too, so the limits hold all versions. The
// merged history is longer than the history of any copy, which is what a limit would cut.
func TestTheHistoryLimitsRiseToHoldTheJoinedHistory(t *testing.T) {
	long := historyOf(8, 100)
	other := long
	other.History = nil
	other.EntryVersion = version(hours(50), "other main")
	first, second := limited(3, 300, long), limited(3, 300, other)

	inAnyOrder(t, []*snapshot.Snapshot{first, second}, func(t *testing.T, merged *snapshot.Snapshot) {
		versions := 1 + len(onlyEntry(t, merged).History)
		if versions != 9 {
			t.Fatalf("the entry has %d versions, want 9", versions)
		}
		if merged.Meta.HistoryMaxItems < int64(versions) {
			t.Errorf("history max items = %d, want at least %d", merged.Meta.HistoryMaxItems, versions)
		}
		if merged.Meta.HistoryMaxSize < 900 {
			t.Errorf("history max size = %d, want more than the 900 bytes of the versions", merged.Meta.HistoryMaxSize)
		}
	})
}

func TestTheAttachmentsCountForTheLimitOfTheHistorySize(t *testing.T) {
	content := make([]byte, 5000)
	id := snapshot.NewAttachmentID(content)
	v := version(hours(1), "x")
	v.Attachments = []snapshot.AttachmentRef{{Name: "a.bin", ID: id}}
	s := limited(10, 100, entry(v))
	s.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{id: {Content: content}}

	if got := mergeOf(t, s).Merged.Meta.HistoryMaxSize; got < 5000 {
		t.Errorf("history max size = %d, want more than the bytes of the attachment", got)
	}
}

// gremlins found that no test weighed these two parts of a version.
func TestLargePartsOfAVersionCountForTheLimitOfTheHistorySize(t *testing.T) {
	long := strings.Repeat("x", 5000)
	for name, change := range map[string]func(v *snapshot.EntryVersion){
		"a sequence of Auto-Type": func(v *snapshot.EntryVersion) {
			v.AutoType.Associations = []snapshot.AutoTypeAssociation{{Window: "w", KeystrokeSequence: long}}
		},
		"a window of Auto-Type": func(v *snapshot.EntryVersion) {
			v.AutoType.Associations = []snapshot.AutoTypeAssociation{{Window: long, KeystrokeSequence: "s"}}
		},
		"a value of custom data": func(v *snapshot.EntryVersion) {
			v.CustomData = []snapshot.CustomDataItem{{Key: "k", Value: long}}
		},
		"a key of custom data": func(v *snapshot.EntryVersion) {
			v.CustomData = []snapshot.CustomDataItem{{Key: long, Value: "v"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			v := version(hours(1), "x")
			change(&v)

			if got := mergeOf(t, limited(10, 100, entry(v))).Merged.Meta.HistoryMaxSize; got < 5000 {
				t.Errorf("history max size = %d, want more than the 5000 bytes of this part", got)
			}
		})
	}
}

func TestALimitThatHoldsTheHistoryStaysAsItIs(t *testing.T) {
	merged := mergeOf(t, limited(10, 6291456, historyOf(3, 10)), limited(25, 1000000, historyOf(3, 10))).Merged

	if merged.Meta.HistoryMaxItems != 25 || merged.Meta.HistoryMaxSize != 6291456 {
		t.Errorf("limits = %d and %d, want the largest of the copies", merged.Meta.HistoryMaxItems, merged.Meta.HistoryMaxSize)
	}
}

// -1 means no limit to KeePassXC, so it beats every number.
func TestNoLimitStaysNoLimit(t *testing.T) {
	inAnyOrder(t, []*snapshot.Snapshot{limited(-1, 100, historyOf(4, 200)), limited(100, -1, historyOf(4, 200))}, func(t *testing.T, merged *snapshot.Snapshot) {
		if merged.Meta.HistoryMaxItems != -1 || merged.Meta.HistoryMaxSize != -1 {
			t.Errorf("limits = %d and %d, want -1 and -1", merged.Meta.HistoryMaxItems, merged.Meta.HistoryMaxSize)
		}
	})
}

// A limit of zero is a number. It rises as far as the history needs, and it does not mean no limit.
func TestAZeroLimitRisesAsFarAsTheHistoryNeeds(t *testing.T) {
	merged := mergeOf(t, limited(0, 0, historyOf(3, 10))).Merged

	if merged.Meta.HistoryMaxItems < 3 || merged.Meta.HistoryMaxSize < 30 {
		t.Errorf("limits = %d and %d, want room for the three versions", merged.Meta.HistoryMaxItems, merged.Meta.HistoryMaxSize)
	}
}

// An entry the human deleted holds no history anymore, so it asks for no higher limit.
func TestADeletedEntryRaisesNoLimit(t *testing.T) {
	gone := limited(3, 1000000, historyOf(8, 10))
	deleted := recorded(limited(3, 1000000), snapshot.Deletion{UUID: mailID, Time: hours(99)})

	merged := answering(t, merge.AnswerDelete, named(gone, deleted)...).Merged

	if merged.Meta.HistoryMaxItems != 3 {
		t.Errorf("history max items = %d, want 3", merged.Meta.HistoryMaxItems)
	}
}

package lossaudit_test

import (
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/lossaudit"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

var iconID = snapshot.UUID{15: 0xC1}

func withIcons(icons ...snapshot.CustomIcon) *snapshot.Snapshot {
	s := database()
	s.Meta.CustomIcons = icons
	return s
}

func icon(id snapshot.UUID, data string, modified time.Time) snapshot.CustomIcon {
	return snapshot.CustomIcon{UUID: id, Data: data, Name: "icon", LastModification: modified}
}

func iconLost(id snapshot.UUID) lossaudit.Loss {
	return lossaudit.Loss{Kind: lossaudit.CustomIconLost, Object: id}
}

func TestAKeptCustomIconIsNoLoss(t *testing.T) {
	kept := withIcons(icon(iconID, "AAAA", hours(1)))

	wantLosses(t, audit(kept, kept))
}

func TestAMissingCustomIconIsALoss(t *testing.T) {
	wantLosses(t, audit(database(), withIcons(icon(iconID, "AAAA", hours(1)))), iconLost(iconID))
}

// The icons of all copies join, also those only an older copy has.
func TestACustomIconOfAnyCopyCounts(t *testing.T) {
	other := snapshot.UUID{15: 0xC2}
	first, second := withIcons(icon(iconID, "AAAA", hours(1))), withIcons(icon(other, "BBBB", hours(2)))

	wantLosses(t, audit(withIcons(icon(iconID, "AAAA", hours(1)), icon(other, "BBBB", hours(2))), first, second))
	wantLosses(t, audit(second, first, second), iconLost(iconID))
}

func TestACustomIconKeepsItsNewestContent(t *testing.T) {
	older, newer := withIcons(icon(iconID, "OLD", hours(1))), withIcons(icon(iconID, "NEW", hours(2)))

	wantLosses(t, audit(newer, older, newer))
	wantLosses(t, audit(older, older, newer), iconLost(iconID))
	wantLosses(t, audit(older, newer, older), iconLost(iconID))
}

func TestACustomIconKeepsItsNewestName(t *testing.T) {
	older, newer := icon(iconID, "AAAA", hours(1)), icon(iconID, "AAAA", hours(2))
	newer.Name = "renamed"

	wantLosses(t, audit(withIcons(newer), withIcons(older), withIcons(newer)))
	older.LastModification = hours(2)
	wantLosses(t, audit(withIcons(older), withIcons(newer)), iconLost(iconID))
}

func TestEitherCustomIconWithTheSameTimeWillDo(t *testing.T) {
	first, second := withIcons(icon(iconID, "ONE", hours(1))), withIcons(icon(iconID, "TWO", hours(1)))

	wantLosses(t, audit(first, first, second))
	wantLosses(t, audit(second, first, second))
	wantLosses(t, audit(withIcons(icon(iconID, "THREE", hours(1))), first, second), iconLost(iconID))
}

// A time before year one is broken, but the thing with that time is still there to keep.
func TestACustomIconWithABrokenTimeStillCounts(t *testing.T) {
	broken := withIcons(icon(iconID, "AAAA", time.Time{}.Add(-time.Hour)))

	wantLosses(t, audit(broken, broken))
	wantLosses(t, audit(database(), broken), iconLost(iconID))
}

func withCustomData(items ...snapshot.CustomDataItem) *snapshot.Snapshot {
	s := database()
	s.Meta.CustomData = items
	return s
}

func dataItem(key, value string, modified time.Time) snapshot.CustomDataItem {
	return snapshot.CustomDataItem{Key: key, Value: value, LastModification: modified}
}

func dataLost(key string) lossaudit.Loss {
	return lossaudit.Loss{Kind: lossaudit.CustomDataLost, Name: key}
}

func TestKeptCustomDataIsNoLoss(t *testing.T) {
	kept := withCustomData(dataItem("plugin", "1", hours(1)))

	wantLosses(t, audit(kept, kept))
}

func TestMissingCustomDataIsALoss(t *testing.T) {
	wantLosses(t, audit(database(), withCustomData(dataItem("plugin", "1", hours(1)))), dataLost("plugin"))
}

func TestCustomDataOfAnyCopyCounts(t *testing.T) {
	first, second := withCustomData(dataItem("a", "1", hours(1))), withCustomData(dataItem("b", "2", hours(2)))

	wantLosses(t, audit(withCustomData(dataItem("a", "1", hours(1)), dataItem("b", "2", hours(2))), first, second))
	wantLosses(t, audit(second, first, second), dataLost("a"))
}

func TestCustomDataKeepsItsNewestValue(t *testing.T) {
	older, newer := withCustomData(dataItem("a", "old", hours(1))), withCustomData(dataItem("a", "new", hours(2)))

	wantLosses(t, audit(newer, older, newer))
	wantLosses(t, audit(older, older, newer), dataLost("a"))
	wantLosses(t, audit(older, newer, older), dataLost("a"))
}

// The time of the item is not what a person keeps. Only the value counts.
func TestTheTimeOfACustomDataItemIsNoLoss(t *testing.T) {
	got := audit(withCustomData(dataItem("a", "1", hours(9))), withCustomData(dataItem("a", "1", hours(1))))

	wantLosses(t, got)
}

func TestEitherCustomDataValueWithTheSameTimeWillDo(t *testing.T) {
	first, second := withCustomData(dataItem("a", "1", time.Time{})), withCustomData(dataItem("a", "2", time.Time{}))

	wantLosses(t, audit(first, first, second))
	wantLosses(t, audit(second, first, second))
	wantLosses(t, audit(withCustomData(dataItem("a", "3", time.Time{})), first, second), dataLost("a"))
}

// KeePassXC renews these on every save, so their values mean nothing.
func TestServiceCustomDataIsNoLoss(t *testing.T) {
	input := withCustomData(
		dataItem(snapshot.CustomDataRandomSlug, "abc", hours(1)),
		dataItem(snapshot.CustomDataLastModified, "Mon", hours(1)),
	)

	wantLosses(t, audit(database(), input))
}

// settingCases name each setting with its own time of change and a way to set both.
var settingCases = []struct {
	name string
	set  func(m *snapshot.Meta, value string, changed time.Time)
}{
	{"DatabaseName", func(m *snapshot.Meta, value string, changed time.Time) {
		m.DatabaseName, m.DatabaseNameChanged = value, changed
	}},
	{"DatabaseDescription", func(m *snapshot.Meta, value string, changed time.Time) {
		m.DatabaseDescription, m.DatabaseDescriptionChanged = value, changed
	}},
	{"DefaultUserName", func(m *snapshot.Meta, value string, changed time.Time) {
		m.DefaultUserName, m.DefaultUserNameChanged = value, changed
	}},
}

func TestASettingKeepsItsNewestValue(t *testing.T) {
	for _, c := range settingCases {
		t.Run(c.name, func(t *testing.T) {
			older, newer := database(), database()
			c.set(&older.Meta, "old", hours(1))
			c.set(&newer.Meta, "new", hours(2))
			lost := lossaudit.Loss{Kind: lossaudit.SettingLost, Name: c.name}

			wantLosses(t, audit(newer, older, newer))
			wantLosses(t, audit(older, older, newer), lost)
			wantLosses(t, audit(older, newer, older), lost)
		})
	}
}

// Only the time of each setting counts, not the time of the other settings.
func TestSettingsChooseTheirCopyOneByOne(t *testing.T) {
	first, second, result := database(), database(), database()
	settingCases[0].set(&first.Meta, "name", hours(2))
	settingCases[0].set(&second.Meta, "old name", hours(1))
	settingCases[1].set(&first.Meta, "old description", hours(1))
	settingCases[1].set(&second.Meta, "description", hours(2))
	settingCases[0].set(&result.Meta, "name", hours(2))
	settingCases[1].set(&result.Meta, "description", hours(2))

	wantLosses(t, audit(result, first, second))
}

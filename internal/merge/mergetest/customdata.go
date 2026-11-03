package mergetest

import (
	"fmt"
	"slices"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// CustomDataMarker starts every value of the custom data of an entry. A test looks for it in
// any text that must not hold such a value.
const CustomDataMarker = "CUSTOM-DATA-MARKER-"

// The moves of custom data and of a time that changes alone. The keys and the values come from
// few choices, so that two versions with the same time often hold one another, or neither does.
func init() {
	register("edit as KeePassXC does", editAsKeePassXC)
	register("edit behind the clock as KeePassXC does", editBehindTheClockAsKeePassXC)
	register("set custom data of an entry in the same second", setEntryDataInTheSameSecond)
	register("set custom data of an entry", setEntryData)
	register("forget custom data of an entry", forgetEntryData)
	register("renew the time", renewTime)
}

var entryDataKeys = []string{"KPXC_BROWSER", "plugin", "other"}

func drawCustomDataValue(t *rapid.T) string {
	return fmt.Sprintf("%s%d", CustomDataMarker, rapid.IntRange(0, 2).Draw(t, "custom data value"))
}

// keepInHistoryWithoutCustomData adds what KeePassXC 2.7.12 adds to the history on an edit: the
// old main version, without the custom data of the entry.
func keepInHistoryWithoutCustomData(e *snapshot.Entry) {
	old := cloneVersion(&e.EntryVersion)
	old.CustomData = nil
	e.History = append(e.History, old)
}

// editAsKeePassXC makes a new version the way KeePassXC 2.7.12 does.
func editAsKeePassXC(t *rapid.T, _ *world, f *fork) {
	if ref, ok := f.pickEntry(t); ok {
		e := ref.entry()
		keepInHistoryWithoutCustomData(e)
		edit(t, f.snapshot, e, f.tick(t), false)
	}
}

// editBehindTheClockAsKeePassXC is that edit on a system whose clock runs behind: the version it
// replaces has the later time. So the clock and the history disagree, and the history lacks the
// custom data of the version another copy still holds as its main version.
func editBehindTheClockAsKeePassXC(t *rapid.T, _ *world, f *fork) {
	if ref, ok := f.pickEntry(t); ok {
		e := ref.entry()
		keepInHistoryWithoutCustomData(e)
		edit(t, f.snapshot, e, e.Times.LastModification.Add(-rapid.SampledFrom(gaps).Draw(t, "gap")), false)
	}
}

// setEntryDataInTheSameSecond sets an item the way an edit does, with no other change, and the
// clock has not moved on. So the history holds the main version without its custom data.
func setEntryDataInTheSameSecond(t *rapid.T, _ *world, f *fork) {
	if ref, ok := f.pickEntry(t); ok {
		e := ref.entry()
		keepInHistoryWithoutCustomData(e)
		setItem(t, &e.EntryVersion)
	}
}

// pickVersion draws the main version or a history version of an entry.
func pickVersion(t *rapid.T, e *snapshot.Entry) *snapshot.EntryVersion {
	at := rapid.IntRange(0, len(e.History)).Draw(t, "version")
	if at == 0 {
		return &e.EntryVersion
	}
	return &e.History[at-1]
}

// setEntryData sets an item with no new time, as a plugin or another program can.
func setEntryData(t *rapid.T, _ *world, f *fork) {
	ref, ok := f.pickEntry(t)
	if !ok {
		return
	}
	setItem(t, pickVersion(t, ref.entry()))
}

func setItem(t *rapid.T, v *snapshot.EntryVersion) {
	key := rapid.SampledFrom(entryDataKeys).Draw(t, "custom data key")
	v.CustomData = slices.DeleteFunc(v.CustomData, func(item snapshot.CustomDataItem) bool { return item.Key == key })
	v.CustomData = append(v.CustomData, snapshot.CustomDataItem{Key: key, Value: drawCustomDataValue(t)})
}

// forgetEntryData removes an item with no new time, as a program that does not keep it does.
func forgetEntryData(t *rapid.T, _ *world, f *fork) {
	ref, ok := f.pickEntry(t)
	if !ok {
		return
	}
	v := pickVersion(t, ref.entry())
	if len(v.CustomData) == 0 {
		return
	}
	at := rapid.IntRange(0, len(v.CustomData)-1).Draw(t, "custom data item")
	v.CustomData = slices.Delete(v.CustomData, at, at+1)
	if len(v.CustomData) == 0 {
		v.CustomData = nil // A file with no custom data reads as nil, not as an empty list.
	}
}

// renewTime gives the main version a new time with the same content and no item in the history,
// as keepassxc-cli 2.7.12 does when it moves an entry to the bin. So two copies hold the same
// content with different times.
func renewTime(t *rapid.T, _ *world, f *fork) {
	if ref, ok := f.pickEntry(t); ok {
		e := ref.entry()
		e.Times.LastModification = f.tick(t)
		e.Times.LastAccess = e.Times.LastModification
	}
}

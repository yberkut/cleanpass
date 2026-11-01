package mergetest

import (
	"slices"
	"time"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The moves of the versions of entries.
func init() {
	register("edit entry", editEntry)
	register("edit in two copies", editInTwoCopies)
	register("edit behind the clock", editBehindTheClock)
	register("read entry", readEntry)
	register("move entry", moveEntry)
	register("add entry", addEntry)
	register("trim history", trimHistory)
	register("save again", saveAgain)
	register("carry the file", carryFile)
	register("set the clock", setClock)
}

// edits holds the ways an edit can change the content of a version.
var edits = []func(t *rapid.T, s *snapshot.Snapshot, v *snapshot.EntryVersion){
	func(t *rapid.T, _ *snapshot.Snapshot, v *snapshot.EntryVersion) {
		setField(v, "Password", drawSecret(t), true)
	},
	func(t *rapid.T, _ *snapshot.Snapshot, v *snapshot.EntryVersion) {
		setField(v, "Title", rapid.SampledFrom(titles).Draw(t, "title"), false)
	},
	func(t *rapid.T, _ *snapshot.Snapshot, v *snapshot.EntryVersion) {
		setField(v, "URL", rapid.SampledFrom(urls).Draw(t, "URL"), false)
	},
	func(t *rapid.T, _ *snapshot.Snapshot, v *snapshot.EntryVersion) {
		setField(v, "Custom", drawText(t), false)
	},
	func(t *rapid.T, _ *snapshot.Snapshot, v *snapshot.EntryVersion) {
		v.Tags = rapid.SampledFrom(tagSets).Draw(t, "tags")
	},
	func(t *rapid.T, _ *snapshot.Snapshot, v *snapshot.EntryVersion) {
		v.ForegroundColor = rapid.SampledFrom(colors).Draw(t, "color")
	},
	func(t *rapid.T, _ *snapshot.Snapshot, v *snapshot.EntryVersion) {
		v.IconID = int64(rapid.IntRange(0, 3).Draw(t, "icon"))
	},
	func(t *rapid.T, s *snapshot.Snapshot, v *snapshot.EntryVersion) { attach(t, s, v) },
	func(t *rapid.T, _ *snapshot.Snapshot, v *snapshot.EntryVersion) {
		v.AutoType.Enabled = !v.AutoType.Enabled
	},
	func(t *rapid.T, _ *snapshot.Snapshot, v *snapshot.EntryVersion) {
		v.CustomData = append(v.CustomData, snapshot.CustomDataItem{Key: "KPXC_BROWSER", Value: drawText(t)})
	},
}

func setField(v *snapshot.EntryVersion, key, value string, protected bool) {
	for i := range v.Fields {
		if v.Fields[i].Key == key {
			v.Fields[i].Value = value
			return
		}
	}
	v.Fields = append(v.Fields, snapshot.Field{Key: key, Value: value, Protected: protected})
}

// edit makes a new version of the entry.
func edit(t *rapid.T, s *snapshot.Snapshot, e *snapshot.Entry, stamp time.Time, keepOld bool) {
	if keepOld {
		e.History = append(e.History, cloneVersion(&e.EntryVersion))
	}
	rapid.SampledFrom(edits).Draw(t, "edit")(t, s, &e.EntryVersion)
	e.Times.LastModification = stamp
}

// editEntry makes a new version. KeePassXC keeps the old one in the history, but other
// programs and a history limit may not.
func editEntry(t *rapid.T, _ *world, f *fork) {
	if ref, ok := f.pickEntry(t); ok {
		edit(t, f.snapshot, ref.entry(), f.tick(t), rapid.Bool().Draw(t, "keep the old version"))
	}
}

// gaps holds distances between two edits on both sides of the limit of 24 hours.
var gaps = []time.Duration{0, time.Second, time.Hour, day - time.Second, day, day + time.Second, 10 * day}

// editInTwoCopies makes two copies edit the same entry a chosen time apart, because edits
// on a random entry rarely meet on the limit of 24 hours or in the same second.
func editInTwoCopies(t *rapid.T, w *world, f *fork) {
	other := rapid.SampledFrom(w.forks).Draw(t, "other copy")
	ref, ok := f.pickEntry(t)
	if !ok || other == f {
		return
	}
	mine := ref.entry()
	for _, candidate := range other.entries() {
		if theirs := candidate.entry(); theirs.UUID == mine.UUID {
			stamp := f.tick(t)
			edit(t, f.snapshot, mine, stamp, rapid.Bool().Draw(t, "keep the old version"))
			edit(t, other.snapshot, theirs, stamp.Add(rapid.SampledFrom(gaps).Draw(t, "gap")), rapid.Bool().Draw(t, "keep the old version"))
			return
		}
	}
}

// editBehindTheClock makes an edit whose time lies before the version it replaces, as a
// system with a clock that runs behind does. The history knows the order, the clock does not.
func editBehindTheClock(t *rapid.T, _ *world, f *fork) {
	if ref, ok := f.pickEntry(t); ok {
		e := ref.entry()
		edit(t, f.snapshot, e, e.Times.LastModification.Add(-rapid.SampledFrom(gaps).Draw(t, "gap")), true)
	}
}

// readEntry changes only what a read changes.
func readEntry(t *rapid.T, _ *world, f *fork) {
	ref, ok := f.pickEntry(t)
	if !ok {
		return
	}
	e := ref.entry()
	e.Times.LastAccess = f.tick(t)
	e.Times.UsageCount += int64(rapid.IntRange(1, 3).Draw(t, "uses"))
}

// moveEntry moves an entry to another group. Moving changes only its location.
func moveEntry(t *rapid.T, _ *world, f *fork) {
	ref, ok := f.pickEntry(t)
	if !ok {
		return
	}
	groups := f.groups()
	target := rapid.SampledFrom(groups).Draw(t, "target group")
	if target == ref.group {
		return
	}
	moved := *ref.entry()
	previous := ref.group.UUID
	moved.PreviousParentGroup = &previous
	moved.Times.LocationChanged = f.tick(t)
	ref.group.Entries = slices.Delete(ref.group.Entries, ref.index, ref.index+1)
	target.Entries = append(target.Entries, moved)
}

// addEntry adds an entry only this copy has.
func addEntry(t *rapid.T, w *world, f *fork) {
	target := rapid.SampledFrom(f.groups()).Draw(t, "target group")
	target.Entries = append(target.Entries, w.newEntry(t, f.snapshot, f.tick(t)))
}

// trimHistory forgets the oldest version, as KeePassXC does above its history limit.
func trimHistory(t *rapid.T, _ *world, f *fork) {
	ref, ok := f.pickEntry(t)
	if !ok || len(ref.entry().History) == 0 {
		return
	}
	e := ref.entry()
	e.History = slices.Delete(e.History, 0, 1)
}

// saveAgain does what a plain save does: it renews the service data and may write the same
// content in another order, without any change to the versions.
func saveAgain(t *rapid.T, _ *world, f *fork) {
	f.snapshot.Meta.CustomData = []snapshot.CustomDataItem{
		{Key: snapshot.CustomDataRandomSlug, Value: drawText(t), LastModification: f.tick(t)},
	}
	ref, ok := f.pickEntry(t)
	if !ok {
		return
	}
	v := &ref.entry().EntryVersion
	v.CustomData = append(v.CustomData, snapshot.CustomDataItem{Key: snapshot.CustomDataLastModified, Value: drawText(t)})
	slices.Reverse(v.Fields)
	v.Tags = rapid.SampledFrom([]string{v.Tags, respell(v.Tags)}).Draw(t, "spelling of the tags")
}

// respell writes the same tags with the other separator.
func respell(tags string) string {
	result := []byte(tags)
	for i, c := range result {
		switch c {
		case ',':
			result[i] = ';'
		case ';':
			result[i] = ','
		}
	}
	return string(result)
}

// carryFile replaces this copy with another one, as a copy over a flash drive does. Edits
// after it give a copy that lags behind or a copy that is ahead.
func carryFile(t *rapid.T, w *world, f *fork) {
	f.snapshot = Clone(rapid.SampledFrom(w.forks).Draw(t, "source").snapshot)
}

// setClock makes the clock of this system wrong in another way.
func setClock(t *rapid.T, _ *world, f *fork) {
	f.clock = f.clock.Add(rapid.SampledFrom(clockErrors).Draw(t, "clock error"))
}

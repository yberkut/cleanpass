package mergetest

import (
	"strings"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The moves that give the same accounts under new UUIDs, as an import does.
func init() {
	register("import an entry", importEntry)
	register("import a database", importDatabase)
}

// importChanges holds what can happen to an entry when it arrives under a new UUID. The first
// ones keep its content, so the pair of the old entry and the new one has the same content.
var importChanges = []func(t *rapid.T, f *fork, e *snapshot.Entry){
	func(*rapid.T, *fork, *snapshot.Entry) {},
	func(t *rapid.T, f *fork, e *snapshot.Entry) { renewTimes(e, f, t) },
	func(_ *rapid.T, _ *fork, e *snapshot.Entry) {
		setField(&e.EntryVersion, "Title", " "+strings.ToUpper(fieldOf(&e.EntryVersion, "Title"))+" ", false)
		setField(&e.EntryVersion, "UserName", strings.ToUpper(fieldOf(&e.EntryVersion, "UserName"))+"\t", false)
	},
	func(t *rapid.T, _ *fork, e *snapshot.Entry) {
		setField(&e.EntryVersion, "Password", drawSecret(t), true)
	},
	func(_ *rapid.T, _ *fork, e *snapshot.Entry) {
		setField(&e.EntryVersion, "Title", fieldOf(&e.EntryVersion, "Title")+" (imported)", false)
		if url := fieldOf(&e.EntryVersion, "URL"); url != "" {
			setField(&e.EntryVersion, "URL", url+"/imported", false)
		}
	},
}

func fieldOf(v *snapshot.EntryVersion, key string) string {
	for i := range v.Fields {
		if v.Fields[i].Key == key {
			return v.Fields[i].Value
		}
	}
	return ""
}

// renewTimes writes the times of a new entry, as an import from a format without times does.
func renewTimes(e *snapshot.Entry, f *fork, t *rapid.T) {
	stamp := f.tick(t)
	e.Times.Creation, e.Times.LastModification, e.Times.LastAccess, e.Times.LocationChanged = stamp, stamp, stamp, stamp
	e.Times.UsageCount = 0
}

// importEntry adds an entry of any copy to this copy under a new UUID.
func importEntry(t *rapid.T, w *world, f *fork) {
	source := rapid.SampledFrom(w.forks).Draw(t, "source")
	ref, ok := source.pickEntry(t)
	if !ok {
		return
	}
	imported := cloneEntry(ref.entry())
	imported.UUID = w.newUUID()
	if !rapid.Bool().Draw(t, "keep the history") {
		imported.History = nil
	}
	rapid.SampledFrom(importChanges).Draw(t, "import change")(t, f, &imported)
	for _, v := range append([]snapshot.EntryVersion{imported.EntryVersion}, imported.History...) {
		for _, a := range v.Attachments {
			f.snapshot.Attachments[a.ID] = source.snapshot.Attachments[a.ID]
		}
	}
	target := rapid.SampledFrom(f.groups()).Draw(t, "target group")
	target.Entries = append(target.Entries, imported)
}

// importDatabase replaces this copy with an import of any copy: the same data, and no UUID in
// common, not even the one of the root group. This is a copy that began as a new database.
func importDatabase(t *rapid.T, w *world, f *fork) {
	source := rapid.SampledFrom(w.forks).Draw(t, "source")
	imported := Clone(source.snapshot)
	imported.Deletions = nil
	keepHistory := rapid.Bool().Draw(t, "keep the history")
	newTimes := rapid.Bool().Draw(t, "new times")
	groupIDs := map[snapshot.UUID]snapshot.UUID{}
	imported.Root.Walk(func(g *snapshot.Group) {
		id := w.newUUID()
		groupIDs[g.UUID], g.UUID = id, id
		g.PreviousParentGroup = nil
		for i := range g.Entries {
			e := &g.Entries[i]
			e.UUID = w.newUUID()
			e.PreviousParentGroup = nil
			for j := range e.History {
				e.History[j].PreviousParentGroup = nil
			}
			if !keepHistory {
				e.History = nil
			}
			if newTimes {
				renewTimes(e, f, t)
			}
		}
	})
	imported.Meta.RecycleBin = groupIDs[imported.Meta.RecycleBin]
	f.snapshot = imported
}

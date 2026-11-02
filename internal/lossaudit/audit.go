// Package lossaudit is the Loss Audit: the independent proof that a merge lost nothing.
//
// It compares the input copies with the result and lists what is gone. It works from the
// words of the spec and from the snapshots alone, and it uses no code of the Merge Engine,
// so a bug in the engine cannot hide itself here. Only an explicit decision of the human lets
// data go, see Allowed. A version that another one holds entirely is not gone, see keptVersions.
//
// A Loss names a thing and never holds a value, so a list of losses can reach the Report.
package lossaudit

import (
	"bytes"
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// Input is what an audit looks at.
type Input struct {
	Copies []*snapshot.Snapshot
	// Result must not be nil.
	Result *snapshot.Snapshot
	// Allowed is what may leave its place. Nothing else may.
	Allowed Allowed
}

// Allowed holds the reasons for which data may leave its place. Such a reason never lets
// more go than it names: deleting a group does not cover its entries, and no reason covers a
// deletion record, a custom icon, custom data or the bytes of an attachment that a kept
// version refers to.
//
// A decision of an earlier merge is not here. The audit does not take that from the engine: it
// reads it from the copies, see rememberedDeletions.
type Allowed struct {
	// Deleted holds the entries and the groups the human explicitly chose to delete in this merge.
	Deleted []snapshot.UUID
	// Absorbed maps an entry the human merged into another one to the entry that took its
	// versions. The versions themselves must stay. Deleting the entry that took them
	// covers them too.
	Absorbed map[snapshot.UUID]snapshot.UUID
	// MergedGroups maps a group joined by its path to the group that took its place. All
	// the groups joined this way count as one group, and that group must stay.
	MergedGroups map[snapshot.UUID]snapshot.UUID
}

// Kind says what was lost.
type Kind int

// The kinds of loss.
const (
	// VersionLost: a version of an entry is not in the result. Object is the entry and
	// Time is the time of the version.
	VersionLost Kind = iota + 1
	// AttachmentLost: the result lacks the bytes of an attachment, or holds other bytes, or
	// no longer protects them. Object is the first entry by UUID that refers to the bytes.
	AttachmentLost
	// DeletionLost: a record about a deleted object is not in the result, or has an older
	// time. Object is the deleted object and Time is the newest time of the record.
	DeletionLost
	// GroupLost: a group is not in the result. Object is the group.
	GroupLost
	// GroupFieldsLost: a group in the result lacks the fields of its newest version. Object
	// is the group.
	GroupFieldsLost
	// CustomIconLost: a custom icon is not in the result, or not in its newest version.
	// Object is the icon.
	CustomIconLost
	// CustomDataLost: an item of the custom data of the database is not in the result, or
	// has lost its newest value. Name is the key of the item.
	CustomDataLost
	// SettingLost: a setting with a time of its own has lost its newest value. Name says
	// which setting.
	SettingLost
)

// Loss is one thing the copies hold and the result does not. It never holds a value.
type Loss struct {
	Kind Kind
	// Object is the entry, the group, the custom icon or the deleted object.
	Object snapshot.UUID
	// Time tells the lost version or the lost record apart. It is in UTC.
	Time time.Time
	// Attachment is the content of the lost attachment.
	Attachment snapshot.AttachmentID
	// Name is the key of lost custom data or the name of a lost setting.
	Name string
}

// Audit lists what the copies hold and the result does not, in a fixed order. An empty list
// means that nothing is lost.
func Audit(in Input) []Loss {
	a := auditor{
		in:             in,
		chosenToDelete: map[snapshot.UUID]bool{},
		remembered:     rememberedDeletions(in.Copies),
		kept:           keptInResult(in.Result),
		losses:         map[Loss]bool{},
	}
	for _, id := range in.Allowed.Deleted {
		a.chosenToDelete[id] = true
	}
	a.checkVersions()
	a.checkAttachments()
	a.checkDeletions()
	a.checkGroups()
	a.checkCustomIcons()
	a.checkCustomData()
	a.checkSettings()
	return slices.SortedFunc(maps.Keys(a.losses), compareLosses)
}

// auditor is the state of one audit.
type auditor struct {
	in             Input
	chosenToDelete map[snapshot.UUID]bool
	// remembered holds the keys of the deletions the copies remember.
	remembered map[string]bool
	// kept holds the versions of each entry of the result.
	kept   map[snapshot.UUID]keptVersions
	losses map[Loss]bool
}

func (a *auditor) lose(loss Loss) { a.losses[loss] = true }

func compareLosses(x, y Loss) int {
	return cmp.Or(
		cmp.Compare(x.Kind, y.Kind),
		compareUUIDs(x.Object, y.Object),
		x.Time.Compare(y.Time),
		bytes.Compare(x.Attachment[:], y.Attachment[:]),
		strings.Compare(x.Name, y.Name),
	)
}

func compareUUIDs(x, y snapshot.UUID) int { return bytes.Compare(x[:], y[:]) }

// walkEntries calls visit for every entry of a copy.
func walkEntries(s *snapshot.Snapshot, visit func(*snapshot.Entry)) {
	s.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			visit(&g.Entries[i])
		}
	})
}

// walkVersions calls visit for the main version and each history version of an entry.
func walkVersions(e *snapshot.Entry, visit func(*snapshot.EntryVersion)) {
	visit(&e.EntryVersion)
	for i := range e.History {
		visit(&e.History[i])
	}
}

func keptInResult(result *snapshot.Snapshot) map[snapshot.UUID]keptVersions {
	kept := map[snapshot.UUID]keptVersions{}
	walkEntries(result, func(e *snapshot.Entry) {
		if kept[e.UUID] == nil {
			kept[e.UUID] = keptVersions{}
		}
		walkVersions(e, kept[e.UUID].add)
	})
	return kept
}

// isKept says the result holds the version under one of the places. A version in the history and
// a main version count the same, see keptVersions.covers.
func (a *auditor) isKept(places []snapshot.UUID, v *snapshot.EntryVersion) bool {
	return slices.ContainsFunc(places, func(p snapshot.UUID) bool { return a.kept[p].covers(v) })
}

// checkVersions looks for each version of each entry of each copy in the result. Only a decision
// of the human lets one stay out: of this merge for a whole entry, or of an earlier one for a
// version that decision names.
func (a *auditor) checkVersions() {
	for _, c := range a.in.Copies {
		walkEntries(c, func(e *snapshot.Entry) {
			places := followLinks(e.UUID, a.in.Allowed.Absorbed)
			if a.deleted(places) {
				return
			}
			walkVersions(e, func(v *snapshot.EntryVersion) {
				if !a.isKept(places, v) && !a.humanSawItDeleted(places, v) {
					a.lose(Loss{Kind: VersionLost, Object: e.UUID, Time: v.Times.LastModification.UTC()})
				}
			})
		})
	}
}

// The memory of decisions is a contract with the Merge Engine, written down in the custom data of
// a copy. The audit reads it from the copies, not from the result and not from the engine, so a
// bug in the engine cannot excuse itself. A key names an entry the human chose to delete and the
// time of one version the human saw then: a version with any other time is news to the human.
const rememberedDeletionPrefix = "cleanpass/accepted/"

func rememberedDeletions(copies []*snapshot.Snapshot) map[string]bool {
	remembered := map[string]bool{}
	for _, c := range copies {
		for i := range c.Meta.CustomData {
			if key := c.Meta.CustomData[i].Key; strings.HasPrefix(key, rememberedDeletionPrefix) {
				remembered[key] = true
			}
		}
	}
	return remembered
}

// humanSawItDeleted says a copy remembers that the human deleted one of the places, knowing a
// version with this time.
func (a *auditor) humanSawItDeleted(places []snapshot.UUID, v *snapshot.EntryVersion) bool {
	seen := "/" + v.Times.LastModification.UTC().Format(time.RFC3339Nano)
	return slices.ContainsFunc(places, func(p snapshot.UUID) bool {
		return a.remembered[rememberedDeletionPrefix+p.String()+seen]
	})
}

// checkAttachments looks at the bytes of every attachment a kept version refers to, by
// content: the result must hold bytes that hash to the ID of the reference. A reference is
// what makes bytes dear. A file with bytes that nothing refers to stops at the Gateway, and
// a copy can forget a version with its last reference, for example at a history limit.
func (a *auditor) checkAttachments() {
	held := map[snapshot.AttachmentID]bool{}
	protected := map[snapshot.AttachmentID]bool{}
	for _, c := range a.in.Copies {
		for id, attachment := range c.Attachments {
			held[id] = true
			protected[id] = protected[id] || attachment.Protected
		}
	}
	needed := map[snapshot.AttachmentID]snapshot.UUID{}
	for _, c := range a.in.Copies {
		walkEntries(c, func(e *snapshot.Entry) {
			places := followLinks(e.UUID, a.in.Allowed.Absorbed)
			if a.deleted(places) {
				return
			}
			walkVersions(e, func(v *snapshot.EntryVersion) {
				if a.humanSawItDeleted(places, v) && !a.isKept(places, v) {
					return
				}
				for i := range v.Attachments {
					id := v.Attachments[i].ID
					if first, seen := needed[id]; held[id] && (!seen || compareUUIDs(e.UUID, first) < 0) {
						needed[id] = e.UUID
					}
				}
			})
		})
	}
	for id, entry := range needed {
		kept, ok := a.in.Result.Attachments[id]
		if !ok || snapshot.NewAttachmentID(kept.Content) != id || (protected[id] && !kept.Protected) {
			a.lose(Loss{Kind: AttachmentLost, Object: entry, Attachment: id})
		}
	}
}

// checkDeletions wants each record in the result with its newest time. No decision lets a
// record go: without it an old copy would bring a deleted entry back as a new one.
func (a *auditor) checkDeletions() {
	newestOf := func(deletions []snapshot.Deletion, into map[snapshot.UUID]time.Time) {
		for _, d := range deletions {
			if at, seen := into[d.UUID]; !seen || d.Time.After(at) {
				into[d.UUID] = d.Time
			}
		}
	}
	wanted, kept := map[snapshot.UUID]time.Time{}, map[snapshot.UUID]time.Time{}
	for _, c := range a.in.Copies {
		newestOf(c.Deletions, wanted)
	}
	newestOf(a.in.Result.Deletions, kept)
	for id, at := range wanted {
		if got, ok := kept[id]; !ok || got.Before(at) {
			a.lose(Loss{Kind: DeletionLost, Object: id, Time: at.UTC()})
		}
	}
}

// checkGroups wants each group in the result with the fields of its newest version. The file
// has no history of groups, so the older versions may go, as the spec says. All root groups
// are one group, and so are the groups joined by their path.
func (a *auditor) checkGroups() {
	roots := map[snapshot.UUID]bool{}
	for _, c := range a.in.Copies {
		roots[c.Root.UUID] = true
	}
	wanted := map[snapshot.UUID]*newest[*snapshot.Group]{}
	for _, c := range a.in.Copies {
		c.Root.Walk(func(g *snapshot.Group) {
			path := followLinks(g.UUID, a.in.Allowed.MergedGroups)
			if a.deleted(path) {
				return
			}
			survivor := path[len(path)-1]
			if roots[survivor] {
				survivor = a.in.Result.Root.UUID
			}
			see(wanted, survivor, g.Times.LastModification, g)
		})
	}
	kept := map[snapshot.UUID]*snapshot.Group{}
	a.in.Result.Root.Walk(func(g *snapshot.Group) { kept[g.UUID] = g })
	for id, versions := range wanted {
		got, ok := kept[id]
		switch {
		case !ok:
			a.lose(Loss{Kind: GroupLost, Object: id})
		case !slices.ContainsFunc(versions.items, func(v *snapshot.Group) bool { return holdsGroup(got, v) }):
			a.lose(Loss{Kind: GroupFieldsLost, Object: id})
		}
	}
}

// checkCustomIcons wants the icons of all copies in the result, each with its newest content.
func (a *auditor) checkCustomIcons() {
	wanted := map[snapshot.UUID]*newest[string]{}
	for _, c := range a.in.Copies {
		for i := range c.Meta.CustomIcons {
			icon := &c.Meta.CustomIcons[i]
			see(wanted, icon.UUID, icon.LastModification, customIconForm(icon))
		}
	}
	icons := a.in.Result.Meta.CustomIcons
	kept := make([]string, len(icons))
	for i := range icons {
		kept[i] = customIconForm(&icons[i])
	}
	for id, versions := range wanted {
		if !containsAny(kept, versions.items) {
			a.lose(Loss{Kind: CustomIconLost, Object: id})
		}
	}
}

// checkCustomData wants the custom data of all copies in the result, each item with its
// newest value. The custom data of an entry version is part of that version, and that of a
// group is part of the fields of that group.
func (a *auditor) checkCustomData() {
	wanted := map[string]*newest[string]{}
	for _, c := range a.in.Copies {
		for i := range c.Meta.CustomData {
			if item := &c.Meta.CustomData[i]; !item.IsService() {
				see(wanted, item.Key, item.LastModification, customDataForm(item))
			}
		}
	}
	kept := customDataForms(a.in.Result.Meta.CustomData)
	for key, versions := range wanted {
		if !containsAny(kept, versions.items) {
			a.lose(Loss{Kind: CustomDataLost, Name: key})
		}
	}
}

// settings lists the settings that have a time of their own and that a person fills in. The
// other settings of a database are no data of the person, so the audit leaves them out.
var settings = []struct {
	name string
	read func(m *snapshot.Meta) (value string, changed time.Time)
}{
	{"DatabaseName", func(m *snapshot.Meta) (string, time.Time) { return m.DatabaseName, m.DatabaseNameChanged }},
	{"DatabaseDescription", func(m *snapshot.Meta) (string, time.Time) { return m.DatabaseDescription, m.DatabaseDescriptionChanged }},
	{"DefaultUserName", func(m *snapshot.Meta) (string, time.Time) { return m.DefaultUserName, m.DefaultUserNameChanged }},
}

// checkSettings wants the newest value of each setting, by the time of that setting.
func (a *auditor) checkSettings() {
	for _, setting := range settings {
		var versions newest[string]
		for _, c := range a.in.Copies {
			value, changed := setting.read(&c.Meta)
			versions.see(changed, value)
		}
		value, _ := setting.read(&a.in.Result.Meta)
		if len(versions.items) > 0 && !slices.Contains(versions.items, value) {
			a.lose(Loss{Kind: SettingLost, Name: setting.name})
		}
	}
}

// newest keeps the items with the largest time. Several items can have that time, and then
// the result may hold any of them: nothing tells which one is the last.
type newest[T any] struct {
	at    time.Time
	items []T
}

func (n *newest[T]) see(at time.Time, item T) {
	switch {
	case len(n.items) == 0 || at.After(n.at):
		n.at, n.items = at, []T{item}
	case at.Equal(n.at):
		n.items = append(n.items, item)
	}
}

// see records an item under its key.
func see[K comparable, T any](into map[K]*newest[T], key K, at time.Time, item T) {
	n := into[key]
	if n == nil {
		n = &newest[T]{}
		into[key] = n
	}
	n.see(at, item)
}

func containsAny(kept, wanted []string) bool {
	return slices.ContainsFunc(wanted, func(form string) bool { return slices.Contains(kept, form) })
}

// deleted says that the human chose to delete one of the places.
func (a *auditor) deleted(places []snapshot.UUID) bool {
	return slices.ContainsFunc(places, func(p snapshot.UUID) bool { return a.chosenToDelete[p] })
}

// followLinks gives id and all it leads to. A loop ends the walk, so a wrong decision can
// neither hang the audit nor hide a loss.
func followLinks(id snapshot.UUID, links map[snapshot.UUID]snapshot.UUID) []snapshot.UUID {
	path := []snapshot.UUID{id}
	for {
		next, ok := links[path[len(path)-1]]
		if !ok || slices.Contains(path, next) {
			return path
		}
		path = append(path, next)
	}
}

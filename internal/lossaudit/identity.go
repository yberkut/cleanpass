package lossaudit

import (
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// keptVersions holds what a result keeps of one entry: for each identity without custom data,
// the custom data of the versions with that identity.
type keptVersions map[string][][]string

func (k keptVersions) add(v *snapshot.EntryVersion) {
	identity := identityWithoutCustomData(v)
	k[identity] = append(k[identity], customDataForms(v.CustomData))
}

// covers says the version is in the result: the same version, or one that says all it says.
//
// The second way exists because KeePassXC leaves the custom data of an entry out of the history
// version an edit makes: a version with the same time, the same content and custom data that
// holds all of this one and more. Nothing else lets a version go without a decision.
func (k keptVersions) covers(v *snapshot.EntryVersion) bool {
	wanted := customDataForms(v.CustomData)
	return slices.ContainsFunc(k[identityWithoutCustomData(v)], func(held []string) bool {
		return set(held) == set(wanted) || holdsMore(held, wanted)
	})
}

// holdsMore says held holds every item of wanted and at least one other, as sets.
func holdsMore(held, wanted []string) bool {
	items := map[string]bool{}
	for _, item := range held {
		items[item] = true
	}
	different := map[string]bool{}
	for _, item := range wanted {
		if !items[item] {
			return false
		}
		different[item] = true
	}
	return len(different) < len(items)
}

// identityWithoutCustomData gives the identity of a version as the spec defines it, its time
// and its content, apart from the custom data. keptVersions compares that part by itself.
//
// This is written from the spec, apart from the fingerprint of the Merge Engine, and it
// stays in memory: it holds secret values in clear text, so no Loss carries it.
//
// The identity leaves out what the spec leaves out or a plain save can change: LastAccess,
// UsageCount, LocationChanged, PreviousParentGroup, the order of anything and the spelling of
// the tags. customDataForms leaves out service custom data and the time of a custom data item.
func identityWithoutCustomData(v *snapshot.EntryVersion) string {
	fields := make([]string, len(v.Fields))
	for i := range v.Fields {
		f := &v.Fields[i]
		fields[i] = record(text(f.Key), text(f.Value), flag(f.Protected))
	}
	attachments := make([]string, len(v.Attachments))
	for i := range v.Attachments {
		a := &v.Attachments[i]
		attachments[i] = record(text(a.Name), hex.EncodeToString(a.ID[:]))
	}
	associations := make([]string, len(v.AutoType.Associations))
	for i := range v.AutoType.Associations {
		a := &v.AutoType.Associations[i]
		associations[i] = record(text(a.Window), text(a.KeystrokeSequence))
	}
	return record(
		moment(v.Times.LastModification),
		set(fields),
		set(attachments),
		tagSet(v.Tags),
		text(v.ForegroundColor),
		text(v.BackgroundColor),
		number(v.IconID),
		v.CustomIcon.String(),
		text(v.OverrideURL),
		optional(v.QualityCheck),
		moment(v.Times.Creation),
		moment(v.Times.Expiry),
		flag(v.Times.Expires),
		flag(v.AutoType.Enabled),
		number(v.AutoType.Obfuscation),
		text(v.AutoType.DefaultSequence),
		set(associations),
	)
}

// holdsGroup says that the group got holds what a person set on the group version want. It
// leaves out usage, the place in the tree and the state of the window, and it lets the group
// hold more custom data than that version.
func holdsGroup(got, want *snapshot.Group) bool {
	return groupFields(got) == groupFields(want) && isSubset(customDataForms(want.CustomData), customDataForms(got.CustomData))
}

// defaultGroupIcon is the icon KeePassXC gives a new group. With no custom icon it is no icon a
// person has chosen.
const defaultGroupIcon = 48

// holdsJoined says that the group got, which several groups went into, holds what a person set on
// the group version want: the name, each other field the same or empty in want, and all its
// tags and custom data. An empty field is no field of that group: the standard icon with no
// custom icon, an expiry that is off, a setting that comes from the parent. The time of creation
// is no setting of a person, since an import writes a new one.
func holdsJoined(got, want *snapshot.Group) bool {
	sameOrEmpty := func(empty, same bool) bool { return empty || same }
	return got.Name == want.Name &&
		sameOrEmpty(want.Notes == "", got.Notes == want.Notes) &&
		sameOrEmpty(want.IconID == defaultGroupIcon && want.CustomIcon.IsZero(), got.IconID == want.IconID && got.CustomIcon == want.CustomIcon) &&
		sameOrEmpty(!want.Times.Expires, got.Times.Expires && got.Times.Expiry.Equal(want.Times.Expiry)) &&
		sameOrEmpty(want.DefaultAutoTypeSequence == "", got.DefaultAutoTypeSequence == want.DefaultAutoTypeSequence) &&
		sameOrEmpty(want.EnableAutoType == nil, optional(got.EnableAutoType) == optional(want.EnableAutoType)) &&
		sameOrEmpty(want.EnableSearching == nil, optional(got.EnableSearching) == optional(want.EnableSearching)) &&
		isSubset(tagList(want.Tags), tagList(got.Tags)) &&
		isSubset(customDataForms(want.CustomData), customDataForms(got.CustomData))
}

// comesFrom says that each value a person sets on the group got, which several groups went
// into, comes from one of those groups. A value from nowhere is a sign that the value of a group
// went somewhere else. Custom data may be more, as with one group, see holdsGroup.
func comesFrom(got *snapshot.Group, groups []*snapshot.Group) bool {
	var tags []string
	for _, g := range groups {
		tags = append(tags, tagList(g.Tags)...)
	}
	return slices.ContainsFunc(groups, func(g *snapshot.Group) bool { return g.Notes == got.Notes }) &&
		slices.ContainsFunc(groups, func(g *snapshot.Group) bool { return g.IconID == got.IconID && g.CustomIcon == got.CustomIcon }) &&
		(!got.Times.Expires || slices.ContainsFunc(groups, func(g *snapshot.Group) bool { return g.Times.Expires && g.Times.Expiry.Equal(got.Times.Expiry) })) &&
		slices.ContainsFunc(groups, func(g *snapshot.Group) bool { return g.DefaultAutoTypeSequence == got.DefaultAutoTypeSequence }) &&
		slices.ContainsFunc(groups, func(g *snapshot.Group) bool { return optional(g.EnableAutoType) == optional(got.EnableAutoType) }) &&
		slices.ContainsFunc(groups, func(g *snapshot.Group) bool { return optional(g.EnableSearching) == optional(got.EnableSearching) }) &&
		isSubset(tagList(got.Tags), tags)
}

func isSubset(items, of []string) bool {
	return !slices.ContainsFunc(items, func(item string) bool { return !slices.Contains(of, item) })
}

func groupFields(g *snapshot.Group) string {
	return record(
		text(g.Name),
		text(g.Notes),
		number(g.IconID),
		g.CustomIcon.String(),
		moment(g.Times.Creation),
		moment(g.Times.Expiry),
		flag(g.Times.Expires),
		text(g.DefaultAutoTypeSequence),
		optional(g.EnableAutoType),
		optional(g.EnableSearching),
		tagSet(g.Tags),
	)
}

func customIconForm(icon *snapshot.CustomIcon) string {
	return record(icon.UUID.String(), text(icon.Data), text(icon.Name))
}

// customDataForms leaves out the service items, because KeePassXC renews them on every save,
// and the time of an item, because a save can change it too.
func customDataForms(items []snapshot.CustomDataItem) []string {
	var forms []string
	for i := range items {
		if !items[i].IsService() {
			forms = append(forms, customDataForm(&items[i]))
		}
	}
	return forms
}

func customDataForm(item *snapshot.CustomDataItem) string {
	return record(text(item.Key), text(item.Value))
}

// tagSet gives one form for the same tags. KeePassXC takes comma and semicolon as separators
// and sorts the tags when it loads a file, so the same tags have several spellings.
func tagSet(tags string) string { return set(tagList(tags)) }

// tagList gives the tags sorted, each once, in the form of text.
func tagList(tags string) []string {
	var result []string
	for _, tag := range strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == ';' }) {
		if tag = strings.TrimSpace(tag); tag != "" {
			result = append(result, text(tag))
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

// The forms below give one unambiguous text for each value. A text value is quoted, so it
// cannot break out of its place, and two different values never give the same form.

func record(parts ...string) string { return "(" + strings.Join(parts, " ") + ")" }

// set gives the same form for the same items in any order.
func set(items []string) string {
	sorted := slices.Sorted(slices.Values(items))
	return "[" + strings.Join(sorted, " ") + "]"
}

func text(s string) string { return strconv.Quote(s) }

func number(n int64) string { return strconv.FormatInt(n, 10) }

func flag(b bool) string { return strconv.FormatBool(b) }

// optional gives three different forms for nil, false and true.
func optional(b *bool) string {
	if b == nil {
		return "unset"
	}
	return flag(*b)
}

// moment gives the same form for the same instant in any time zone.
func moment(t time.Time) string {
	return number(t.Unix()) + "." + strconv.Itoa(t.Nanosecond())
}

package judge

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// MismatchError means KeePassXC sees in the result something else than it should hold. Place
// names a part of a database and never a value.
type MismatchError struct{ Place string }

func (e *MismatchError) Error() string { return "KeePassXC sees something else at " + e.Place }

// CompareExport compares what keepassxc-cli export printed with the expected result: all the
// data, and not only how much of it there is. The bytes of attachments are not in an export, so
// it looks at their names here.
//
// It gives ErrCannotRead or a MismatchError. Neither holds a value from the text.
func CompareExport(raw []byte, want *snapshot.Snapshot) error {
	seen, err := parseExport(raw)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCannotRead, err)
	}
	expected, shown := expectedView(want, seen), seenView(seen)
	takeOverMissingTimes(reflect.ValueOf(&expected.Meta).Elem(), reflect.ValueOf(&shown.Meta).Elem())
	takeOverMissingTimes(reflect.ValueOf(&expected.Root).Elem(), reflect.ValueOf(&shown.Root).Elem())
	unifyLineBreaks(reflect.ValueOf(expected).Elem())
	if place := snapshot.Difference(expected, shown); place != "" {
		return &MismatchError{Place: place}
	}
	return nil
}

// viewer makes a Snapshot into what can be compared with an export. What it does on both sides is
// about things without meaning, such as the order of fields. With rules, it also does what
// KeePassXC itself does while it reads a file. These lose no data of a person.
type viewer struct{ rules *readingRules }

// readingRules are the facts of a result that say how KeePassXC reads it.
type readingRules struct {
	protection      snapshot.MemoryProtection
	groups, entries map[snapshot.UUID]bool
	// olderFormat says that KeePassXC printed a format older than KDBX 4.1.
	olderFormat bool
}

// expectedView gives what KeePassXC should show for a result. It leaves the Snapshot as it is.
func expectedView(want, seen *snapshot.Snapshot) *snapshot.Snapshot {
	rules := &readingRules{
		protection:  want.Meta.MemoryProtection,
		groups:      map[snapshot.UUID]bool{},
		entries:     map[snapshot.UUID]bool{},
		olderFormat: !printedNewestFormat(seen),
	}
	want.Root.Walk(func(g *snapshot.Group) {
		rules.groups[g.UUID] = true
		for i := range g.Entries {
			rules.entries[g.Entries[i].UUID] = true
		}
	})
	return viewer{rules}.snapshot(want)
}

func seenView(seen *snapshot.Snapshot) *snapshot.Snapshot { return viewer{}.snapshot(seen) }

// printedNewestFormat tells whether the text holds anything only KDBX 4.1 can hold. KeePassXC
// picks the oldest format that fits the data, for a print as for a save. Some of the data does not
// ask for KDBX 4.1 by itself: an old place, and a time of custom data. With no other reason, these
// do not come out of KeePassXC at all, and only the loss of all of them can pass here.
func printedNewestFormat(seen *snapshot.Snapshot) bool {
	for i := range seen.Meta.CustomIcons {
		if icon := &seen.Meta.CustomIcons[i]; icon.Name != "" || !icon.LastModification.IsZero() {
			return true
		}
	}
	for i := range seen.Meta.CustomData {
		if !seen.Meta.CustomData[i].LastModification.IsZero() {
			return true
		}
	}
	found := false
	seen.Root.Walk(func(g *snapshot.Group) {
		found = found || g.Tags != "" || withoutZero(g.PreviousParentGroup) != nil
		for i := range g.Entries {
			found = found || holdsNewestFormat(&g.Entries[i].EntryVersion)
			for j := range g.Entries[i].History {
				found = found || holdsNewestFormat(&g.Entries[i].History[j])
			}
		}
	})
	return found
}

func holdsNewestFormat(v *snapshot.EntryVersion) bool {
	return v.QualityCheck != nil || withoutZero(v.PreviousParentGroup) != nil
}

func (r *readingRules) oldPlace(id *snapshot.UUID) *snapshot.UUID {
	if r != nil && r.olderFormat {
		return nil
	}
	return withoutZero(id)
}

func (v viewer) snapshot(s *snapshot.Snapshot) *snapshot.Snapshot {
	result := &snapshot.Snapshot{
		Meta:      s.Meta,
		Root:      v.group(&s.Root),
		Deletions: slices.Clone(s.Deletions),
	}
	result.Meta.CustomIcons = slices.Clone(s.Meta.CustomIcons)
	result.Meta.CustomData = withoutService(s.Meta.CustomData, v.rules == nil || !v.rules.olderFormat)
	if v.rules != nil {
		// A setting that points at a group that is not there comes out as no group.
		result.Meta.RecycleBin = v.rules.existingGroup(s.Meta.RecycleBin)
		result.Meta.EntryTemplatesGroup = v.rules.existingGroup(s.Meta.EntryTemplatesGroup)
		result.Meta.LastSelectedGroup = v.rules.existingGroup(s.Meta.LastSelectedGroup)
		result.Meta.LastTopVisibleGroup = v.rules.existingGroup(s.Meta.LastTopVisibleGroup)
	}
	return result
}

func (r *readingRules) existingGroup(id snapshot.UUID) snapshot.UUID {
	if r.groups[id] {
		return id
	}
	return snapshot.UUID{}
}

func (v viewer) group(g *snapshot.Group) snapshot.Group {
	result := *g
	result.Tags = sortedTags(g.Tags)
	result.PreviousParentGroup = v.rules.oldPlace(g.PreviousParentGroup)
	result.CustomData = withoutService(g.CustomData, false)
	if v.rules != nil {
		if !g.CustomIcon.IsZero() {
			result.IconID = 0
		}
		if !v.rules.entries[g.LastTopVisibleEntry] {
			result.LastTopVisibleEntry = snapshot.UUID{}
		}
	}
	result.Entries = make([]snapshot.Entry, 0, len(g.Entries))
	for i := range g.Entries {
		entry := snapshot.Entry{UUID: g.Entries[i].UUID, EntryVersion: v.version(&g.Entries[i].EntryVersion)}
		for j := range g.Entries[i].History {
			entry.History = append(entry.History, v.version(&g.Entries[i].History[j]))
		}
		result.Entries = append(result.Entries, entry)
	}
	result.Groups = make([]snapshot.Group, 0, len(g.Groups))
	for i := range g.Groups {
		result.Groups = append(result.Groups, v.group(&g.Groups[i]))
	}
	return result
}

func (v viewer) version(e *snapshot.EntryVersion) snapshot.EntryVersion {
	result := *e
	result.Tags = sortedTags(e.Tags)
	result.PreviousParentGroup = v.rules.oldPlace(e.PreviousParentGroup)
	result.CustomData = withoutService(e.CustomData, false)
	// KeePassXC prints this element only when the check is off.
	if e.QualityCheck != nil && *e.QualityCheck {
		result.QualityCheck = nil
	}
	result.AutoType.Associations = slices.Clone(e.AutoType.Associations)
	// The bytes get their own check, so here an attachment is only a name.
	result.Attachments = nil
	for i := range e.Attachments {
		result.Attachments = append(result.Attachments, snapshot.AttachmentRef{Name: e.Attachments[i].Name})
	}
	slices.SortFunc(result.Attachments, func(a, b snapshot.AttachmentRef) int { return cmp.Compare(a.Name, b.Name) })
	result.Fields = v.fields(e.Fields)
	if v.rules != nil && !e.CustomIcon.IsZero() {
		result.IconID = 0
	}
	return result
}

// fields sorts the fields by name. With rules, each version gets the five standard fields
// KeePassXC gives every entry, and these are protected also by the settings of the database.
func (v viewer) fields(fields []snapshot.Field) []snapshot.Field {
	result := slices.Clone(fields)
	if v.rules != nil {
		protection := map[string]bool{
			"Title":    v.rules.protection.Title,
			"UserName": v.rules.protection.UserName,
			"Password": v.rules.protection.Password,
			"URL":      v.rules.protection.URL,
			"Notes":    v.rules.protection.Notes,
		}
		for key := range protection {
			if !slices.ContainsFunc(result, func(f snapshot.Field) bool { return f.Key == key }) {
				result = append(result, snapshot.Field{Key: key})
			}
		}
		for i := range result {
			result[i].Protected = result[i].Protected || protection[result[i].Key]
		}
	}
	slices.SortFunc(result, func(a, b snapshot.Field) int { return cmp.Compare(a.Key, b.Key) })
	return result
}

// sortedTags gives tags in one spelling. KeePassXC sorts the tags of an entry and takes three
// separators, and the tags of a group it keeps as they are.
func sortedTags(tags string) string {
	var result []string
	for _, part := range strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == ';' || r == '\t' }) {
		if tag := strings.TrimSpace(part); tag != "" {
			result = append(result, tag)
		}
	}
	slices.Sort(result)
	return strings.Join(slices.Compact(result), ",")
}

func withoutZero(id *snapshot.UUID) *snapshot.UUID {
	if id != nil && id.IsZero() {
		return nil
	}
	return id
}

// withoutService drops what KeePassXC renews by itself and sorts the rest by key. KeePassXC
// keeps the time of an item only for the database itself.
func withoutService(items []snapshot.CustomDataItem, keepTimes bool) []snapshot.CustomDataItem {
	var result []snapshot.CustomDataItem
	for i := range items {
		if items[i].IsService() {
			continue
		}
		item := items[i]
		if !keepTimes {
			item.LastModification = time.Time{}
		}
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b snapshot.CustomDataItem) int { return cmp.Compare(a.Key, b.Key) })
	return result
}

var timeType = reflect.TypeFor[time.Time]()

// takeOverMissingTimes makes each time the result does not have the same on both sides. A file
// without a time leaves the choice to KeePassXC, and it takes the present moment.
func takeOverMissingTimes(want, seen reflect.Value) {
	switch {
	case want.Type() == timeType:
		if want.Interface().(time.Time).IsZero() {
			seen.Set(want)
		}
	case want.Kind() == reflect.Struct:
		for i := range want.NumField() {
			takeOverMissingTimes(want.Field(i), seen.Field(i))
		}
	case want.Kind() == reflect.Slice:
		// Lists of other sizes differ anyway, and the comparison will say so.
		for i := range min(want.Len(), seen.Len()) {
			takeOverMissingTimes(want.Index(i), seen.Index(i))
		}
	}
}

// unifyLineBreaks writes each kind of line break as one character in every text, the way an XML
// parser reads the text of an export. KeePassXC prints these characters as they are, and an XML
// parser cannot tell them apart.
func unifyLineBreaks(value reflect.Value) {
	switch value.Kind() {
	case reflect.String:
		value.SetString(strings.ReplaceAll(strings.ReplaceAll(value.String(), "\r\n", "\n"), "\r", "\n"))
	case reflect.Struct:
		if value.Type() == timeType {
			return
		}
		for i := range value.NumField() {
			unifyLineBreaks(value.Field(i))
		}
	case reflect.Slice:
		for i := range value.Len() {
			unifyLineBreaks(value.Index(i))
		}
	}
}

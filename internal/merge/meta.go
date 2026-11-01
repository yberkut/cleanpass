package merge

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

func init() { addRule(phaseMetadata, mergeMetadata) }

// mergeMetadata joins the settings of the database. A setting with a time of its own takes the
// value with the newest time. All other settings come together from the copy with the newest
// SettingsChanged. Custom icons and custom data join as sets. The value itself breaks a tie of
// the times, so the choice does not depend on the order of the copies.
func mergeMetadata(m *merger) {
	metas := make([]*snapshot.Meta, len(m.in.Copies))
	for i := range m.in.Copies {
		metas[i] = &m.in.Copies[i].Snapshot.Meta
	}
	out := plainSettings(slices.MaxFunc(metas, compareSettings))
	for _, meta := range metas {
		newestText(&out.DatabaseName, &out.DatabaseNameChanged, meta.DatabaseName, meta.DatabaseNameChanged)
		newestText(&out.DatabaseDescription, &out.DatabaseDescriptionChanged, meta.DatabaseDescription, meta.DatabaseDescriptionChanged)
		newestText(&out.DefaultUserName, &out.DefaultUserNameChanged, meta.DefaultUserName, meta.DefaultUserNameChanged)
		if cmp.Or(
			meta.MasterKeyChanged.Compare(out.MasterKeyChanged),
			cmp.Compare(meta.MasterKeyChangeRec, out.MasterKeyChangeRec),
			cmp.Compare(meta.MasterKeyChangeForce, out.MasterKeyChangeForce),
		) > 0 {
			out.MasterKeyChanged, out.MasterKeyChangeRec, out.MasterKeyChangeForce = meta.MasterKeyChanged, meta.MasterKeyChangeRec, meta.MasterKeyChangeForce
		}
		if cmp.Or(meta.EntryTemplatesGroupChanged.Compare(out.EntryTemplatesGroupChanged), compareUUIDs(meta.EntryTemplatesGroup, out.EntryTemplatesGroup)) > 0 {
			out.EntryTemplatesGroup, out.EntryTemplatesGroupChanged = meta.EntryTemplatesGroup, meta.EntryTemplatesGroupChanged
		}
	}
	out.EntryTemplatesGroup = m.groupID(out.EntryTemplatesGroup)
	out.LastSelectedGroup = m.groupID(out.LastSelectedGroup)
	out.LastTopVisibleGroup = m.groupID(out.LastTopVisibleGroup)
	out.RecycleBin, out.RecycleBinChanged = m.bins.result, m.bins.changed
	out.CustomIcons = joinCustomIcons(metas)
	out.CustomData = joinCustomData(metas)
	out.HistoryMaxItems, out.HistoryMaxSize = m.historyLimits(metas)
	m.out.Meta = out
}

// plainSettings leaves only the settings that have no rule of their own.
func plainSettings(meta *snapshot.Meta) snapshot.Meta {
	return snapshot.Meta{
		Generator:              meta.Generator,
		SettingsChanged:        meta.SettingsChanged,
		MaintenanceHistoryDays: meta.MaintenanceHistoryDays,
		Color:                  meta.Color,
		MemoryProtection:       meta.MemoryProtection,
		RecycleBinEnabled:      meta.RecycleBinEnabled,
		LastSelectedGroup:      meta.LastSelectedGroup,
		LastTopVisibleGroup:    meta.LastTopVisibleGroup,
	}
}

func compareSettings(a, b *snapshot.Meta) int {
	return cmp.Or(a.SettingsChanged.Compare(b.SettingsChanged), strings.Compare(
		fmt.Sprintf("%v", plainSettings(a)),
		fmt.Sprintf("%v", plainSettings(b)),
	))
}

// newestText takes the other value if it is newer.
func newestText(value *string, changed *time.Time, otherValue string, otherChanged time.Time) {
	if cmp.Or(otherChanged.Compare(*changed), strings.Compare(otherValue, *value)) > 0 {
		*value, *changed = otherValue, otherChanged
	}
}

func joinCustomIcons(metas []*snapshot.Meta) []snapshot.CustomIcon {
	joined := map[snapshot.UUID]snapshot.CustomIcon{}
	for _, meta := range metas {
		for _, icon := range meta.CustomIcons {
			if known, seen := joined[icon.UUID]; !seen || compareIcons(icon, known) > 0 {
				joined[icon.UUID] = icon
			}
		}
	}
	return slices.Collect(func(yield func(snapshot.CustomIcon) bool) {
		for _, id := range slices.SortedFunc(maps.Keys(joined), compareUUIDs) {
			if !yield(joined[id]) {
				return
			}
		}
	})
}

func compareIcons(a, b snapshot.CustomIcon) int {
	return cmp.Or(a.LastModification.Compare(b.LastModification), strings.Compare(a.Data, b.Data), strings.Compare(a.Name, b.Name))
}

func joinCustomData(metas []*snapshot.Meta) []snapshot.CustomDataItem {
	joined := map[string]snapshot.CustomDataItem{}
	for _, meta := range metas {
		for _, item := range meta.CustomData {
			if known, seen := joined[item.Key]; !seen || compareItems(item, known) > 0 {
				joined[item.Key] = item
			}
		}
	}
	return slices.Collect(func(yield func(snapshot.CustomDataItem) bool) {
		for _, key := range slices.Sorted(maps.Keys(joined)) {
			if !yield(joined[key]) {
				return
			}
		}
	})
}

func compareItems(a, b snapshot.CustomDataItem) int {
	return cmp.Or(a.LastModification.Compare(b.LastModification), strings.Compare(a.Value, b.Value))
}

// historyLimits gives limits under which KeePassXC does not cut the merged history at the next
// edit. That edit puts the main version in the history too, so a limit must hold all versions
// of the longest and of the largest entry. A copy with no limit, which KeePassXC writes as a
// negative number, keeps the result free of it, and a limit never goes down.
func (m *merger) historyLimits(metas []*snapshot.Meta) (items, size int64) {
	known := m.knownAttachments()
	for _, e := range m.entries {
		var total int64
		for _, v := range e.versions {
			total += historySize(&v.data, known)
		}
		items, size = max(items, int64(len(e.versions))), max(size, total)
	}
	for _, meta := range metas {
		items = raisedLimit(items, meta.HistoryMaxItems)
		size = raisedLimit(size, meta.HistoryMaxSize)
	}
	return items, size
}

func raisedLimit(limit, other int64) int64 {
	if limit < 0 || other < 0 {
		return -1
	}
	return max(limit, other)
}

// serviceDataRoom is the room for the service custom data KeePassXC adds to a version. The
// data itself does not count, so the limit is the same whichever copy gave the version.
const serviceDataRoom = 256

// historySize gives at least what KeePassXC counts for a version under its limit of the
// history size. It depends only on the content of the version, the way a fingerprint does.
func historySize(v *snapshot.EntryVersion, known map[snapshot.AttachmentID]snapshot.Attachment) int64 {
	size := serviceDataRoom
	for i := range v.Fields {
		size += len(v.Fields[i].Key) + len(v.Fields[i].Value)
	}
	for i := range v.Attachments {
		size += len(v.Attachments[i].Name) + len(known[v.Attachments[i].ID].Content)
	}
	for i := range v.AutoType.Associations {
		size += len(v.AutoType.Associations[i].Window) + len(v.AutoType.Associations[i].KeystrokeSequence)
	}
	for _, item := range contentCustomData(v.CustomData) {
		size += len(item.Key) + len(item.Value)
	}
	for _, tag := range splitTags(v.Tags) {
		size += len(tag)
	}
	return int64(size)
}

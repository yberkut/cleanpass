package snapshot

import "time"

// Stats is the scale of a copy.
type Stats struct {
	// Entries counts entries in all groups, also in the recycle bin. History
	// versions do not count.
	Entries int
	// Groups counts all groups, also the root group and the recycle bin.
	Groups int
	// Attachments counts the attachments of the main versions of entries. The same
	// bytes on two entries count twice, as a person sees them.
	Attachments int
	// Deletions counts the records about deleted objects.
	Deletions int
}

// Stats counts what the copy holds.
func (s *Snapshot) Stats() Stats {
	stats := Stats{Deletions: len(s.Deletions)}
	s.Root.Walk(func(g *Group) {
		stats.Groups++
		stats.Entries += len(g.Entries)
		for i := range g.Entries {
			stats.Attachments += len(g.Entries[i].Attachments)
		}
	})
	return stats
}

// LastChange returns the newest time at which something in the database changed,
// or the zero time if the database has no times at all.
//
// LocationChanged counts because moving an entry to the recycle bin changes only
// that time. LastAccess and UsageCount do not count: reading is not a change.
// Expiry does not count: it is a date in the future, set by a person. Service custom
// data does not count either: KeePassXC renews it on every save, also without a change.
func (s *Snapshot) LastChange() time.Time {
	var newest time.Time
	see := func(times ...time.Time) {
		for _, t := range times {
			if t.After(newest) {
				newest = t
			}
		}
	}
	seeData := func(items []CustomDataItem) {
		for i := range items {
			if !items[i].IsService() {
				see(items[i].LastModification)
			}
		}
	}
	seeVersion := func(v *EntryVersion) {
		see(v.Times.Creation, v.Times.LastModification, v.Times.LocationChanged)
		seeData(v.CustomData)
	}

	m := &s.Meta
	see(m.SettingsChanged, m.DatabaseNameChanged, m.DatabaseDescriptionChanged, m.DefaultUserNameChanged,
		m.MasterKeyChanged, m.RecycleBinChanged, m.EntryTemplatesGroupChanged)
	seeData(m.CustomData)
	for i := range m.CustomIcons {
		see(m.CustomIcons[i].LastModification)
	}
	for i := range s.Deletions {
		see(s.Deletions[i].Time)
	}
	s.Root.Walk(func(g *Group) {
		see(g.Times.Creation, g.Times.LastModification, g.Times.LocationChanged)
		seeData(g.CustomData)
		for i := range g.Entries {
			seeVersion(&g.Entries[i].EntryVersion)
			for j := range g.Entries[i].History {
				seeVersion(&g.Entries[i].History[j])
			}
		}
	})
	return newest
}

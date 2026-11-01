package merge

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

func init() { addRule(phaseMetadata, mergeMetadata) }

// mergeMetadata takes all settings from the copy with the newest SettingsChanged. The
// printed form breaks a tie, so the choice does not depend on the order of the copies. The
// recycle bin has its own rule, see recycleBins, so it has no say in the choice.
func mergeMetadata(m *merger) {
	best := &m.in.Copies[0].Snapshot.Meta
	for i := range m.in.Copies {
		meta := &m.in.Copies[i].Snapshot.Meta
		if compareMeta(meta, best) > 0 {
			best = meta
		}
	}
	m.out.Meta = *best
	m.out.Meta.RecycleBin, m.out.Meta.RecycleBinChanged = m.bins.result, m.bins.changed
}

func compareMeta(a, b *snapshot.Meta) int {
	return cmp.Or(a.SettingsChanged.Compare(b.SettingsChanged), strings.Compare(settingsText(*a), settingsText(*b)))
}

func settingsText(meta snapshot.Meta) string {
	meta.RecycleBin, meta.RecycleBinChanged = snapshot.UUID{}, time.Time{}
	return fmt.Sprintf("%v", meta)
}

package merge

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

func init() { addRule(phaseMetadata, mergeMetadata) }

// mergeMetadata takes all settings from the copy with the newest SettingsChanged. The
// printed form breaks a tie, so the choice does not depend on the order of the copies.
func mergeMetadata(m *merger) {
	best := &m.in.Copies[0].Snapshot.Meta
	for i := range m.in.Copies {
		meta := &m.in.Copies[i].Snapshot.Meta
		if compareMeta(meta, best) > 0 {
			best = meta
		}
	}
	m.out.Meta = *best
}

func compareMeta(a, b *snapshot.Meta) int {
	return cmp.Or(a.SettingsChanged.Compare(b.SettingsChanged), strings.Compare(fmt.Sprintf("%v", *a), fmt.Sprintf("%v", *b)))
}

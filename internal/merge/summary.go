package merge

import "github.com/yberkut/cleanpass/internal/snapshot"

// Summary counts what a merge did, for the last words to the person.
type Summary struct {
	// Added counts the entries that only part of the copies hold.
	Added int
	// Updated counts the entries whose main versions differ between the copies.
	Updated int
	// KeptInHistory counts the main versions of the copies that the result holds in its history.
	KeptInHistory int
	// DeletedByDecision counts the entries an answer has deleted, now or in an earlier merge.
	DeletedByDecision int
	// Absorbed counts the entries an answer of this run has merged into others.
	Absorbed int
}

// Summary counts over the same Input that gave this Result.
func (r *Result) Summary(in Input) Summary {
	var s Summary
	for _, event := range r.Report {
		switch e := event.(type) {
		case EntryChange:
			if e.Outcome == OutcomeNew {
				s.Added++
			} else {
				s.Updated++
			}
		case DeletionChange:
			if e.Outcome == DeletionApplied {
				s.DeletedByDecision++
			}
		case PairMerged:
			s.Absorbed += len(e.Absorbed)
		}
	}
	s.KeptInHistory = r.keptInHistory(in)
	return s
}

// keptInHistory looks at the result itself: each version counts once, however many copies held it,
// and only if the result really holds it in the history of the entry it now belongs to.
func (r *Result) keptInHistory(in Input) int {
	entries := map[snapshot.UUID]*snapshot.Entry{}
	r.Merged.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			entries[g.Entries[i].UUID] = &g.Entries[i]
		}
	})
	into := make(map[snapshot.UUID]snapshot.UUID, len(r.Absorbed))
	for _, a := range r.Absorbed {
		into[a.Absorbed] = a.Into
	}
	type place struct {
		entry   snapshot.UUID
		version versionKey
	}
	counted := map[place]bool{}
	for i := range in.Copies {
		in.Copies[i].Snapshot.Root.Walk(func(g *snapshot.Group) {
			for j := range g.Entries {
				id := g.Entries[j].UUID
				if head, absorbed := into[id]; absorbed {
					id = head
				}
				entry := entries[id]
				if entry == nil {
					continue
				}
				main := in.Key.versionKey(&g.Entries[j].EntryVersion)
				if counted[place{id, main}] || main == in.Key.versionKey(&entry.EntryVersion) {
					continue
				}
				for k := range entry.History {
					if in.Key.versionKey(&entry.History[k]) == main {
						counted[place{id, main}] = true
						break
					}
				}
			}
		})
	}
	return len(counted)
}

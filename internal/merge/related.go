package merge

import (
	"slices"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// RelatedGroups sorts the copies into groups that look like one database each. A result with
// more than one group means the copies look like different databases: compare warns about that
// and merge stops on it. It depends on the copies alone, and each group holds the positions of
// its copies in the order of the input, sorted by the first position.
//
// Two copies are related if they share the UUID of the root group or of an entry, or if at least
// half of the entries of one copy have a similar entry in the other. Similar means what it means
// for a question about a pair, so no secret value takes part. A copy related to a group joins
// it, so a merged copy links the two copies it came from.
func RelatedGroups(copies []*snapshot.Snapshot) [][]int {
	traits := make([]copyTraits, len(copies))
	groupOf := make([]int, len(copies))
	for i, c := range copies {
		traits[i] = traitsOf(c)
		groupOf[i] = i
	}
	for i := range traits {
		for j := i + 1; j < len(traits); j++ {
			if traits[i].relatedTo(&traits[j]) {
				joinGroups(groupOf, groupOf[i], groupOf[j])
			}
		}
	}
	var groups [][]int
	byGroup := map[int]int{}
	for i, group := range groupOf {
		at, known := byGroup[group]
		if !known {
			at = len(groups)
			byGroup[group] = at
			groups = append(groups, nil)
		}
		groups[at] = append(groups[at], i)
	}
	return groups
}

// joinGroups moves all copies of one group to the other one.
func joinGroups(groupOf []int, a, b int) {
	for i, group := range groupOf {
		if group == b {
			groupOf[i] = a
		}
	}
}

// copyTraits is what the rule looks at in one copy.
type copyTraits struct {
	root    snapshot.UUID
	entries map[snapshot.UUID]bool
	// similarities holds, for each entry, what it can have in common with another entry.
	similarities [][]similarity
	known        map[similarity]bool
}

func traitsOf(c *snapshot.Snapshot) copyTraits {
	traits := copyTraits{root: c.Root.UUID, entries: map[snapshot.UUID]bool{}, known: map[similarity]bool{}}
	c.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			e := &g.Entries[i]
			traits.entries[e.UUID] = true
			found := similaritiesOf(labelOf(&e.EntryVersion))
			traits.similarities = append(traits.similarities, found)
			for _, s := range found {
				traits.known[s] = true
			}
		}
	})
	return traits
}

func (a *copyTraits) relatedTo(b *copyTraits) bool {
	if !a.root.IsZero() && a.root == b.root {
		return true
	}
	for id := range a.entries {
		if !id.IsZero() && b.entries[id] {
			return true
		}
	}
	return a.halfSimilarTo(b) || b.halfSimilarTo(a)
}

// halfSimilarTo says at least half of the entries of this copy have a similar entry in the other.
func (a *copyTraits) halfSimilarTo(b *copyTraits) bool {
	similar := 0
	for _, found := range a.similarities {
		if slices.ContainsFunc(found, func(s similarity) bool { return b.known[s] }) {
			similar++
		}
	}
	return similar > 0 && 2*similar >= len(a.similarities)
}

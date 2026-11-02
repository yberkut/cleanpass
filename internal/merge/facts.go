package merge

import "github.com/yberkut/cleanpass/internal/snapshot"

// Facts is what the Report takes from a merge. A Result holds the merged copy with its secrets,
// and its events and questions are open types that could hold anything. Facts names each kind by
// its type and holds no open type and no part of a copy, so a secret value has no way into it. A
// test in the Report reads these types to prove that.
type Facts struct {
	// Entries holds the entries the copies do not agree on.
	Entries []EntryChange
	// Conflicts holds the questions about the versions of an entry.
	Conflicts map[snapshot.UUID]Conflict
	// Deletions holds the questions about deletions with what became of them.
	Deletions []DeletionChange
	// Pairs holds the questions about similar entries.
	Pairs []SimilarPair
	// IdenticalPairs counts the pairs one decision can merge.
	IdenticalPairs int
	// RememberedPairs holds the entries an earlier merge made of several, where a copy still holds
	// one of them under its old UUID.
	RememberedPairs []PairRemembered
	// GroupsJoined holds the groups that took other groups in.
	GroupsJoined []GroupsJoined
	// GroupCycles holds the groups that left their parent to break a cycle.
	GroupCycles []GroupCycleBroken
	// Unknown counts the events and questions of a kind Facts does not name yet. The Report says
	// so, because a kind it cannot show must not disappear without a word.
	Unknown int
}

// Facts sorts the events and the questions by their kinds. It keeps the order of each kind.
func (r *Result) Facts() Facts {
	facts := Facts{Conflicts: map[snapshot.UUID]Conflict{}}
	for _, event := range r.Report {
		switch e := event.(type) {
		case EntryChange:
			facts.Entries = append(facts.Entries, e)
		case DeletionChange:
			facts.Deletions = append(facts.Deletions, e)
		case PairRemembered:
			facts.RememberedPairs = append(facts.RememberedPairs, e)
		case GroupsJoined:
			facts.GroupsJoined = append(facts.GroupsJoined, e)
		case GroupCycleBroken:
			facts.GroupCycles = append(facts.GroupCycles, e)
		default:
			facts.Unknown++
		}
	}
	for i := range r.Questions {
		switch d := r.Questions[i].Detail.(type) {
		case Conflict:
			facts.Conflicts[d.Entry] = d
		case SimilarPair:
			facts.Pairs = append(facts.Pairs, d)
		case IdenticalPairs:
			facts.IdenticalPairs = len(d.Pairs)
		case Deletion:
			// The event about the same deletion holds all of this.
		default:
			facts.Unknown++
		}
	}
	return facts
}

// Host gives the host of the URL of the entry, or nothing if the value is no URL. The rest of a
// URL can hold a password or a token, so this is the part to show to a person.
func (l Label) Host() string { return hostOf(l.URL) }

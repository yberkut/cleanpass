package merge

import (
	"bytes"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// Outcome tells what happened to an entry.
type Outcome int

// The outcomes of an entry.
const (
	outcomeSame Outcome = iota
	// OutcomeNew means the copies that hold the entry agree, and another copy lacks it.
	OutcomeNew
	// OutcomeUpdated means the copies hold different main versions and their histories
	// order them: a copy lags, and there is no question.
	OutcomeUpdated
	// OutcomeConflict means a true conflict with a gap of at least 24 hours: the newer
	// version became main, and there is no question.
	OutcomeConflict
	// OutcomeAsked means a true conflict with a question. The newer version is main,
	// which is also the rule without an answer.
	OutcomeAsked
	// OutcomeChosen means a true conflict in which the answer of the human made a new
	// version the main one.
	OutcomeChosen
)

// EntryChange is the event about an entry the copies do not agree on.
type EntryChange struct {
	Entry   snapshot.UUID
	Label   Label
	Outcome Outcome
	// Replaced says the main version of the result is one a copy has in its history under
	// a different main version: the clock has put a replaced version back on top.
	Replaced bool
	// Copies holds one item for each input copy, sorted by name.
	Copies []CopyState
}

func (c EntryChange) sortKey() string { return "entry:" + c.Entry.String() }

// CopyState is the state of an entry in one copy.
type CopyState struct {
	Name string
	// Holds says the copy has the entry. If not, the other values are empty.
	Holds bool
	// Modified is the time of the main version in this copy.
	Modified time.Time
	// Behind says another copy holds this main version in its history.
	Behind bool
	// Differences tells how this main version differs from the main version of the result.
	Differences Difference
}

// Label is what tells a person which entry this is. A protected value stays empty.
type Label struct {
	Title, UserName, URL string
}

func labelOf(v *snapshot.EntryVersion) Label {
	var label Label
	for i := range v.Fields {
		f := &v.Fields[i]
		if f.Protected {
			continue
		}
		switch f.Key {
		case "Title":
			label.Title = f.Value
		case "UserName":
			label.UserName = f.Value
		case "URL":
			label.URL = f.Value
		}
	}
	return label
}

// Difference names what differs between two versions. It holds names, never values, so it
// tells "the password differs" without the password.
type Difference struct {
	// Fields holds the names of the string fields that differ, sorted.
	Fields []string
	// Attributes holds the other parts that differ.
	Attributes []Attribute
}

// differences compares what the fingerprint covers, so two versions with different
// fingerprints always have a difference to show.
func differences(a, b *snapshot.EntryVersion) Difference {
	var result Difference
	fieldsOfA := map[string]snapshot.Field{}
	for _, f := range a.Fields {
		fieldsOfA[f.Key] = f
	}
	for _, f := range b.Fields {
		if other, ok := fieldsOfA[f.Key]; !ok || other != f {
			result.Fields = append(result.Fields, f.Key)
		}
		delete(fieldsOfA, f.Key)
	}
	for name := range fieldsOfA {
		result.Fields = append(result.Fields, name)
	}
	slices.Sort(result.Fields)

	for _, part := range contentParts {
		var first, second encoding
		part.encode(&first, a)
		part.encode(&second, b)
		if !bytes.Equal(first, second) {
			result.Attributes = append(result.Attributes, part.attribute)
		}
	}
	return result
}

// outcomeWithoutConflict gives the outcome when no main version disputes another.
func (e *entryState) outcomeWithoutConflict(copies int) Outcome {
	main := e.main().key
	for i := range e.holdings {
		if e.holdings[i].main != main {
			return OutcomeUpdated
		}
	}
	if len(e.copiesWithMain(main)) < copies {
		return OutcomeNew
	}
	return outcomeSame
}

// change makes the event about the entry, from the state after all its decisions.
func (e *entryState) change(outcome Outcome, copies []Copy) EntryChange {
	main := e.main()
	change := EntryChange{Entry: e.uuid, Label: labelOf(&main.data), Outcome: outcome}
	for i := range e.holdings {
		change.Replaced = change.Replaced || e.holdings[i].history[main.key]
	}
	for i := range copies {
		state := CopyState{Name: copies[i].Name}
		for j := range e.holdings {
			if h := &e.holdings[j]; h.copy == state.Name {
				state.Holds = true
				state.Modified = h.main.time()
				state.Behind = e.isBehind(h)
				state.Differences = differences(&main.data, &h.entry.EntryVersion)
			}
		}
		change.Copies = append(change.Copies, state)
	}
	slices.SortFunc(change.Copies, func(a, b CopyState) int { return strings.Compare(a.Name, b.Name) })
	return change
}

func (e *entryState) isBehind(h *holding) bool {
	for i := range e.holdings {
		if e.holdings[i].history[h.main] {
			return true
		}
	}
	return false
}

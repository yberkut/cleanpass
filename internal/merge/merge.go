// Package merge is the Merge Engine: a pure function from N copies of a database, the
// decisions of the human and the current time to one merged copy, the questions for the
// human and the data for the report.
//
// It touches no files, no clock and no input or output: Run depends on its Input alone.
// Only NewKey reads the system, and the caller calls it once for each run of the tool.
//
// # Adding a rule
//
// A rule lives in its own file and registers itself for its phase in init, so a new rule
// does not change this file:
//
//	func init() { addRule(phaseDeletions, mergeDeletions) }
//
// A rule works on the merger: it reads the copies, changes the entries, asks with ask and
// tells the report with tell. The kinds of question and of report event are free types
// in the file of the rule, see Question.Detail and Event.
package merge

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// Copy is one input of a merge.
type Copy struct {
	// Name tells the copies apart in the questions and the report, for example a path.
	// It has no effect on the merged data.
	Name     string
	Snapshot *snapshot.Snapshot
}

// Input is everything a merge depends on.
type Input struct {
	Copies []Copy
	// Decisions are the answers of the human to the Questions of an earlier Run on the
	// same copies with the same Key.
	Decisions []Decision
	// Now is the current time. A version stamped after it comes from the future.
	Now time.Time
	// Key makes the content fingerprints. Use one NewKey for all Runs of a process.
	Key Key
}

// Result is what a merge gives.
type Result struct {
	// Merged shares its slices and pointers with the input copies, so treat it as read
	// only. Its Format is empty: the writer takes it from the copy that gives the keys.
	Merged *snapshot.Snapshot
	// Questions holds every question the copies raise, answered or not, sorted by ID.
	Questions []Question
	// Report holds the events for the report, without any secret value, in a fixed order.
	Report []Event
	// Absorbed lists the entries that left the tree because a pair merged, sorted. All their
	// versions are under the entry they went into.
	Absorbed []Absorption
}

// Event is one thing the report can tell. Each rule defines its own event types.
type Event interface {
	// sortKey puts the events in an order that does not depend on the order of the copies.
	sortKey() string
}

// The errors of Run. They mean a bug in the caller, not a problem with a copy.
var (
	ErrNoCopies = errors.New("merge: no copies")
	ErrNoKey    = errors.New("merge: no fingerprint key")
	ErrNoTime   = errors.New("merge: no current time")
)

// phase is a step of a merge. A phase runs after all phases before it.
type phase int

const (
	// phaseMemory reads the decisions stored in the copies by earlier merges, before
	// anything is joined.
	phaseMemory phase = iota
	// phaseVersions joins the versions of each entry and settles the conflicts.
	phaseVersions
	// phasePairs handles similar entries. Each entry has its versions by now.
	phasePairs
	// phaseDeletions handles deletion records and the recycle bin.
	phaseDeletions
	// phaseGroups joins the groups.
	phaseGroups
	// phaseMetadata joins the settings of the database.
	phaseMetadata
	// phaseRemember stores the decisions of this merge in the result.
	phaseRemember

	phaseCount
)

// rule is one step of a merge.
type rule func(m *merger)

var rules [phaseCount][]rule

func addRule(p phase, r rule) { rules[p] = append(rules[p], r) }

// merger is the state of one Run. The rules share it.
type merger struct {
	in Input
	// root is the UUID of the merged root group.
	root snapshot.UUID
	// roots holds the UUIDs of the root groups of all copies.
	roots map[snapshot.UUID]bool
	// entries holds every entry by UUID. A rule removes an entry by removing it here.
	entries map[snapshot.UUID]*entryState
	groups  map[snapshot.UUID]*groupState
	// out is the merged copy so far. A rule fills the parts it owns.
	out snapshot.Snapshot

	answers   map[QuestionID]int
	used      map[QuestionID]bool
	questions []Question
	report    []Event
	absorbed  []Absorption
	failure   error
}

// Run merges the copies.
func Run(in Input) (*Result, error) {
	switch {
	case len(in.Copies) == 0:
		return nil, ErrNoCopies
	case in.Key == Key{}:
		return nil, ErrNoKey
	case in.Now.IsZero():
		return nil, ErrNoTime
	}
	m := newMerger(in)
	for _, phaseRules := range rules {
		for _, r := range phaseRules {
			r(m)
		}
	}
	return m.finish()
}

func newMerger(in Input) *merger {
	m := &merger{
		in:      in,
		roots:   map[snapshot.UUID]bool{},
		entries: map[snapshot.UUID]*entryState{},
		groups:  map[snapshot.UUID]*groupState{},
		answers: map[QuestionID]int{},
		used:    map[QuestionID]bool{},
	}
	for _, d := range in.Decisions {
		m.answers[d.Question] = d.Choice
	}
	for i := range in.Copies {
		root := in.Copies[i].Snapshot.Root.UUID
		m.roots[root] = true
		if i == 0 || compareUUIDs(root, m.root) < 0 {
			m.root = root
		}
	}
	for i := range in.Copies {
		c := &in.Copies[i]
		c.Snapshot.Root.Walk(func(g *snapshot.Group) {
			for j := range g.Entries {
				m.hold(c, m.groupID(g.UUID), &g.Entries[j])
			}
		})
	}
	return m
}

// groupID gives the UUID a group has in the merged tree. All root groups are one group.
func (m *merger) groupID(id snapshot.UUID) snapshot.UUID {
	if m.roots[id] {
		return m.root
	}
	return id
}

// sortedEntries gives the entries in a fixed order. Rules use it instead of ranging over
// the map, because the result must not depend on the order of a map.
func (m *merger) sortedEntries() []*entryState {
	result := make([]*entryState, 0, len(m.entries))
	for _, e := range m.entries {
		result = append(result, e)
	}
	slices.SortFunc(result, func(a, b *entryState) int { return compareUUIDs(a.uuid, b.uuid) })
	return result
}

// tell adds an event to the report.
func (m *merger) tell(event Event) { m.report = append(m.report, event) }

// finish builds the result after all rules have run.
func (m *merger) finish() (*Result, error) {
	if m.failure != nil {
		return nil, m.failure
	}
	if err := m.checkAllDecisionsUsed(); err != nil {
		return nil, err
	}
	m.out.Root = m.buildTree()
	m.out.Attachments = m.collectAttachments()
	slices.SortFunc(m.questions, func(a, b Question) int { return strings.Compare(string(a.ID), string(b.ID)) })
	slices.SortStableFunc(m.report, func(a, b Event) int { return strings.Compare(a.sortKey(), b.sortKey()) })
	merged := m.out
	return &Result{Merged: &merged, Questions: m.questions, Report: m.report, Absorbed: m.absorbed}, nil
}

// collectAttachments gives the bytes of every attachment the merged versions refer to.
// The file marks the bytes as protected if any copy does.
func (m *merger) collectAttachments() map[snapshot.AttachmentID]snapshot.Attachment {
	known := map[snapshot.AttachmentID]snapshot.Attachment{}
	for i := range m.in.Copies {
		for id, a := range m.in.Copies[i].Snapshot.Attachments {
			a.Protected = a.Protected || known[id].Protected
			known[id] = a
		}
	}
	result := map[snapshot.AttachmentID]snapshot.Attachment{}
	for _, e := range m.entries {
		for _, v := range e.versions {
			for _, ref := range v.data.Attachments {
				if a, ok := known[ref.ID]; ok {
					result[ref.ID] = a
				}
			}
		}
	}
	return result
}

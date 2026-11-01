package merge

import (
	"encoding/hex"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

func init() { addRule(phaseDeletions, mergeDeletions) }

// The answers to a question about a deletion.
const (
	// AnswerKeep keeps the entry. It is what happens without an answer.
	AnswerKeep = 0
	// AnswerDelete applies the deletion.
	AnswerDelete = 1
)

// deletionPrefix starts the ID of a question about a deletion. The rest is the UUID of the
// entry, so the ID stays the same in every run on any copies.
const deletionPrefix = "deletion:"

// DeletesEntry names the entry the decision allows to disappear. No other decision allows
// that, so the Loss Audit takes what the human said from here and not from the engine.
func (d Decision) DeletesEntry() (snapshot.UUID, bool) {
	var id snapshot.UUID
	text, isDeletion := strings.CutPrefix(string(d.Question), deletionPrefix)
	if !isDeletion || d.Choice != AnswerDelete || len(text) != hex.EncodedLen(len(id)) {
		return snapshot.UUID{}, false
	}
	if _, err := hex.Decode(id[:], []byte(text)); err != nil {
		return snapshot.UUID{}, false
	}
	return id, true
}

// Deletion is the Detail of a question about an entry that one copy has deleted while it
// still exists. See AnswerKeep and AnswerDelete.
type Deletion struct {
	Entry snapshot.UUID
	Label Label
	// Deleted is the time of the newest deletion.
	Deleted time.Time
	// Modified is the time of the newest version of the entry.
	Modified time.Time
	// Recorded names the copies with a record about the deletion, sorted.
	Recorded []string
	// Recycled names the copies that hold the entry in their recycle bin, sorted.
	Recycled []string
	// Held names the copies that hold the entry outside their recycle bin, sorted.
	Held []string
}

// DeletionOutcome tells what became of a question about a deletion.
type DeletionOutcome int

// The outcomes of a question about a deletion.
const (
	// DeletionOpen means there is no answer, so the entry stays.
	DeletionOpen DeletionOutcome = iota
	// DeletionRejected means the human kept the entry.
	DeletionRejected
	// DeletionApplied means the human deleted the entry.
	DeletionApplied
)

// DeletionChange is the event about a question about a deletion.
type DeletionChange struct {
	Deletion
	Outcome DeletionOutcome
}

func (c DeletionChange) sortKey() string { return deletionPrefix + c.Entry.String() }

// mergeDeletions joins the records about deleted objects and asks about every entry a record
// stands against. Only the answer of the human removes an entry.
func mergeDeletions(m *merger) {
	m.bins = m.readBins()
	records := m.joinRecords()
	asked := map[snapshot.UUID]bool{}
	for _, e := range m.sortedEntries() {
		if m.settleDeletion(e, records) {
			asked[e.uuid] = true
		}
	}
	m.dropRecordsAboutGroups(records)
	for _, id := range slices.SortedFunc(maps.Keys(records), compareUUIDs) {
		m.out.Deletions = append(m.out.Deletions, snapshot.Deletion{UUID: id, Time: records[id].time})
	}
	m.report = slices.DeleteFunc(m.report, func(event Event) bool {
		change, isChange := event.(EntryChange)
		if !isChange || !asked[change.Entry] {
			return false
		}
		_, kept := m.entries[change.Entry]
		return !kept || change.Outcome == OutcomeNew
	})
}

// record is what all copies together say about the deletion of one object.
type record struct {
	time time.Time
	// copies names the copies with a record.
	copies []string
}

// joinRecords gives the newest record for each UUID.
func (m *merger) joinRecords() map[snapshot.UUID]record {
	records := map[snapshot.UUID]record{}
	for i := range m.in.Copies {
		c := &m.in.Copies[i]
		for _, d := range c.Snapshot.Deletions {
			r := records[d.UUID]
			r.time = later(r.time, d.Time)
			r.copies = append(r.copies, c.Name)
			records[d.UUID] = r
		}
	}
	return records
}

// settleDeletion asks about the entry if a record stands against it, or if a copy holds it in
// the recycle bin and another one outside, and applies the answer. It says whether it asked.
//
// A kept entry has no record in the result: it would ask again in the next merge, and
// KeePassXC would apply it in its own merge.
func (m *merger) settleDeletion(e *entryState, records map[snapshot.UUID]record) bool {
	r, isRecorded := records[e.uuid]
	inBin, outside := m.bins.count(e)
	hasBoth := inBin > 0 && outside > 0
	e.recycled = outside == 0
	if !isRecorded && !hasBoth {
		return false
	}
	detail := m.deletion(e, r)
	choice, answered := m.ask(Question{
		ID:      QuestionID(deletionPrefix + e.uuid.String()),
		Choices: 2,
		Detail:  detail,
	})
	outcome := DeletionOpen
	if answered {
		outcome = DeletionRejected
	}
	switch {
	case choice == AnswerDelete && isRecorded:
		outcome = DeletionApplied
		delete(m.entries, e.uuid)
	case choice == AnswerDelete:
		// No copy has removed the entry. The answer goes as far as the deletion went: the
		// entry lies in the bin with all its versions, and the bin is for the human to empty.
		outcome = DeletionApplied
		e.recycled = true
		m.placeBy(e, true)
	default:
		delete(records, e.uuid)
		if hasBoth {
			m.placeBy(e, false)
		}
	}
	m.tell(DeletionChange{Deletion: detail, Outcome: outcome})
	return true
}

// dropRecordsAboutGroups removes the records about groups a copy still holds. No copy can lose
// a group by a deletion: the merge keeps every group, so a record would only stand against it.
func (m *merger) dropRecordsAboutGroups(records map[snapshot.UUID]record) {
	for i := range m.in.Copies {
		m.in.Copies[i].Snapshot.Root.Walk(func(g *snapshot.Group) { delete(records, g.UUID) })
	}
}

// deletion makes the data of the question. It comes from the copies alone, so the question
// stays the same whatever the human answers to another one.
func (m *merger) deletion(e *entryState, r record) Deletion {
	newest := e.newestOriginal()
	d := Deletion{
		Entry:    e.uuid,
		Label:    labelOf(&newest.data),
		Deleted:  r.time,
		Modified: newest.key.time(),
		Recorded: sortedNames(r.copies),
	}
	for i := range e.holdings {
		h := &e.holdings[i]
		if since, inBin := m.bins.recycled(h); inBin {
			d.Deleted = later(d.Deleted, since)
			d.Recycled = append(d.Recycled, h.copy.Name)
		} else {
			d.Held = append(d.Held, h.copy.Name)
		}
	}
	d.Recycled, d.Held = sortedNames(d.Recycled), sortedNames(d.Held)
	return d
}

// newestOriginal gives the newest version the copies hold, which is not always the main
// version: a decision about a conflict can add a new one.
func (e *entryState) newestOriginal() *version {
	var keys []versionKey
	for i := range e.holdings {
		keys = append(keys, e.holdings[i].main)
		keys = slices.AppendSeq(keys, maps.Keys(e.holdings[i].history))
	}
	return e.find(slices.MaxFunc(keys, compareKeys))
}

func sortedNames(names []string) []string {
	slices.Sort(names)
	return slices.Compact(names)
}

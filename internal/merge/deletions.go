package merge

import (
	"encoding/hex"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

func init() {
	addRule(phaseMemory, func(m *merger) { m.records = m.joinRecords() })
	addRule(phaseDeletions, mergeDeletions)
}

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
	// Modified is the time the newest content of the entry appeared.
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
	// Remembered says an answer of an earlier merge decided, so nobody was asked this time.
	Remembered bool
}

func (c DeletionChange) sortKey() string { return deletionPrefix + c.Entry.String() }

// mergeDeletions joins the records about deleted objects and asks about every entry a record
// stands against. Only the answer of the human removes an entry.
//
// Every record stays in the result, also next to an entry the human keeps. So the next merge
// asks again until the human answers, and an old copy cannot bring a deleted entry back as a
// new one. An answer goes to the memory, so an old copy does not raise the same question again.
// A group raises no question and never goes away: the merge keeps every group.
func mergeDeletions(m *merger) {
	for _, id := range slices.SortedFunc(maps.Keys(m.records), compareUUIDs) {
		m.out.Deletions = append(m.out.Deletions, snapshot.Deletion{UUID: id, Time: m.records[id].time})
	}
	asked := map[snapshot.UUID]bool{}
	for _, e := range m.sortedEntries() {
		if m.settleDeletion(e) {
			asked[e.uuid] = true
		}
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
func (m *merger) settleDeletion(e *entryState) bool {
	r, isRecorded := m.records[e.uuid]
	inBin, outside := m.bins.count(e)
	hasBoth := inBin > 0 && outside > 0
	e.recycled = outside == 0
	if !isRecorded && !hasBoth {
		return false
	}
	detail := m.deletion(e, r)
	choice, answered := m.memory.deletionAnswer(e, detail)
	remembered := answered
	if !remembered {
		id := QuestionID(deletionPrefix + e.uuid.String())
		m.noteAsked(id, e.ruleMain)
		choice, answered = m.ask(Question{
			ID:      id,
			Choices: 2,
			Detail:  detail,
		})
		if answered {
			m.rememberDeletion(e, detail, choice)
		}
	}
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
	case hasBoth:
		m.placeBy(e, false)
	}
	m.tell(DeletionChange{Deletion: detail, Outcome: outcome, Remembered: remembered})
	return true
}

// goesByMemory says an earlier answer removes the entry in this merge, so there is no sense in
// asking anything about its versions.
func (m *merger) goesByMemory(e *entryState) bool {
	r, isRecorded := m.records[e.uuid]
	if !isRecorded {
		return false
	}
	answer, remembered := m.memory.deletionAnswer(e, m.deletion(e, r))
	return remembered && answer == AnswerDelete
}

// rememberDeletion stores an answer. A rejection holds for the deletions the human knew about. An
// acceptance holds for the versions the human knew about: an old copy may hold any of them, so
// it names the time of each one.
func (m *merger) rememberDeletion(e *entryState, d Deletion, choice int) {
	if choice == AnswerDelete {
		for _, v := range e.versions {
			m.remember(memoryAccepted, e.uuid.String(), memoryDate(v.key.time()))
		}
		return
	}
	through := d.Deleted
	if e.recycled {
		through = later(through, m.timeInTheBin(e))
	}
	m.remember(memoryRejected, e.uuid.String(), memoryDate(through))
}

// timeInTheBin gives the time of the move to the bin that the result itself will show for an entry
// that stays there. It can be newer than the time of any copy that holds the entry, because a copy
// without the entry can have moved a group around it.
func (m *merger) timeInTheBin(e *entryState) time.Time {
	since := e.result().Times.LocationChanged
	for id := e.group; id != m.bins.result && id != m.root; id = m.groups[id].parent {
		if m.groups[id] == nil {
			break
		}
		since = later(since, m.groups[id].data.Times.LocationChanged)
	}
	return since
}

// deletion makes the data of the question. It comes from the copies alone, so the question
// stays the same whatever the human answers to another one.
func (m *merger) deletion(e *entryState, r record) Deletion {
	d := Deletion{
		Entry:    e.uuid,
		Label:    labelOf(&e.ruleMain.data),
		Deleted:  r.time,
		Modified: lastChange(e.originalKeys(), e.ruleMain.key),
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

// originalKeys gives the versions the copies hold, without one a decision about a conflict has
// added.
func (e *entryState) originalKeys() []versionKey {
	var keys []versionKey
	for i := range e.holdings {
		keys = append(keys, e.holdings[i].main)
		keys = slices.AppendSeq(keys, maps.Keys(e.holdings[i].history))
	}
	return keys
}

// lastChange gives the time the newest content appeared: the oldest version with that content
// after the newest version with another one. keepassxc-cli gives an entry a new time when it
// moves it to the bin, with the same content, and that is no change the human should weigh
// against the deletion. Two contents with the same time say nothing about their order, so then
// that time counts, whichever of them the key of the run puts on top.
func lastChange(keys []versionKey, newest versionKey) time.Time {
	var before time.Time
	for _, k := range keys {
		if k.content != newest.content {
			before = later(before, k.time())
		}
	}
	first := newest.time()
	for _, k := range keys {
		if k.content == newest.content && k.time().After(before) && k.time().Before(first) {
			first = k.time()
		}
	}
	return first
}

func sortedNames(names []string) []string {
	slices.Sort(names)
	return slices.Compact(names)
}

package merge

import (
	"slices"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// closeInTime is the gap below which the clocks of two systems cannot order two versions.
const closeInTime = 24 * time.Hour

// Conflict is the Detail of a question about the order of versions, when the histories of
// the copies and the clock do not settle it together.
type Conflict struct {
	Entry snapshot.UUID
	// Close says two versions in conflict are less than 24 hours apart.
	Close bool
	// Future says a version in conflict has a time after the current time.
	Future bool
	// Replaced says a copy holds the newest version by the clock in its history, under
	// another main version. The clock and that history disagree about the order.
	Replaced bool
	// Versions are the answers: first the main version by the rule of the maximum, which is
	// what happens without an answer, then the others, newest first.
	Versions []VersionView
}

// VersionView is what the human sees of one version. It holds no secret value.
type VersionView struct {
	// Modified is the time of the version. Together with Conflict.Entry it finds the
	// version in Result.Merged when the human asks to see one secret.
	Modified time.Time
	// Copies names the copies that have this as their main version, sorted.
	Copies []string
	Label  Label
	// Differences tells how this version differs from the first answer.
	Differences Difference
}

// settle sorts out what the main versions of the copies say about each other. It asks the
// human about a conflict the clock cannot decide, and about a main version that a copy has
// replaced. One entry has one question at most, because two answers could contradict.
func (m *merger) settle(e *entryState) {
	disputed, near := e.disputed()
	future := false
	for _, k := range disputed {
		future = future || k.time().After(m.in.Now)
	}
	replacing := e.replacingMain()
	outcome := e.outcomeWithoutConflict(len(m.in.Copies))
	settled := m.memory.orderIsSettled(e.uuid, e.main().key.time(), replacing)
	switch {
	case near || future || (len(replacing) > 0 && !settled):
		outcome = OutcomeAsked
		detail := Conflict{Entry: e.uuid, Close: near, Future: future, Replaced: len(replacing) > 0}
		if m.askAboutVersions(e, detail, slices.Concat(disputed, replacing)) {
			outcome = OutcomeChosen
		}
	case len(disputed) > 0:
		outcome = OutcomeConflict
	}
	if outcome != outcomeSame {
		m.tell(e.change(outcome, m.in.Copies))
	}
}

// disputed gives the main versions that are in conflict: with different content, and no copy
// with one of them holds the other in its history. It also says whether two of them are close.
//
// The same content with another time is no conflict, because there is nothing to choose:
// keepassxc-cli gives an entry a new time when it moves it to the bin, with no new content and
// no item in the history. Both stay as versions, and the newest by the clock is main.
func (e *entryState) disputed() (keys []versionKey, near bool) {
	mains := e.mains()
	inConflict := make([]bool, len(mains))
	for i, older := range mains {
		for j := i + 1; j < len(mains); j++ {
			newer := mains[j]
			if older.content == newer.content || !e.apart(older, newer) || e.replaced(older, newer) || e.replaced(newer, older) {
				continue
			}
			inConflict[i], inConflict[j] = true, true
			near = near || newer.time().Sub(older.time()) < closeInTime
		}
	}
	for i, k := range mains {
		if inConflict[i] {
			keys = append(keys, k)
		}
	}
	return keys, near
}

// mains gives the versions of the set that stand for the main versions of the copies, each
// once, the oldest first.
func (e *entryState) mains() []versionKey {
	var keys []versionKey
	for i := range e.holdings {
		keys = append(keys, e.holdings[i].stands...)
	}
	slices.SortFunc(keys, compareKeys)
	return slices.Compact(keys)
}

// apart says that a copy stands for each version without the other. A main version that went
// into both versions says nothing against either of them, so alone it makes no conflict.
func (e *entryState) apart(a, b versionKey) bool {
	standsWithout := func(with, without versionKey) bool {
		return slices.ContainsFunc(e.holdings, func(h holding) bool {
			return slices.Contains(h.stands, with) && !slices.Contains(h.stands, without)
		})
	}
	return standsWithout(a, b) && standsWithout(b, a)
}

// replaced says a copy with main version by holds old in its history. Both are versions of the
// set, so a history version that lacks the custom data of old still counts as old: see absorbs.
func (e *entryState) replaced(old, by versionKey) bool {
	for i := range e.holdings {
		if h := &e.holdings[i]; slices.Contains(h.stands, by) && h.history[old] {
			return true
		}
	}
	return false
}

// replacingMain gives the main versions of the copies that hold the main version of the entry
// in their history. A history leaves out the main version of its own copy, so each of these
// differs from it. A main version with the same content as the main version of the entry does
// not count: the clock has moved a time there and no content. Normally there are none: the
// newest version is no copy's old one.
func (e *entryState) replacingMain() []versionKey {
	main := e.main().key
	var keys []versionKey
	for i := range e.holdings {
		h := &e.holdings[i]
		if !h.history[main] {
			continue
		}
		for _, k := range h.stands {
			if k.content != main.content {
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// askAboutVersions fills the answers into the detail, asks the human and applies the answer.
// It says whether the answer made a new version.
func (m *merger) askAboutVersions(e *entryState, detail Conflict, asked []versionKey) bool {
	answers := e.answers(asked)
	first := &answers[0].data
	detail.Versions = make([]VersionView, len(answers))
	for i, v := range answers {
		detail.Versions[i] = VersionView{
			Modified:    v.key.time(),
			Copies:      e.copiesWithMain(v.key),
			Label:       labelOf(&v.data),
			Differences: differences(first, &v.data),
		}
	}
	choice, answered := m.ask(Question{
		ID:      QuestionID("conflict:" + e.uuid.String()),
		Choices: len(answers),
		Detail:  detail,
	})
	if !answered {
		return false
	}
	main, replacing := e.main().key.time(), e.replacingMain()
	if e.choose(answers[choice], m.in.Key, m.in.Now) {
		return true
	}
	if len(replacing) > 0 {
		// No new version closes the matter, so the same copy would raise the same question.
		newest := slices.MaxFunc(replacing, compareKeys).time()
		m.remember(memoryReplaced, e.uuid.String(), memoryDate(main), memoryDate(newest))
	}
	return false
}

// answers lists the possible answers. The first one is what happens without an answer, so it
// is always the main version, even if the question is not about it. The others follow,
// newest first, each once.
func (e *entryState) answers(asked []versionKey) []*version {
	slices.SortFunc(asked, compareKeys)
	result := []*version{e.main()}
	for _, key := range slices.Backward(slices.Compact(asked)) {
		if key != e.main().key {
			result = append(result, e.find(key))
		}
	}
	return result
}

// find gives a version that is in the set.
func (e *entryState) find(key versionKey) *version {
	i, _ := e.search(key)
	return e.versions[i]
}

// copiesWithMain names the copies whose main version is this version, or went into it.
func (e *entryState) copiesWithMain(key versionKey) []string {
	var names []string
	for i := range e.holdings {
		if slices.Contains(e.holdings[i].stands, key) {
			names = append(names, e.holdings[i].copy.Name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// choose makes the chosen version the main one by adding a new version with its content
// and the current time, so that the next merge does not undo the decision. All original
// versions stay. It says whether it had to add a version: the newest version needs none,
// unless another version has the same time, because then the key of the run decides.
func (e *entryState) choose(chosen *version, key Key, now time.Time) bool {
	newest := e.main()
	last := len(e.versions) - 1
	alone := last == 0 || e.versions[last-1].key.time().Before(newest.key.time())
	if chosen == newest && alone {
		return false
	}
	data := chosen.data
	data.Times.LastModification = stampAfter(newest.key.time(), now)
	e.put(key.versionKey(&data), data)
	return true
}

// stampAfter gives the current time in whole seconds, because the file stores no less. If
// a stamp from the future is newer than that, it gives the next second after that stamp.
func stampAfter(newest, now time.Time) time.Time {
	stamp := now.UTC().Truncate(time.Second)
	if stamp.After(newest) {
		return stamp
	}
	return newest.Truncate(time.Second).Add(time.Second)
}

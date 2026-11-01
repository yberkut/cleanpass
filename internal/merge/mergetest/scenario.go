// Package mergetest generates scenarios for the property tests of the Merge Engine and of
// everything that checks its result. Only tests import it.
//
// A scenario starts from one database, forks it into N copies and applies random moves to
// the copies, each with its own wrong clock.
//
// # Adding a move
//
// A move lives in its own file and registers itself in init, so a new move does not change
// this file:
//
//	func init() { register("delete entry", deleteEntry) }
//
// Every property test draws from Scenarios, so a new move reaches all of them at once.
package mergetest

import (
	"fmt"
	"maps"
	"time"
	"unicode"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// SecretMarker starts every generated secret value. A test looks for it in any text that
// must hold no secrets.
const SecretMarker = "SECRET-MARKER-"

// Scenario is a generated set of copies of one database.
type Scenario struct {
	Copies []merge.Copy
	Now    time.Time
}

// world is a scenario while it grows.
type world struct {
	forks  []*fork
	lastID uint32
	// bin is the UUID of the recycle bin, once a copy has one.
	bin snapshot.UUID
}

// fork is one copy while it grows.
type fork struct {
	name     string
	snapshot *snapshot.Snapshot
	// clock is the local clock of the system that edits this copy.
	clock time.Time
}

// move is one thing that can happen to a copy.
type move struct {
	name  string
	apply func(t *rapid.T, w *world, f *fork)
}

var moves []move

func register(name string, apply func(t *rapid.T, w *world, f *fork)) {
	moves = append(moves, move{name: name, apply: apply})
}

var start = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// Scenarios generates scenarios.
func Scenarios() *rapid.Generator[Scenario] {
	return rapid.Custom(func(t *rapid.T) Scenario {
		w := &world{}
		base := w.newDatabase(t)
		for i := range rapid.IntRange(2, 4).Draw(t, "copies") {
			w.forks = append(w.forks, &fork{
				name:     fmt.Sprintf("copy-%d", i+1),
				snapshot: Clone(base),
				clock:    start.Add(rapid.SampledFrom(clockErrors).Draw(t, "clock error")),
			})
		}
		for range rapid.IntRange(0, 14).Draw(t, "moves") {
			f := rapid.SampledFrom(w.forks).Draw(t, "fork")
			m := rapid.SampledFrom(moves).Draw(t, "move")
			m.apply(t, w, f)
		}
		scenario := Scenario{Now: start.Add(rapid.SampledFrom(ages).Draw(t, "age"))}
		for _, f := range w.forks {
			f.dropUnusedAttachments()
			scenario.Copies = append(scenario.Copies, merge.Copy{Name: f.name, Snapshot: f.snapshot})
		}
		return scenario
	})
}

const day = 24 * time.Hour

var (
	// clockErrors holds how wrong the clock of a system can be. Some values lie around the
	// limit of 24 hours, and some reach beyond the current time.
	clockErrors = []time.Duration{0, 0, 0, time.Hour, -time.Hour, 3 * time.Hour, -3 * time.Hour, day, -day, 30 * day}
	// steps holds how far a clock runs between two edits. A step of 0 gives two edits in the
	// same second, and equal steps from a same clock give a gap of exactly 24 hours.
	steps = []time.Duration{0, time.Second, time.Minute, time.Hour, 23 * time.Hour, day, 25 * time.Hour, 10 * day}
	// ages holds how long after the fork the merge runs.
	ages = []time.Duration{day, 7 * day, 30 * day, 60 * day}
)

// dropUnusedAttachments removes the bytes no version refers to any more, as a save does. The
// Gateway stops a file with such bytes, so no copy with them reaches the engine.
func (f *fork) dropUnusedAttachments() {
	used := map[snapshot.AttachmentID]bool{}
	for _, ref := range f.entries() {
		e := ref.entry()
		for _, a := range e.Attachments {
			used[a.ID] = true
		}
		for i := range e.History {
			for _, a := range e.History[i].Attachments {
				used[a.ID] = true
			}
		}
	}
	maps.DeleteFunc(f.snapshot.Attachments, func(id snapshot.AttachmentID, _ snapshot.Attachment) bool { return !used[id] })
}

// tick runs the clock of the fork and gives its time.
func (f *fork) tick(t *rapid.T) time.Time {
	f.clock = f.clock.Add(rapid.SampledFrom(steps).Draw(t, "step"))
	return f.clock
}

func (w *world) newUUID() snapshot.UUID {
	w.lastID++
	var id snapshot.UUID
	id[12], id[13], id[14], id[15] = byte(w.lastID>>24), byte(w.lastID>>16), byte(w.lastID>>8), byte(w.lastID)
	return id
}

// newDatabase generates the database all copies fork from.
func (w *world) newDatabase(t *rapid.T) *snapshot.Snapshot {
	s := &snapshot.Snapshot{
		Format:      snapshot.Format{Major: 4, Minor: 1, Cipher: snapshot.CipherAES256},
		Meta:        snapshot.Meta{DatabaseName: "base", SettingsChanged: start.Add(-100 * day)},
		Root:        snapshot.Group{UUID: w.newUUID(), Name: "Root"},
		Attachments: map[snapshot.AttachmentID]snapshot.Attachment{},
	}
	for range rapid.IntRange(0, 3).Draw(t, "groups") {
		s.Root.Groups = append(s.Root.Groups, snapshot.Group{
			UUID: w.newUUID(),
			Name: rapid.SampledFrom(groupNames).Draw(t, "group name"),
		})
	}
	if rapid.Bool().Draw(t, "recycle bin before the fork") {
		w.makeBin(s, start.Add(-90*day))
	}
	for range rapid.IntRange(1, 6).Draw(t, "entries") {
		group := rapid.IntRange(0, len(s.Root.Groups)).Draw(t, "group")
		e := w.newEntry(t, s, start.Add(-50*day))
		if group == 0 {
			s.Root.Entries = append(s.Root.Entries, e)
		} else {
			s.Root.Groups[group-1].Entries = append(s.Root.Groups[group-1].Entries, e)
		}
	}
	return s
}

// newEntry generates an entry with a short history, all before the given time.
func (w *world) newEntry(t *rapid.T, s *snapshot.Snapshot, before time.Time) snapshot.Entry {
	e := snapshot.Entry{UUID: w.newUUID()}
	created := before.Add(-10 * day)
	for i := range rapid.IntRange(0, 2).Draw(t, "history") {
		e.History = append(e.History, newVersion(t, s, created.Add(time.Duration(i)*time.Hour), created))
	}
	e.EntryVersion = newVersion(t, s, before, created)
	if rapid.Bool().Draw(t, "custom data") {
		setItem(t, &e.EntryVersion)
	}
	return e
}

var (
	titles     = []string{"Mail", "Bank", "Forum", "Git"}
	userNames  = []string{"user", "admin", "user@example.com"}
	urls       = []string{"", "https://example.com/login", "https://mail.example.com"}
	groupNames = []string{"Work", "Home", "Keys"}
	tagSets    = []string{"", "alpha", "alpha,beta", "beta;alpha", "gamma"}
	colors     = []string{"", "#112233", "#445566"}
)

func newVersion(t *rapid.T, s *snapshot.Snapshot, modified, created time.Time) snapshot.EntryVersion {
	v := snapshot.EntryVersion{
		Times: snapshot.Times{
			Creation:         created,
			LastModification: modified,
			LastAccess:       modified,
			LocationChanged:  created,
		},
		Fields: []snapshot.Field{
			{Key: "Title", Value: rapid.SampledFrom(titles).Draw(t, "title")},
			{Key: "UserName", Value: rapid.SampledFrom(userNames).Draw(t, "user name")},
			{Key: "Password", Value: drawSecret(t), Protected: true},
			{Key: "URL", Value: rapid.SampledFrom(urls).Draw(t, "URL")},
			{Key: "Notes", Value: drawText(t)},
		},
	}
	if rapid.Bool().Draw(t, "custom secret") {
		v.Fields = append(v.Fields, snapshot.Field{Key: "otp", Value: drawSecret(t), Protected: true})
	}
	if rapid.Bool().Draw(t, "attachment") {
		attach(t, s, &v)
	}
	return v
}

// drawSecret draws from few values, so that two copies can make the same edit by chance.
func drawSecret(t *rapid.T) string {
	return fmt.Sprintf("%s%d", SecretMarker, rapid.IntRange(0, 9).Draw(t, "secret"))
}

func drawText(t *rapid.T) string {
	return rapid.StringOfN(rapid.RuneFrom(nil, unicode.Cyrillic, unicode.Latin), 0, 8, -1).Draw(t, "text")
}

// attach adds an attachment to the version and its bytes to the copy.
func attach(t *rapid.T, s *snapshot.Snapshot, v *snapshot.EntryVersion) {
	content := rapid.SliceOfN(rapid.Byte(), 1, 4).Draw(t, "attachment bytes")
	id := snapshot.NewAttachmentID(content)
	stored := s.Attachments[id]
	stored.Content = content
	stored.Protected = stored.Protected || rapid.Bool().Draw(t, "protected attachment")
	s.Attachments[id] = stored
	v.Attachments = append(v.Attachments, snapshot.AttachmentRef{
		Name: rapid.SampledFrom([]string{"a.bin", "b.bin"}).Draw(t, "attachment name"),
		ID:   id,
	})
}

// groups lists all groups of the copy, parents first.
func (f *fork) groups() []*snapshot.Group {
	var result []*snapshot.Group
	f.snapshot.Root.Walk(func(g *snapshot.Group) { result = append(result, g) })
	return result
}

// entryRef points to an entry and the group that holds it.
type entryRef struct {
	group *snapshot.Group
	index int
}

func (r entryRef) entry() *snapshot.Entry { return &r.group.Entries[r.index] }

// entries lists all entries of the copy.
func (f *fork) entries() []entryRef {
	var result []entryRef
	for _, g := range f.groups() {
		for i := range g.Entries {
			result = append(result, entryRef{group: g, index: i})
		}
	}
	return result
}

// pickEntry draws one entry of the copy, if it has any.
func (f *fork) pickEntry(t *rapid.T) (entryRef, bool) {
	entries := f.entries()
	if len(entries) == 0 {
		return entryRef{}, false
	}
	return entries[rapid.IntRange(0, len(entries)-1).Draw(t, "entry")], true
}

package main_test

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/testkit"
)

// The differential test of the spec: with no conflict in the copies, the main versions of our
// result are what `keepassxc-cli merge` gives. docs/acceptance.md lists every difference and why it
// is there. A difference nobody has named fails the test.

type exportedItem struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type exportedTimes struct {
	LastModification string `xml:"LastModificationTime"`
	Creation         string `xml:"CreationTime"`
	LastAccess       string `xml:"LastAccessTime"`
	Expiry           string `xml:"ExpiryTime"`
	Expires          string `xml:"Expires"`
	UsageCount       string `xml:"UsageCount"`
	LocationChanged  string `xml:"LocationChanged"`
}

type exportedEntry struct {
	UUID                string         `xml:"UUID"`
	IconID              string         `xml:"IconID"`
	CustomIconUUID      string         `xml:"CustomIconUUID"`
	ForegroundColor     string         `xml:"ForegroundColor"`
	BackgroundColor     string         `xml:"BackgroundColor"`
	OverrideURL         string         `xml:"OverrideURL"`
	QualityCheck        string         `xml:"QualityCheck"`
	Tags                string         `xml:"Tags"`
	PreviousParentGroup string         `xml:"PreviousParentGroup"`
	Times               exportedTimes  `xml:"Times"`
	Strings             []exportedItem `xml:"String"`
	Binaries            []struct {
		Key string `xml:"Key"`
	} `xml:"Binary"`
	AutoType struct {
		Enabled         string `xml:"Enabled"`
		Obfuscation     string `xml:"DataTransferObfuscation"`
		DefaultSequence string `xml:"DefaultSequence"`
		Associations    []struct {
			Window   string `xml:"Window"`
			Sequence string `xml:"KeystrokeSequence"`
		} `xml:"Association"`
	} `xml:"AutoType"`
	CustomData []exportedItem  `xml:"CustomData>Item"`
	History    []exportedEntry `xml:"History>Entry"`
}

type exportedGroup struct {
	UUID    string          `xml:"UUID"`
	Name    string          `xml:"Name"`
	Notes   string          `xml:"Notes"`
	IconID  string          `xml:"IconID"`
	Entries []exportedEntry `xml:"Entry"`
	Groups  []exportedGroup `xml:"Group"`
}

// seenByKeePassXC is what the real KeePassXC shows of a file.
type seenByKeePassXC struct {
	// mains holds each main version by the UUID of its entry: the name of a part and its value.
	mains map[string]map[string]string
	// versionTimes holds the times of the versions behind each main version.
	versionTimes map[string][]string
	// groups holds the paths of the groups.
	groups []string
}

func seenBy(t *testing.T, db testkit.DB) seenByKeePassXC {
	t.Helper()
	var file struct {
		Root struct {
			Groups []exportedGroup `xml:"Group"`
		} `xml:"Root"`
	}
	if err := xml.Unmarshal([]byte(db.CLI(t, "export")), &file); err != nil {
		t.Fatalf("read the export of %s: %v", db.Path, err)
	}
	if len(file.Root.Groups) != 1 {
		t.Fatalf("the export of %s has %d root groups", db.Path, len(file.Root.Groups))
	}
	seen := seenByKeePassXC{mains: map[string]map[string]string{}, versionTimes: map[string][]string{}}
	var walk func(g *exportedGroup, path string)
	walk = func(g *exportedGroup, path string) {
		seen.groups = append(seen.groups, path)
		for i := range g.Entries {
			e := &g.Entries[i]
			seen.mains[e.UUID] = partsOf(e, path)
			for j := range e.History {
				seen.versionTimes[e.UUID] = append(seen.versionTimes[e.UUID], e.History[j].Times.LastModification)
			}
		}
		for i := range g.Groups {
			walk(&g.Groups[i], path+"/"+g.Groups[i].Name)
		}
	}
	walk(&file.Root.Groups[0], "")
	sort.Strings(seen.groups)
	return seen
}

func partsOf(e *exportedEntry, group string) map[string]string {
	parts := map[string]string{
		"group":             group,
		"modified":          e.Times.LastModification,
		"created":           e.Times.Creation,
		"expires":           e.Times.Expires,
		"last access":       e.Times.LastAccess,
		"usage count":       e.Times.UsageCount,
		"location changed":  e.Times.LocationChanged,
		"previous location": e.PreviousParentGroup,
		"icon":              e.IconID,
		"custom icon":       e.CustomIconUUID,
		"foreground":        e.ForegroundColor,
		"background":        e.BackgroundColor,
		"override URL":      e.OverrideURL,
		"quality check":     e.QualityCheck,
		"tags":              e.Tags,
		"Auto-Type":         e.AutoType.Enabled,
		"obfuscation":       e.AutoType.Obfuscation,
		"default sequence":  e.AutoType.DefaultSequence,
	}
	if e.Times.Expires == "True" {
		parts["expiry"] = e.Times.Expiry
	}
	for _, field := range e.Strings {
		parts["field "+field.Key] = field.Value
	}
	for _, attachment := range e.Binaries {
		parts["attachment "+attachment.Key] = "there"
	}
	for i, association := range e.AutoType.Associations {
		parts[fmt.Sprintf("association %d", i)] = association.Window + " " + association.Sequence
	}
	for _, item := range e.CustomData {
		if item.Key != snapshot.CustomDataLastModified {
			parts["custom data "+item.Key] = item.Value
		}
	}
	return parts
}

// momentOf reads a time as an export prints it for KDBX 4: seconds from year 1, in base64.
func momentOf(t *testing.T, text string) int64 {
	t.Helper()
	if text == "" {
		return 0
	}
	if raw, err := base64.StdEncoding.DecodeString(text); err == nil && len(raw) == 8 {
		return int64(binary.LittleEndian.Uint64(raw))
	}
	moment, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatalf("a time in the export: %q", text)
	}
	return moment.Unix()
}

// The differences docs/acceptance.md names. Each lets the two sides differ in one direction only:
// cleanpass holds what KeePassXC holds, and more.
const (
	diffNewestRead     = "last access and usage count are the maximum over all copies"
	diffNewestLocation = "the time of the location is the newest, also for a move that ended in the same group"
	diffCustomData     = "a version with one time and one content takes the custom data of all copies"
	diffMoreVersions   = "the number of versions differs: versions with one time stay apart here, and KeePassXC takes them for one"
	diffNewestOfOne    = "the main version has the newest time of its content, also when that time lies in a history"
	diffEmptyGroups    = "empty groups differ: one group on a path, one bin, and a group a copy holds outside the bin stays there"
)

// sortOutDifference says which named difference this is, or that it is none.
func sortOutDifference(t *testing.T, part, ours, theirs string) (named string, known bool) {
	t.Helper()
	switch {
	case part == "modified":
		// Any other part of a main version that differs fails the test, so the content is the same.
		return diffNewestOfOne, momentOf(t, ours) > momentOf(t, theirs)
	case part == "last access":
		return diffNewestRead, momentOf(t, ours) > momentOf(t, theirs)
	case part == "usage count":
		mine, errOurs := strconv.Atoi(ours)
		other, errTheirs := strconv.Atoi(theirs)
		return diffNewestRead, errOurs == nil && errTheirs == nil && mine > other
	case part == "location changed":
		return diffNewestLocation, momentOf(t, ours) > momentOf(t, theirs)
	case part == "previous location":
		return diffNewestLocation, true
	case strings.HasPrefix(part, "custom data "):
		return diffCustomData, theirs == ""
	}
	return "", false
}

// compareResults fails the test on a difference nobody has named, and counts those named.
func compareResults(t *testing.T, ours, theirs testkit.DB, named map[string]int) {
	t.Helper()
	mine, other := seenBy(t, ours), seenBy(t, theirs)
	ids := map[string]bool{}
	for id := range mine.mains {
		ids[id] = true
	}
	for id := range other.mains {
		ids[id] = true
	}
	for id := range ids {
		parts := map[string]bool{}
		for part := range mine.mains[id] {
			parts[part] = true
		}
		for part := range other.mains[id] {
			parts[part] = true
		}
		for part := range parts {
			a, b := mine.mains[id][part], other.mains[id][part]
			if a == b {
				continue
			}
			if name, known := sortOutDifference(t, part, a, b); known {
				named[name]++
				continue
			}
			t.Errorf("entry %s, %s: cleanpass %q, KeePassXC %q", id, part, a, b)
		}
		times := slices.Clone(mine.versionTimes[id])
		times = append(times, mine.mains[id]["modified"])
		for _, moment := range other.versionTimes[id] {
			if !slices.Contains(times, moment) {
				t.Errorf("entry %s: KeePassXC holds a version with the time %q, and cleanpass does not", id, moment)
			}
		}
		if len(mine.versionTimes[id]) != len(other.versionTimes[id]) {
			named[diffMoreVersions]++
		}
	}
	// A group that holds entries shows in the place of those entries above, so only a difference in
	// groups with nothing in them can get here.
	if !slices.Equal(mine.groups, other.groups) {
		named[diffEmptyGroups]++
	}
	wantSameAttachments(t, ours, theirs)
}

// wantSameAttachments compares the bytes of the attachments of the main versions. The export does
// not hold them, and attachment-export finds an entry by a path two entries can share.
func wantSameAttachments(t *testing.T, ours, theirs testkit.DB) {
	t.Helper()
	of := func(db testkit.DB) map[string]map[string]snapshot.AttachmentID {
		snap, err := gateway.Open(db.Path, gateway.Credentials{Password: []byte(db.Password)})
		if err != nil {
			t.Fatalf("open %s: %v", db.Path, err)
		}
		found := map[string]map[string]snapshot.AttachmentID{}
		snap.Root.Walk(func(g *snapshot.Group) {
			for i := range g.Entries {
				e := &g.Entries[i]
				found[e.UUID.String()] = map[string]snapshot.AttachmentID{}
				for _, attachment := range e.Attachments {
					found[e.UUID.String()][attachment.Name] = attachment.ID
				}
			}
		})
		return found
	}
	mine, other := of(ours), of(theirs)
	if !maps.EqualFunc(mine, other, func(a, b map[string]snapshot.AttachmentID) bool { return maps.Equal(a, b) }) {
		t.Error("the main versions hold other bytes of attachments")
	}
}

// mergedByKeePassXC gives what keepassxc-cli merge makes of the copies, one after another, in a new
// file. The real merge takes two files and changes the first one.
func mergedByKeePassXC(t *testing.T, path string, copies ...testkit.DB) testkit.DB {
	t.Helper()
	data, err := os.ReadFile(copies[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	target := copies[0]
	target.Path = path
	for _, source := range copies[1:] {
		target.CLI(t, "merge", "--same-credentials", source.Path)
	}
	return target
}

// mergedByCleanpass gives the result of the built tool with no answer to a question.
func mergedByCleanpass(t *testing.T, dir string, copies ...testkit.DB) testkit.DB {
	t.Helper()
	result := testkit.DB{Path: filepath.Join(dir, "cleanpass.kdbx"), Password: copies[0].Password}
	args := []string{"merge", "--allow-unrelated"}
	for _, c := range copies {
		args = append(args, c.Path)
	}
	wantJudged(t, testkit.Run(t, passwordLines(len(copies)), append(args, "--output", result.Path)...))
	return result
}

// compareWithBothOrders checks the copies against the real merge in both orders of that merge.
func compareWithBothOrders(t *testing.T, dir string, named map[string]int, copies ...testkit.DB) {
	t.Helper()
	ours := mergedByCleanpass(t, dir, copies...)
	backward := slices.Clone(copies)
	slices.Reverse(backward)
	compareResults(t, ours, mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), copies...), named)
	compareResults(t, ours, mergedByKeePassXC(t, filepath.Join(dir, "keepassxc-backward.kdbx"), backward...), named)
}

// realEdit is a change the real keepassxc-cli makes and saves.
type realEdit func(t *testing.T, db testkit.DB)

func kxc(command string, args ...string) realEdit {
	return func(t *testing.T, db testkit.DB) {
		t.Helper()
		db.CLI(t, command, args...)
	}
}

func attachBytes(entry, name, content string) realEdit {
	return func(t *testing.T, db testkit.DB) {
		t.Helper()
		db.AttachFile(t, entry, name, []byte(content))
	}
}

// nextSecond keeps two steps apart on the clock. In one second their order has no meaning for
// either tool: KeePassXC stores whole seconds, and its move renews the time of the entry.
func nextSecond(*testing.T, testkit.DB) { time.Sleep(1100 * time.Millisecond) }

// Every change here is one a person makes: the real KeePassXC makes it and saves the file.
func TestDifferentialWithRealEdits(t *testing.T) {
	t.Parallel()
	const (
		bin   = "/Кошик/У кошику"
		other = richWorkEntry
		mail  = richMailEntry
	)
	for name, edits := range map[string][][]realEdit{
		"one copy lags":                    {{}, {kxc("edit", "--notes", "нове", other)}},
		"each copy edits its own entry":    {{kxc("edit", "--notes", "перша", other)}, {kxc("edit", "-u", "новий логін", mail)}},
		"each copy adds an entry":          {{kxc("add", "-u", "a", "/Нове в першій")}, {kxc("add", "-u", "b", "/Робота/Нове в другій")}},
		"a new group with an entry":        {{}, {kxc("mkdir", "/Нова група"), kxc("add", "--url", "https://example.org", "/Нова група/Запис")}},
		"an entry moves":                   {{}, {kxc("mv", other, "/")}},
		"one moves, later the other edits": {{kxc("mv", other, "/")}, {nextSecond, kxc("edit", "--notes", "зміна", other)}},
		"a title changes":                  {{}, {kxc("edit", "-t", "Нова назва", other)}},
		"a new password":                   {{kxc("edit", "-g", "-L", "24", other)}, {}},
		"an attachment comes":              {{}, {attachBytes(other, "нове.bin", "нові байти")}},
		"an attachment goes":               {{}, {kxc("attachment-rm", mail, "file.bin")}},
		"a plain save":                     {{}, {func(t *testing.T, db testkit.DB) { t.Helper(); saveAgain(t, db) }}},
		"three copies, each with a step":   {{kxc("edit", "--notes", "перша", other)}, {kxc("add", "-u", "b", "/Нове")}, {kxc("edit", "--notes", "третя", bin)}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			base := testkit.DB{Path: filepath.Join(dir, "base.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
			copies := make([]testkit.DB, len(edits))
			for i, steps := range edits {
				copies[i] = copyOf(t, base, fmt.Sprintf("copy-%d.kdbx", i))
				for _, step := range steps {
					step(t, copies[i])
				}
			}

			named := map[string]int{}
			compareWithBothOrders(t, dir, named, copies...)

			for difference, count := range named {
				t.Logf("%d times: %s", count, difference)
			}
		})
	}
}

// asKeePassXCWrites fills the times the generator leaves empty with one old time. KeePassXC writes
// them all, and it takes a missing one for the moment it reads the file, so a missing time would
// compare the clock of this test.
func asKeePassXCWrites(s *snapshot.Snapshot) {
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	fill := func(moment *time.Time) {
		if moment.IsZero() {
			*moment = old
		}
	}
	fillAll := func(times *snapshot.Times) {
		fill(&times.Creation)
		fill(&times.LastModification)
		fill(&times.LastAccess)
		fill(&times.Expiry)
		fill(&times.LocationChanged)
	}
	s.Root.Walk(func(g *snapshot.Group) {
		fillAll(&g.Times)
		for i := range g.Entries {
			fillAll(&g.Entries[i].Times)
			for j := range g.Entries[i].History {
				fillAll(&g.Entries[i].History[j].Times)
			}
		}
	})
}

// withoutConflict says the copies hold what the spec calls a scenario without conflicts. It reads
// the copies alone, by the words of the spec, so that the code under test does not pick its own
// cases. A scenario stays out if:
//   - two copies hold different main versions of an entry and neither holds the other one in its
//     history. A version is the same only if it is the same in all, so a version KeePassXC kept
//     without its custom data counts as another one, and such a scenario stays out too;
//   - the newest version of an entry is in the history of a copy with another main version: a
//     clock ran behind, and the tool asks;
//   - a record names a deleted entry a copy still holds, or a copy holds an entry in its bin while
//     another one holds it outside. Here that asks, and the real merge goes another way;
//   - the newest moves put groups under each other: the real merge never ends on such a cycle.
//
// TestDifference... shows those cases.
func withoutConflict(copies []merge.Copy) bool {
	type holding struct {
		main    string
		history map[string]bool
	}
	held := map[snapshot.UUID][]holding{}
	newest := map[snapshot.UUID]time.Time{}
	newestVersion := map[snapshot.UUID]string{}
	recorded, inBin, outside := map[snapshot.UUID]bool{}, map[snapshot.UUID]bool{}, map[snapshot.UUID]bool{}
	see := func(id snapshot.UUID, v *snapshot.EntryVersion) string {
		version := fmt.Sprint(v.Times.LastModification.Unix(), contentOf(v), customDataOf(v))
		if at := v.Times.LastModification; newestVersion[id] == "" || at.After(newest[id]) {
			newest[id], newestVersion[id] = at, version
		}
		return version
	}
	for _, c := range copies {
		s := c.Snapshot
		for _, d := range s.Deletions {
			recorded[d.UUID] = true
		}
		walkWithBin(s, func(g *snapshot.Group, binned bool) {
			for i := range g.Entries {
				e := &g.Entries[i]
				h := holding{main: see(e.UUID, &e.EntryVersion), history: map[string]bool{}}
				for j := range e.History {
					h.history[see(e.UUID, &e.History[j])] = true
				}
				held[e.UUID] = append(held[e.UUID], h)
				if binned {
					inBin[e.UUID] = true
				} else {
					outside[e.UUID] = true
				}
			}
		})
	}
	for id := range held {
		if recorded[id] || (inBin[id] && outside[id]) {
			return false
		}
	}
	for id, holdings := range held {
		for i, a := range holdings {
			for _, b := range holdings[i+1:] {
				if a.main != b.main && !a.history[b.main] && !b.history[a.main] {
					return false
				}
			}
			if a.main != newestVersion[id] && a.history[newestVersion[id]] {
				return false
			}
		}
	}
	return !groupsMoveInACycle(copies)
}

// The choice of scenarios reads the copies by the words of the spec. These cases pin it down.
func TestTheDifferentialChoosesScenariosByTheCopiesAlone(t *testing.T) {
	t.Parallel()
	at := func(hour int) time.Time { return time.Date(2026, 1, 1, hour, 0, 0, 0, time.UTC) }
	version := func(hour int, password string) snapshot.EntryVersion {
		return snapshot.EntryVersion{Times: snapshot.Times{LastModification: at(hour)}, Fields: []snapshot.Field{{Key: "Password", Value: password}}}
	}
	entryID, binID, groupA, groupB := snapshot.UUID{1}, snapshot.UUID{2}, snapshot.UUID{3}, snapshot.UUID{4}
	withEntry := func(e snapshot.Entry) *snapshot.Snapshot {
		return &snapshot.Snapshot{Root: snapshot.Group{UUID: snapshot.UUID{9}, Entries: []snapshot.Entry{e}}}
	}
	entry := func(main snapshot.EntryVersion, history ...snapshot.EntryVersion) snapshot.Entry {
		return snapshot.Entry{UUID: entryID, EntryVersion: main, History: history}
	}
	recorded := withEntry(entry(version(1, "a")))
	recorded.Deletions = []snapshot.Deletion{{UUID: entryID, Time: at(5)}}
	binned := &snapshot.Snapshot{Root: snapshot.Group{UUID: snapshot.UUID{9}, Groups: []snapshot.Group{{UUID: binID, Entries: []snapshot.Entry{entry(version(1, "a"))}}}}}
	binned.Meta.RecycleBin = binID
	moved := func(child, parent snapshot.UUID, hour int) *snapshot.Snapshot {
		return &snapshot.Snapshot{Root: snapshot.Group{UUID: snapshot.UUID{9}, Groups: []snapshot.Group{{
			UUID: parent, Groups: []snapshot.Group{{UUID: child, Times: snapshot.Times{LocationChanged: at(hour)}}},
		}}}}
	}

	for name, tc := range map[string]struct {
		copies []*snapshot.Snapshot
		want   bool
	}{
		"the same entry":          {[]*snapshot.Snapshot{withEntry(entry(version(1, "a"))), withEntry(entry(version(1, "a")))}, true},
		"one copy lags":           {[]*snapshot.Snapshot{withEntry(entry(version(1, "a"))), withEntry(entry(version(2, "b"), version(1, "a")))}, true},
		"two edits":               {[]*snapshot.Snapshot{withEntry(entry(version(2, "b"), version(1, "a"))), withEntry(entry(version(3, "c"), version(1, "a")))}, false},
		"a clock that ran behind": {[]*snapshot.Snapshot{withEntry(entry(version(5, "b"))), withEntry(entry(version(2, "c"), version(5, "b")))}, false},
		"a record of deletion":    {[]*snapshot.Snapshot{withEntry(entry(version(1, "a"))), recorded}, false},
		"the bin in one copy":     {[]*snapshot.Snapshot{withEntry(entry(version(1, "a"))), binned}, false},
		"a cycle of groups":       {[]*snapshot.Snapshot{moved(groupA, groupB, 5), moved(groupB, groupA, 6)}, false},
		"one move of a group":     {[]*snapshot.Snapshot{moved(groupA, groupB, 5), moved(groupA, groupB, 1)}, true},
	} {
		t.Run(name, func(t *testing.T) {
			copies := make([]merge.Copy, len(tc.copies))
			for i, s := range tc.copies {
				copies[i] = merge.Copy{Snapshot: s}
			}
			if got := withoutConflict(copies); got != tc.want {
				t.Errorf("without conflict = %v, want %v", got, tc.want)
			}
		})
	}
}

// walkWithBin visits each group of a copy and says whether it lies in the bin of that copy.
func walkWithBin(s *snapshot.Snapshot, visit func(g *snapshot.Group, inBin bool)) {
	var walk func(g *snapshot.Group, inBin bool)
	walk = func(g *snapshot.Group, inBin bool) {
		inBin = inBin || (!s.Meta.RecycleBin.IsZero() && g.UUID == s.Meta.RecycleBin)
		visit(g, inBin)
		for i := range g.Groups {
			walk(&g.Groups[i], inBin)
		}
	}
	walk(&s.Root, false)
}

// groupsMoveInACycle follows the parent of each group by its newest move, as the spec says. A move
// into a bin counts for nothing while another copy holds the group outside.
func groupsMoveInACycle(copies []merge.Copy) bool {
	type move struct {
		parent snapshot.UUID
		at     time.Time
		binned bool
	}
	moves := map[snapshot.UUID][]move{}
	outside := map[snapshot.UUID]bool{}
	for _, c := range copies {
		parents := map[snapshot.UUID]snapshot.UUID{}
		c.Snapshot.Root.Walk(func(g *snapshot.Group) {
			for i := range g.Groups {
				parents[g.Groups[i].UUID] = g.UUID
			}
		})
		walkWithBin(c.Snapshot, func(g *snapshot.Group, binned bool) {
			if parent, ok := parents[g.UUID]; ok {
				moves[g.UUID] = append(moves[g.UUID], move{parent: parent, at: g.Times.LocationChanged, binned: binned})
				outside[g.UUID] = outside[g.UUID] || !binned
			}
		})
	}
	newestParents := map[snapshot.UUID][]snapshot.UUID{}
	for id, all := range moves {
		counted := slices.DeleteFunc(slices.Clone(all), func(m move) bool { return outside[id] && m.binned })
		if len(counted) == 0 {
			continue
		}
		best := slices.MaxFunc(counted, func(a, b move) int { return a.at.Compare(b.at) }).at
		for _, m := range counted {
			if m.at.Equal(best) {
				newestParents[id] = append(newestParents[id], m.parent)
			}
		}
	}
	var leadsTo func(from, to snapshot.UUID, seen map[snapshot.UUID]bool) bool
	leadsTo = func(from, to snapshot.UUID, seen map[snapshot.UUID]bool) bool {
		for _, parent := range newestParents[from] {
			if parent == to {
				return true
			}
			if !seen[parent] {
				seen[parent] = true
				if leadsTo(parent, to, seen) {
					return true
				}
			}
		}
		return false
	}
	for id := range newestParents {
		if leadsTo(id, id, map[snapshot.UUID]bool{}) {
			return true
		}
	}
	return false
}

func customDataOf(v *snapshot.EntryVersion) []string {
	var items []string
	for _, item := range v.CustomData {
		if !item.IsService() {
			items = append(items, item.Key+"="+item.Value)
		}
	}
	slices.Sort(items)
	return items
}

// keePassXCCanHold says the copies hold nothing KeePassXC has no way to hold or to order: a name of
// an attachment taken twice in a version, or two versions of an entry with one time and other
// content. The first one stops our merge by design. The second one has no right answer: KeePassXC
// takes those two for one version, and a key of the run orders them here. Custom data counts as
// other content only if neither version holds all of the other's: the rule of ticket #17.
func keePassXCCanHold(copies []merge.Copy) bool {
	type moment struct {
		id snapshot.UUID
		at int64
	}
	versions := map[moment][]*snapshot.EntryVersion{}
	holds := true
	see := func(id snapshot.UUID, v *snapshot.EntryVersion) {
		names := map[string]bool{}
		for _, attachment := range v.Attachments {
			holds = holds && !names[attachment.Name]
			names[attachment.Name] = true
		}
		at := moment{id, v.Times.LastModification.Unix()}
		for _, known := range versions[at] {
			if contentOf(known) != contentOf(v) || !(holdsAllCustomData(known, v) || holdsAllCustomData(v, known)) {
				holds = false
			}
		}
		versions[at] = append(versions[at], v)
	}
	for _, c := range copies {
		c.Snapshot.Root.Walk(func(g *snapshot.Group) {
			for i := range g.Entries {
				see(g.Entries[i].UUID, &g.Entries[i].EntryVersion)
				for j := range g.Entries[i].History {
					see(g.Entries[i].UUID, &g.Entries[i].History[j])
				}
			}
		})
	}
	return holds
}

func holdsAllCustomData(larger, smaller *snapshot.EntryVersion) bool {
	for _, item := range smaller.CustomData {
		if item.IsService() {
			continue
		}
		if !slices.ContainsFunc(larger.CustomData, func(other snapshot.CustomDataItem) bool {
			return other.Key == item.Key && other.Value == item.Value
		}) {
			return false
		}
	}
	return true
}

// contentOf gives a version without what a save can change on the same content.
func contentOf(v *snapshot.EntryVersion) string {
	plain := *v
	plain.Times = snapshot.Times{}
	plain.CustomData = nil
	plain.PreviousParentGroup = nil
	plain.Fields = slices.Clone(v.Fields)
	slices.SortFunc(plain.Fields, func(a, b snapshot.Field) int { return strings.Compare(a.Key, b.Key) })
	plain.Attachments = slices.Clone(v.Attachments)
	slices.SortFunc(plain.Attachments, func(a, b snapshot.AttachmentRef) int { return strings.Compare(a.Name, b.Name) })
	plain.Tags = strings.ReplaceAll(v.Tags, ",", ";")
	return fmt.Sprintf("%+v", plain)
}

// differentialScenarios is how many generated scenarios the test compares. A person can ask for
// more: docs/acceptance.md gives the largest run so far.
func differentialScenarios(t *testing.T) int {
	t.Helper()
	text := os.Getenv("CLEANPASS_DIFFERENTIAL_SCENARIOS")
	if text == "" {
		return 40
	}
	count, err := strconv.Atoi(text)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// The generator of the property tests gives moves no table would hold, and the real merge reads
// the same files as the tool. The moves that clash here are the ones it skips.
func TestDifferentialWithGeneratedScenarios(t *testing.T) {
	t.Parallel()
	var (
		lock    sync.Mutex
		named   = map[string]int{}
		checked int
		skipped int
	)
	want := differentialScenarios(t)
	t.Run("scenario", func(t *testing.T) {
		for seed := 0; checked < want; seed++ {
			s := mergetest.Scenarios().Example(seed)
			for i := range s.Copies {
				asKeePassXCWrites(s.Copies[i].Snapshot)
			}
			if !withoutConflict(s.Copies) || !keePassXCCanHold(s.Copies) {
				skipped++
				continue
			}
			checked++
			t.Run(strconv.Itoa(seed), func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				copies := make([]testkit.DB, len(s.Copies))
				for i := range s.Copies {
					copies[i] = writtenCopy(t, filepath.Join(dir, fmt.Sprintf("copy-%d.kdbx", i)), s.Copies[i].Snapshot)
				}
				seen := map[string]int{}
				compareWithBothOrders(t, dir, seen, copies...)
				lock.Lock()
				defer lock.Unlock()
				for difference, count := range seen {
					named[difference] += count
				}
			})
		}
	})
	t.Logf("%d scenarios compared, %d left out as conflicts or as what KeePassXC cannot hold", checked, skipped)
	for difference, count := range named {
		t.Logf("%6d times: %s", count, difference)
	}
}

// writtenCopy saves a generated copy at a low cost of the KDF.
func writtenCopy(t *testing.T, path string, s *snapshot.Snapshot) testkit.DB {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	format := snapshot.Format{Major: 4, Minor: 1, Cipher: snapshot.CipherAES256, KDF: snapshot.KDF{Kind: snapshot.KDFAES, Rounds: 1000}}
	if err := gateway.Write(file, s, format, gateway.Credentials{Password: []byte(testkit.Password)}); err != nil {
		t.Fatal(err)
	}
	return testkit.DB{Path: path, Password: testkit.Password}
}

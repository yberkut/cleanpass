package main_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/testkit"
)

// Each test here shows one difference from `keepassxc-cli merge` that docs/acceptance.md names, on
// both tools. So the document says what the tools do, and a new KeePassXC that changes its
// behaviour fails a test here.

// The entry Друге of the fixture, as the real KeePassXC names it.
const (
	richWorkEntryID = "AAAAAAAAAAAAAAAAAAABAQ=="
	richBinEntry    = "/Кошик/Друге"
	richWorkGroup   = "/Робота"
	richBinGroup    = "/Кошик"
)

// forkedPair gives two copies with the same bytes, as a copy over a flash drive gives them.
func forkedPair(t *testing.T, xml string) (dir string, a, b testkit.DB) {
	t.Helper()
	dir = t.TempDir()
	base := testkit.DB{Path: filepath.Join(dir, "base.kdbx"), Password: testkit.Password}.Import(t, xml)
	return dir, copyOf(t, base, "a.kdbx"), copyOf(t, base, "b.kdbx")
}

// mergedWithAnswers is the result of the built tool with these lines after the passwords.
func mergedWithAnswers(t *testing.T, dir, answers string, copies ...testkit.DB) testkit.DB {
	t.Helper()
	result := testkit.DB{Path: filepath.Join(dir, "answered.kdbx"), Password: copies[0].Password}
	args := []string{"merge"}
	for _, c := range copies {
		args = append(args, c.Path)
	}
	wantJudged(t, testkit.Run(t, passwordLines(len(copies))+answers, append(args, "--output", result.Path)...))
	return result
}

const answerDelete = "2\n"

// The real merge never applies a record about a deletion in its default mode, and it says nothing.
// The spec expected it could. Here a deletion is a question: with no answer the entry stays as in
// the real merge, and only the answer "delete" removes it.
func TestDifferenceADeletionIsAQuestion(t *testing.T) {
	t.Parallel()
	dir, a, b := forkedPair(t, mergeableXML())
	b.CLI(t, "rm", richWorkEntry)
	b.CLI(t, "rm", richBinEntry)

	theirs := mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), a, b)
	if got := seenBy(t, theirs).mains[richWorkEntryID]["group"]; got != richWorkGroup {
		t.Errorf("the real merge has the entry in %q, want %s: this difference may be gone", got, richWorkGroup)
	}

	silent := mergedByCleanpass(t, dir, a, b)
	compareResults(t, silent, theirs, map[string]int{})

	answered := seenBy(t, mergedWithAnswers(t, dir, answerDelete, a, b))
	if _, there := answered.mains[richWorkEntryID]; there {
		t.Error("the answer did not delete the entry")
	}
}

// The real merge takes a move to the bin as any other move. Here that is a question as well.
func TestDifferenceAMoveToTheBinIsAQuestion(t *testing.T) {
	t.Parallel()
	dir, a, b := forkedPair(t, mergeableXML())
	b.CLI(t, "rm", richWorkEntry)

	theirs := seenBy(t, mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), a, b))
	if got := theirs.mains[richWorkEntryID]["group"]; got != richBinGroup {
		t.Errorf("the real merge put the entry in %q, want %s: this difference may be gone", got, richBinGroup)
	}

	silent := mergedByCleanpass(t, dir, a, b)
	if got := seenBy(t, silent).mains[richWorkEntryID]["group"]; got != richWorkGroup {
		t.Errorf("with no answer the entry lies in %q, want %s", got, richWorkGroup)
	}

	answered := seenBy(t, mergedWithAnswers(t, dir, answerDelete, a, b))
	if got := answered.mains[richWorkEntryID]["group"]; got != richBinGroup {
		t.Errorf("with the answer the entry lies in %q, want %s", got, richBinGroup)
	}
}

// The real merge leaves the name of the database as the first file has it. Here the newest wins.
func TestDifferenceTheNewestNameOfTheDatabaseWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, replaced(t, mergeableXML(),
		"<DatabaseName>Синтетична база<", "<DatabaseName>Нова назва<",
		"<DatabaseNameChanged>2026-02-02T10:00:01Z<", "<DatabaseNameChanged>2026-04-02T10:00:01Z<"))

	theirs := strings.Join(mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), a, b).ExportLines(t), "\n")
	if !strings.Contains(theirs, "<DatabaseName>Синтетична база<") {
		t.Error("the real merge has taken the new name: this difference may be gone")
	}

	ours := strings.Join(mergedByCleanpass(t, dir, a, b).ExportLines(t), "\n")
	if !strings.Contains(ours, "<DatabaseName>Нова назва<") {
		t.Error("the result does not have the newest name")
	}
}

// olderVersionsXML gives the fixture with older versions of the entry Друге.
func olderVersionsXML(t *testing.T, name string, days ...int) string {
	t.Helper()
	var history strings.Builder
	for _, day := range days {
		fmt.Fprintf(&history, `<Entry><UUID>%s</UUID><Times><LastModificationTime>2026-02-%sT10:00:00Z</LastModificationTime></Times><String><Key>Title</Key><Value>Друге</Value></String><String><Key>Password</Key><Value ProtectInMemory="True">%s-%s-%s</Value></String></Entry>`,
			richWorkEntryID, twoDigits(day), secretMarker, name, twoDigits(day))
	}
	return replaced(t, mergeableXML(),
		`<AutoType><Enabled>False</Enabled><DataTransferObfuscation>0</DataTransferObfuscation></AutoType><History/>`,
		`<AutoType><Enabled>False</Enabled><DataTransferObfuscation>0</DataTransferObfuscation></AutoType><History>`+history.String()+`</History>`,
		"<HistoryMaxItems>25<", "<HistoryMaxItems>10<")
}

// The real merge cuts the history at the limit of the file. Here the limit rises.
func TestDifferenceAllVersionsStayOverTheHistoryLimit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, olderVersionsXML(t, "a", 1, 2, 3, 4, 5, 6, 7, 8))
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, olderVersionsXML(t, "b", 11, 12, 13, 14, 15, 16, 17, 18))

	theirs := seenBy(t, mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), a, b))
	if got := len(theirs.versionTimes[richWorkEntryID]); got >= 16 {
		t.Errorf("the real merge kept %d versions of 16: this difference may be gone", got)
	}

	ours := seenBy(t, mergedByCleanpass(t, dir, a, b))
	if got := len(ours.versionTimes[richWorkEntryID]); got != 16 {
		t.Errorf("the result has %d versions, want 16", got)
	}
}

// The real merge takes all versions with one time for one, so it loses all of them but one.
func TestDifferenceVersionsWithOneTimeStayApart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, olderVersionsXML(t, "a", 5))
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, olderVersionsXML(t, "b", 5))
	oneOf := secretMarker + "-a-05"
	otherOf := secretMarker + "-b-05"

	theirs := strings.Join(mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), a, b).ExportLines(t), "\n")
	if strings.Contains(theirs, oneOf) && strings.Contains(theirs, otherOf) {
		t.Error("the real merge kept both versions: this difference may be gone")
	}

	ours := strings.Join(mergedByCleanpass(t, dir, a, b).ExportLines(t), "\n")
	if !strings.Contains(ours, oneOf) || !strings.Contains(ours, otherOf) {
		t.Error("the result lost a version")
	}
}

// Two groups with one path and two UUIDs stay two groups in the real merge. Here they are one.
func TestDifferenceGroupsOnOnePathAreOne(t *testing.T) {
	t.Parallel()
	dir, a, b := forkedPair(t, mergeableXML())
	for _, c := range []testkit.DB{a, b} {
		c.CLI(t, "mkdir", "/Спільна")
	}
	a.CLI(t, "add", "-u", "a", "/Спільна/З першої")
	b.CLI(t, "add", "-u", "b", "/Спільна/З другої")

	count := func(seen seenByKeePassXC) (groups int) {
		for _, path := range seen.groups {
			if path == "/Спільна" {
				groups++
			}
		}
		return groups
	}
	if got := count(seenBy(t, mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), a, b))); got != 2 {
		t.Errorf("the real merge gave %d such groups: this difference may be gone", got)
	}
	ours := seenBy(t, mergedByCleanpass(t, dir, a, b))
	if got := count(ours); got != 1 {
		t.Errorf("the result has %d such groups, want 1", got)
	}
	inTheGroup := 0
	for _, main := range ours.mains {
		if main["group"] == "/Спільна" {
			inTheGroup++
		}
	}
	if inTheGroup != 2 {
		t.Errorf("the group holds %d entries, want the entry of each copy", inTheGroup)
	}
}

// A group has no question of its own, and a move to the bin must not take a group away that a copy
// still holds outside. So an empty group stays outside. The real merge takes the move.
func TestDifferenceAnEmptyGroupStaysOutOfTheBin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := testkit.DB{Path: filepath.Join(dir, "base.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	base.CLI(t, "mkdir", "/Порожня")
	a, b := copyOf(t, base, "a.kdbx"), copyOf(t, base, "b.kdbx")
	nextSecond(t, b)
	b.CLI(t, "rmdir", "/Порожня")

	theirs := seenBy(t, mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), a, b))
	if !slices.Contains(theirs.groups, richBinGroup+"/Порожня") {
		t.Errorf("the real merge has the groups %q: this difference may be gone", theirs.groups)
	}
	ours := seenBy(t, mergedByCleanpass(t, dir, a, b))
	if !slices.Contains(ours.groups, "/Порожня") {
		t.Errorf("the result has the groups %q", ours.groups)
	}
}

// The real merge keeps the order of a copy. Here entries lie by name, so that the order of the
// copies has no say in the result.
func TestDifferenceEntriesLieByName(t *testing.T) {
	t.Parallel()
	dir, a, b := forkedPair(t, mergeableXML())
	for _, c := range []testkit.DB{a, b} {
		c.CLI(t, "mkdir", "/Порядок")
	}
	a.CLI(t, "add", "-u", "x", "/Порядок/Я перший")
	a.CLI(t, "add", "-u", "x", "/Порядок/А другий")

	listing := func(db testkit.DB) string { return db.CLI(t, "ls", "/Порядок") }
	inOrderOfTheCopy := listing(mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), a, b))
	if strings.Index(inOrderOfTheCopy, "Я перший") > strings.Index(inOrderOfTheCopy, "А другий") {
		t.Errorf("the real merge changed the order: this difference may be gone:\n%s", inOrderOfTheCopy)
	}
	byName := listing(mergedByCleanpass(t, dir, a, b))
	if strings.Index(byName, "А другий") > strings.Index(byName, "Я перший") {
		t.Errorf("the result does not lie by name:\n%s", byName)
	}
}

// The real merge takes reads from the version it keeps. Here it is the maximum over all copies.
func TestDifferenceTheMaximumOfReadsCounts(t *testing.T) {
	t.Parallel()
	const entry = "<UsageCount>4</UsageCount>"
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, replaced(t, mergeableXML(), entry, "<UsageCount>9</UsageCount>"))

	theirs := seenBy(t, mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), a, b)).mains[richWorkEntryID]["usage count"]
	if theirs != strconv.Itoa(4) {
		t.Errorf("the real merge has the count %q: this difference may be gone", theirs)
	}
	ours := seenBy(t, mergedByCleanpass(t, dir, a, b)).mains[richWorkEntryID]["usage count"]
	if ours != strconv.Itoa(9) {
		t.Errorf("the result has the count %q, want 9", ours)
	}
}

// A copy moved an entry away and back, so only the time of its place is new, and the other copy has
// edited it later. The real merge keeps the old time of the place when the group is the same.
func TestDifferenceTheNewestMoveCountsAlsoInTheSameGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, replaced(t, mergeableXML(),
		"SECRET-MARKER-друге", divergedSecret,
		"<LastModificationTime>2026-03-01T10:00:03Z<", "<LastModificationTime>2026-05-01T10:00:00Z<"))
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, replaced(t, mergeableXML(),
		"<LocationChanged>2026-02-01T10:00:05Z<", "<LocationChanged>2026-04-01T10:00:05Z<"))

	theirs := seenBy(t, mergedByKeePassXC(t, filepath.Join(dir, "keepassxc.kdbx"), a, b)).mains[richWorkEntryID]
	ours := seenBy(t, mergedByCleanpass(t, dir, a, b)).mains[richWorkEntryID]

	if ours["group"] != richWorkGroup || theirs["group"] != richWorkGroup {
		t.Errorf("the entry lies in %q and in %q, want %s in both", ours["group"], theirs["group"], richWorkGroup)
	}
	if momentOf(t, ours["location changed"]) <= momentOf(t, theirs["location changed"]) {
		t.Errorf("the time of the move: %q here, %q in the real merge: this difference may be gone", ours["location changed"], theirs["location changed"])
	}
}

// Two copies moved two groups under each other. The real merge never ends and takes a whole
// processor, seen on 2.7.12. Here the result is a valid tree.
func TestDifferenceACycleOfGroupsStopsOnlyTheRealMerge(t *testing.T) {
	t.Parallel()
	const hang = 20 * time.Second
	group := func(id, name, location, inside string) string {
		return `<Group><UUID>` + id + `</UUID><Name>` + name + `</Name><Times><LastModificationTime>2026-01-01T10:00:00Z</LastModificationTime><LocationChanged>` + location + `</LocationChanged></Times><IsExpanded>True</IsExpanded><CustomData><Item><Key>kdbx-4</Key><Value>x</Value></Item></CustomData>` + inside + `</Group>`
	}
	const first, second = "AAAAAAAAAAAAAAAAAAAAQA==", "AAAAAAAAAAAAAAAAAAAAUA=="
	database := func(inside string) string {
		return `<?xml version="1.0" encoding="UTF-8"?><KeePassFile><Meta><Generator>KeePassXC</Generator></Meta><Root>` + group("AAAAAAAAAAAAAAAAAAAAEA==", "Root", "2026-01-01T10:00:00Z", inside) + `</Root></KeePassFile>`
	}
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t,
		database(group(first, "Перша", "2026-01-01T10:00:00Z", group(second, "Друга", "2026-02-01T10:00:00Z", ""))))
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t,
		database(group(second, "Друга", "2026-01-01T10:00:00Z", group(first, "Перша", "2026-02-02T10:00:00Z", ""))))

	target := copyOf(t, a, "keepassxc.kdbx")
	if target.CLIWithin(t, hang, "merge", "--same-credentials", b.Path) {
		t.Error("the real merge has ended: this difference may be gone")
	}

	// The real KeePassXC reads the result, so the tree is valid. Each group has to be in it once.
	names := map[string]int{}
	for _, path := range seenBy(t, mergedByCleanpass(t, dir, a, b)).groups {
		names[path[strings.LastIndex(path, "/")+1:]]++
	}
	if names["Перша"] != 1 || names["Друга"] != 1 {
		t.Errorf("the result has the groups %v, want each group once", names)
	}
}

// A true conflict at least 24 hours apart asks nothing here either, and the rule is the same.
func TestNoDifferenceInAConflictFarApart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, replaced(t, mergeableXML(),
		"SECRET-MARKER-друге", secretMarker+"-старша",
		"<LastModificationTime>2026-03-01T10:00:03Z<", "<LastModificationTime>2026-04-01T10:00:00Z<"))
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, replaced(t, mergeableXML(),
		"SECRET-MARKER-друге", secretMarker+"-новіша",
		"<LastModificationTime>2026-03-01T10:00:03Z<", "<LastModificationTime>2026-05-01T10:00:00Z<"))

	compareWithBothOrders(t, dir, map[string]int{}, a, b)
}

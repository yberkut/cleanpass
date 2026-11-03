package main_test

import (
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

const (
	groupsSection     = "Групи"
	rememberedSection = "Записи, які ви вже злили раніше"
)

// groupXML is a group with this UUID, name, time of its last move and content.
func groupXML(id, name, moved, content string) string {
	return "<Group><UUID>" + id + "</UUID><Name>" + name + "</Name><Times><LocationChanged>" + moved + "</LocationChanged></Times>" + content + "</Group>"
}

func withGroup(groups ...string) string {
	return strings.Replace(testkit.PlainXML, "</Entry>", "</Entry>"+strings.Join(groups, ""), 1)
}

func TestCompareSaysWhichGroupsWillJoin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withGroup(groupXML("AAAAAAAAAAAAAAAAAAADAA==", "Робота", v1, "")))
	b := rawCopy(t, dir, "b.kdbx", withGroup(groupXML("AAAAAAAAAAAAAAAAAAADAQ==", "Робота", v1, "")))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	wantLines(t, section(t, got.Stdout, groupsSection), "  Робота: в одну групу з нею об'єднається ще груп: 1 (однаковий шлях або кошик)")
}

// One group made of the two would lose the notes of one, so both stay, and the report says so.
func TestCompareSaysWhichGroupsOnOnePathStayApart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withGroup(groupXML("AAAAAAAAAAAAAAAAAAADAA==", "Робота", v1, "<Notes>перші</Notes>")))
	b := rawCopy(t, dir, "b.kdbx", withGroup(groupXML("AAAAAAAAAAAAAAAAAAADAQ==", "Робота", v1, "<Notes>другі</Notes>")))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	wantLines(t, section(t, got.Stdout, groupsSection),
		"  Робота: на тому самому шляху лишиться окремо ще груп: 1, бо одна група загубила б нотатки, значок, CustomData або інше поле")
}

func TestCompareSaysWhichGroupLeavesACycle(t *testing.T) {
	t.Parallel()
	const first, second = "AAAAAAAAAAAAAAAAAAADAA==", "AAAAAAAAAAAAAAAAAAADAQ=="
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withGroup(groupXML(first, "Перша", v1, groupXML(second, "Друга", v2, ""))))
	b := rawCopy(t, dir, "b.kdbx", withGroup(groupXML(second, "Друга", v1, groupXML(first, "Перша", v3, ""))))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	lines := section(t, got.Stdout, groupsSection)
	if !strings.Contains(strings.Join(lines, "\n"), ": рухи в різних копіях зациклили групи, тому ця група стане під кореневу") {
		t.Errorf("no word about the cycle:\n%s", got.Stdout)
	}
}

// memoryItem gives one decision of an earlier merge, as the custom data of the database holds it.
func memoryItem(xml, key string) string {
	item := "<Item><Key>cleanpass/" + key + "</Key><Value>2026-03-01T10:00:00Z</Value></Item>"
	return strings.Replace(xml, "</Meta>", "<CustomData>"+item+"</CustomData></Meta>", 1)
}

const (
	// plainEntryID is the entry of PlainXML in the form a decision names it.
	plainEntryID = "00000000000000000000000000000100"
	// plainEntryUUID is the same entry in the form the file holds it.
	plainEntryUUID = "AAAAAAAAAAAAAAAAAAABAA=="
)

// withRecord gives PlainXML with a record about the deletion of its entry.
func withRecord(stamp string) string {
	return strings.Replace(testkit.PlainXML, "<DeletedObject><UUID>AAAAAAAAAAAAAAAAAAAHdw==</UUID><DeletionTime>2026-02-10T10:00:00Z<",
		"<DeletedObject><UUID>"+plainEntryUUID+"</UUID><DeletionTime>"+stamp+"<", 1)
}

func TestCompareToldTheAnswerAboutADeletionFromAnEarlierMerge(t *testing.T) {
	t.Parallel()
	const deleted, changed = "2026-03-20T10:00:00Z", "2026-03-15T10:00:00Z"
	for _, tc := range []struct {
		name, key string
		want      string
	}{
		{"the human kept the entry", "rejected/" + plainEntryID + "/" + deleted, "    Питання не буде: ви вже вирішили лишити цей запис."},
		{"the human deleted the entry", "accepted/" + plainEntryID + "/" + changed, "    Питання не буде: ви вже вирішили видалити цей запис."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := rawCopy(t, dir, "a.kdbx", memoryItem(withRecord(deleted), tc.key))
			b := rawCopy(t, dir, "b.kdbx", testkit.PlainXML)

			got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

			wantCode(t, got, 0)
			lines := section(t, got.Stdout, deletionSection)
			wantLines(t, lines, tc.want)
			wantNoLine(t, strings.Join(lines, "\n"), "    Під час злиття буде питання, а без відповіді запис лишиться.")
		})
	}
}

func TestCompareAsksAboutADeletionWithoutAMemoryOfIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withRecord("2026-03-20T10:00:00Z"))
	b := rawCopy(t, dir, "b.kdbx", testkit.PlainXML)

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantLines(t, section(t, got.Stdout, deletionSection), "    Під час злиття буде питання, а без відповіді запис лишиться.")
}

// The human has said these two are one entry, so the report does not offer them as a pair again.
func TestCompareAsksNothingAboutAPairAnEarlierMergeMadeOne(t *testing.T) {
	t.Parallel()
	first := fields("Title", "Пошта", "UserName", "я")
	second := fields("Title", "Пошта", "UserName", "я", "Notes", "інше")
	const smaller, larger = "00000000000000000000000000000200", "00000000000000000000000000000201"
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", memoryItem(withEntries(first, second), "entries/"+smaller+"/"+larger))
	b := rawCopy(t, dir, "b.kdbx", withEntries(first, second))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	wantNoSection(t, got.Stdout, pairsSection)

	// Without the memory the same copies raise the question.
	wantLines(t, section(t, testkit.Run(t, passwords(2), "compare", b.Path, b.Path).Stdout, pairsSection), "    Спільне: назва і логін; пароль однаковий")
}

// The memory has made one entry of two, and an old copy still holds the second one by itself. The
// report gives the reason, and it does not say the old copy lacks the entry.
func TestCompareSaysAnOldCopyStillHoldsAnEntryAnEarlierMergeTookIn(t *testing.T) {
	t.Parallel()
	const smaller, larger = "00000000000000000000000000000200", "00000000000000000000000000000201"
	version := func(id, modified, password string) string {
		return "<UUID>" + id + "</UUID><Times><LastModificationTime>" + modified + "</LastModificationTime></Times>" +
			fields("Title", "Пошта", "UserName", "я", "Notes", password)
	}
	replaceEntry := func(xml string) string {
		start := strings.Index(testkit.PlainXML, "<Entry>")
		end := strings.Index(testkit.PlainXML, "</Entry>") + len("</Entry>")
		return testkit.PlainXML[:start] + xml + testkit.PlainXML[end:]
	}
	const firstUUID, secondUUID = "AAAAAAAAAAAAAAAAAAACAA==", "AAAAAAAAAAAAAAAAAAACAQ=="
	merged := "<Entry>" + version(firstUUID, v2, "нове") + "<History><Entry>" + version(firstUUID, v1, "старе") + "</Entry></History></Entry>"
	dir := t.TempDir()
	result := rawCopy(t, dir, "result.kdbx", memoryItem(replaceEntry(merged), "entries/"+smaller+"/"+larger))
	old := rawCopy(t, dir, "old.kdbx", replaceEntry("<Entry>"+version(secondUUID, v1, "старе")+"</Entry>"))

	got := testkit.Run(t, passwords(2), "compare", result.Path, old.Path)

	wantCode(t, got, 0)
	lines := section(t, got.Stdout, rememberedSection)
	wantLines(t, lines, "    Раніше сюди злито записів: 1. Під старим UUID вони ще є в: "+old.Path)
	wantNoSection(t, got.Stdout, onlyInSomeCopies)
	if !strings.Contains(strings.Join(section(t, got.Stdout, copyLags), "\n"), old.Path+":") {
		t.Errorf("no line about the old copy:\n%s", got.Stdout)
	}
	if strings.Contains(got.Stdout, "запису немає") {
		t.Errorf("the report says a copy lacks the entry:\n%s", got.Stdout)
	}
}

// The human has seen this anomaly and let the version stay on top, so no question comes. The
// report still says what is unusual about the entry.
func TestCompareSaysTheClockAnomalyIsDecided(t *testing.T) {
	t.Parallel()
	key := "replaced/" + plainEntryID + "/" + v3 + "/" + v2
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", memoryItem(withVersions(v2, v3), key))
	b := rawCopy(t, dir, "b.kdbx", withVersions(v3))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	wantLines(t, section(t, got.Stdout, copyLags),
		"    Збитий годинник: найновіша за часом версія вже замінена в іншій копії, але ви вже вирішили лишити її головною. Питання не буде.")
}

package main_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

// section gives the lines of the report after the header that starts with this text, up to the
// next empty line.
func section(t *testing.T, stdout, start string) []string {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(stdout, "\r", ""), "\n")
	at := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, start) })
	if at < 0 {
		t.Fatalf("no section %q in the report:\n%s", start, stdout)
	}
	body := lines[at+1:]
	if end := slices.Index(body, ""); end >= 0 {
		body = body[:end]
	}
	return body
}

func wantNoSection(t *testing.T, stdout, start string) {
	t.Helper()
	for line := range strings.SplitSeq(stdout, "\n") {
		if strings.HasPrefix(line, start) {
			t.Errorf("there should be no section %q in the report:\n%s", start, stdout)
		}
	}
}

func wantLines(t *testing.T, lines []string, want ...string) {
	t.Helper()
	for _, line := range want {
		if !slices.Contains(lines, line) {
			t.Errorf("no line %q in:\n%s", line, strings.Join(lines, "\n"))
		}
	}
}

// The headers of the sections of the report.
const (
	onlyInSomeCopies = "Записи лише в частині копій"
	copyLags         = "Копія відстає"
	versionsDispute  = "Версії сперечаються"
)

func TestCompareNamesEntriesThatOnlyPartOfTheCopiesHold(t *testing.T) {
	dir := t.TempDir()
	a := newCopy(t, dir, "a.kdbx", testkit.RichXML())
	b := copyOf(t, a, "b.kdbx")
	b.CLI(t, "add", "--username", "новий", "/Новий запис")

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	lines := section(t, got.Stdout, onlyInSomeCopies)
	wantLines(t, lines, "  Новий запис (логін: новий)", "    "+a.Path+": запису немає")
	if !slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, "    "+b.Path+": змінено 20") }) {
		t.Errorf("no date for the copy that holds the entry:\n%s", strings.Join(lines, "\n"))
	}
}

func TestCompareShowsTheFieldsAndDatesOfAnEntryInACopyThatLags(t *testing.T) {
	dir := t.TempDir()
	old := rawCopy(t, dir, "old.kdbx", withVersions(v1))
	newer := rawCopy(t, dir, "newer.kdbx", withVersions(v2, v1))

	got := testkit.Run(t, passwords(2), "compare", old.Path, newer.Path)

	wantCode(t, got, 0)
	wantLines(t, section(t, got.Stdout, copyLags),
		"  Запис",
		"    "+old.Path+": змінено "+localTime(t, v1)+"; ця версія є в історії іншої копії; відрізняється від версії, що стане головною: нотатки різні",
		"    "+newer.Path+": змінено "+localTime(t, v2)+"; цієї версії немає в історії інших копій; вміст як у версії, що стане головною",
	)
	wantNoSection(t, got.Stdout, versionsDispute)
	wantNoSection(t, got.Stdout, onlyInSomeCopies)
}

// The real KeePassXC makes the edit, so the history is the one a person really gets.
func TestCompareSeesAnEditOfTheRealKeePassXCAsALag(t *testing.T) {
	dir := t.TempDir()
	old := newCopy(t, dir, "old.kdbx", testkit.RichXML())
	edited := copyOf(t, old, "edited.kdbx")
	edited.CLI(t, "edit", "--notes", "нова нотатка", workEntry)

	got := testkit.Run(t, passwords(2), "compare", old.Path, edited.Path)

	lines := section(t, got.Stdout, copyLags)
	wantLines(t, lines,
		"  Друге (логін: u2, сайт: b.example)",
		"    "+old.Path+": змінено "+localTime(t, "2026-03-01T10:00:03Z")+"; ця версія є в історії іншої копії; відрізняється від версії, що стане головною: нотатки різні",
	)
	wantNoSection(t, got.Stdout, versionsDispute)
}

func TestCompareExplainsEachKindOfDisputeBetweenVersions(t *testing.T) {
	future := "2099-01-05T10:00:00Z"
	for _, tc := range []struct {
		name         string
		a, b         string
		aTime, bTime string
		// aMark and bMark tell whether the main version of the copy is in the history of another.
		aMark, bMark string
		reasons      []string
	}{
		{
			name: "a gap of 24 hours or more", a: withVersions(v2, v1), b: withVersions(v5, v1), aTime: v2, bTime: v5,
			aMark: "цієї версії немає в історії інших копій", bMark: "цієї версії немає в історії інших копій",
			reasons: []string{"    Справжній конфлікт: жодна версія не є в історії іншої. Різниця 24 години або більше, тому новіша стане головною, а старша піде в історію."},
		},
		{
			name: "a gap of less than 24 hours", a: withVersions(v2, v1), b: withVersions("2026-01-05T15:00:00Z", v1), aTime: v2, bTime: "2026-01-05T15:00:00Z",
			aMark: "цієї версії немає в історії інших копій", bMark: "цієї версії немає в історії інших копій",
			reasons: []string{"    Справжній конфлікт: жодна версія не є в історії іншої. Різниця менша за 24 години, і годиннику систем не можна вірити. Під час злиття буде питання, а без відповіді новіша стане головною."},
		},
		{
			name: "a version from the future", a: withVersions(v2, v1), b: withVersions(future, v1), aTime: v2, bTime: future,
			aMark: "цієї версії немає в історії інших копій", bMark: "цієї версії немає в історії інших копій",
			reasons: []string{"    Справжній конфлікт: жодна версія не є в історії іншої. Одна з версій має дату з майбутнього. Під час злиття буде питання, а без відповіді новіша стане головною."},
		},
		{
			name: "the clock put a replaced version on top", a: withVersions(v2, v3), b: withVersions(v3), aTime: v2, bTime: v3,
			aMark: "цієї версії немає в історії інших копій", bMark: "ця версія є в історії іншої копії",
			reasons: []string{"    Увага: збитий годинник. Найновіша за часом версія вже замінена в іншій копії, але без відповіді стане головною, і так може повернутись старий пароль. Під час злиття буде питання."},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := rawCopy(t, dir, "a.kdbx", tc.a)
			b := rawCopy(t, dir, "b.kdbx", tc.b)

			got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

			wantCode(t, got, 0)
			lines := section(t, got.Stdout, versionsDispute)
			wantLines(t, lines, tc.reasons...)
			wantLines(t, lines, "  Запис")
			for _, c := range []struct{ path, stamp, mark string }{{a.Path, tc.aTime, tc.aMark}, {b.Path, tc.bTime, tc.bMark}} {
				start := "    " + c.path + ": змінено " + localTime(t, c.stamp) + "; " + c.mark + "; "
				if !slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, start) }) {
					t.Errorf("no line that starts with %q in:\n%s", start, strings.Join(lines, "\n"))
				}
			}
			wantNoSection(t, got.Stdout, copyLags)
		})
	}
}

// entryWith gives PlainXML where the one entry has this time and this content beside its title.
func entryWith(stamp, content string) string {
	const id = "<UUID>AAAAAAAAAAAAAAAAAAABAA==</UUID>"
	start, end := strings.Index(testkit.PlainXML, "<Entry>"), strings.Index(testkit.PlainXML, "</Entry>")
	return testkit.PlainXML[:start] + "<Entry>" + id + "<Times><LastModificationTime>" + stamp + "</LastModificationTime></Times>" +
		"<String><Key>Title</Key><Value>Запис</Value></String>" + content + testkit.PlainXML[end:]
}

func TestCompareNamesWhatDiffersAndNoValue(t *testing.T) {
	str := func(key, value string) string {
		return "<String><Key>" + key + "</Key><Value>" + value + "</Value></String>"
	}
	const other = "SECRET-MARKER-other"
	for _, tc := range []struct {
		name         string
		before, then string
		want         string
	}{
		{"password", str("Password", "SECRET-MARKER-one"), str("Password", "SECRET-MARKER-two"), "пароль різний"},
		{"login", str("UserName", "перший"), str("UserName", "другий"), "логін різний"},
		{"site", str("URL", "a.example"), str("URL", "b.example"), "URL різний"},
		{"notes", str("Notes", other+"-1"), str("Notes", other+"-2"), "нотатки різні"},
		{"own field", str("Код", other+"-1"), str("Код", other+"-2"), "поле «Код» різне"},
		{"a field only one version has", "", str("Код", other), "поле «Код» різне"},
		{"tags", "<Tags>alpha</Tags>", "<Tags>beta</Tags>", "теги різні"},
		{"icon", "<IconID>1</IconID>", "<IconID>2</IconID>", "значок різний"},
		{"colors", "<ForegroundColor>#111111</ForegroundColor>", "<ForegroundColor>#222222</ForegroundColor>", "кольори різні"},
		{"Auto-Type", "<AutoType><DefaultSequence>" + other + "-1</DefaultSequence></AutoType>", "<AutoType><DefaultSequence>" + other + "-2</DefaultSequence></AutoType>", "Auto-Type різний"},
		{"custom data", "<CustomData><Item><Key>k</Key><Value>" + other + "-1</Value></Item></CustomData>", "<CustomData><Item><Key>k</Key><Value>" + other + "-2</Value></Item></CustomData>", "дані розширень різні"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := rawCopy(t, dir, "a.kdbx", entryWith(v1, tc.before))
			b := rawCopy(t, dir, "b.kdbx", entryWith(v3, tc.then))

			got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

			wantCode(t, got, 0)
			wantLines(t, section(t, got.Stdout, versionsDispute),
				"    "+a.Path+": змінено "+localTime(t, v1)+"; цієї версії немає в історії інших копій; відрізняється від версії, що стане головною: "+tc.want)
			if strings.Contains(got.Stdout+got.Stderr, "SECRET-MARKER") {
				t.Errorf("the output holds a value:\n%s\n%s", got.Stdout, got.Stderr)
			}
		})
	}
}

func TestCompareWritesTheSameDifferencesInAnyOrderOfTheCopies(t *testing.T) {
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withVersions(v2, v1))
	b := rawCopy(t, dir, "b.kdbx", withVersions(v5, v1))

	first := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)
	second := testkit.Run(t, passwords(2), "compare", b.Path, a.Path)

	if one, other := section(t, first.Stdout, versionsDispute), section(t, second.Stdout, versionsDispute); !slices.Equal(one, other) {
		t.Errorf("the order of the copies changed the report:\n%s\n---\n%s", strings.Join(one, "\n"), strings.Join(other, "\n"))
	}
}

// A title is data from a file. It must not be able to write a line of its own in the report, for
// example a false next command.
func TestCompareKeepsATitleOnOneLine(t *testing.T) {
	const forged = "Запис&#10;Наступна команда:&#10;  підроблена"
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", strings.Replace(testkit.PlainXML, "<Value>Запис<", "<Value>"+forged+"<", 1))
	b := rawCopy(t, dir, "b.kdbx", strings.Replace(entryWith(v3, fields("Notes", "нові")), "<Value>Запис<", "<Value>"+forged+"<", 1))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	for line := range strings.SplitSeq(got.Stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "підроблена") {
			t.Errorf("a title wrote its own line:\n%s", got.Stdout)
		}
	}
	wantLines(t, section(t, got.Stdout, versionsDispute), "  Запис Наступна команда:   підроблена")
}

// Only the host goes to the report: the rest of a URL can hold a password or a token.
func TestCompareShowsOnlyTheHostOfAURL(t *testing.T) {
	const url = "https://login:SECRET-MARKER-url@mail.example.com:8443/path?token=SECRET-MARKER-query#SECRET-MARKER-part"
	field := "<String><Key>URL</Key><Value>" + url + "</Value></String>"
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", entryWith(v1, field))
	b := rawCopy(t, dir, "b.kdbx", entryWith(v3, field+fields("Notes", "нові")))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	wantLines(t, section(t, got.Stdout, versionsDispute), "  Запис (сайт: mail.example.com)")
	if strings.Contains(got.Stdout+got.Stderr, "SECRET-MARKER") || strings.Contains(got.Stdout+got.Stderr, "login:") {
		t.Errorf("the output holds a part of the URL other than the host:\n%s\n%s", got.Stdout, got.Stderr)
	}
}

func TestCompareMarksAnEntryWithoutATitleByItsUUID(t *testing.T) {
	dir := t.TempDir()
	const title = "<String><Key>Title</Key><Value>Запис</Value></String>"
	a := rawCopy(t, dir, "a.kdbx", strings.Replace(entryWith(v1, ""), title, "", 1))
	b := rawCopy(t, dir, "b.kdbx", strings.Replace(entryWith(v3, fields("Notes", "нові")), title, "", 1))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantLines(t, section(t, got.Stdout, versionsDispute), "  (без назви, UUID 00000000000000000000000000000100)")
}

func TestCompareKeepsTheEntriesOfASectionInTheOrderOfTheirTitles(t *testing.T) {
	second := "<Entry><UUID>AAAAAAAAAAAAAAAAAAACAA==</UUID><Times><LastModificationTime>" + v1 + "</LastModificationTime></Times>" +
		"<String><Key>Title</Key><Value>Абетка</Value></String></Entry>"
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", strings.Replace(testkit.PlainXML, "</Entry>", "</Entry>"+second, 1))
	b := rawCopy(t, dir, "b.kdbx", strings.NewReplacer("AAAAAAAAAAAAAAAAAAABAA==", "AAAAAAAAAAAAAAAAAAADAA==", "Запис", "Третій").Replace(testkit.PlainXML))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	lines := section(t, got.Stdout, onlyInSomeCopies)
	var titles []string
	for _, line := range lines {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
			titles = append(titles, strings.TrimSpace(line))
		}
	}
	if !slices.Equal(titles, []string{"Абетка", "Запис", "Третій"}) {
		t.Errorf("titles in the order %q:\n%s", titles, got.Stdout)
	}
}

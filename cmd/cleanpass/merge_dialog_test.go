package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/judge"
	"github.com/yberkut/cleanpass/internal/safewriter"
	"github.com/yberkut/cleanpass/internal/testkit"
)

const (
	questionMark       = "Питання "
	unknownInTheReport = "яких цей звіт не вміє показати"
	// firstEntryUUID and the others are the entries withEntries makes, in the form the file holds them.
	firstEntryUUID  = "AAAAAAAAAAAAAAAAAAACAA=="
	secondEntryUUID = "AAAAAAAAAAAAAAAAAAACAQ=="
	thirdEntryUUID  = "AAAAAAAAAAAAAAAAAAACAg=="
)

// merged runs a merge of raw copies with these lines after the passwords, and it must work.
func merged(t *testing.T, answers string, result testkit.DB, copies ...testkit.DB) testkit.Result {
	t.Helper()
	got := runMerge(t, answers, result, copies...)
	wantMerged(t, got)
	return got
}

func runMerge(t *testing.T, answers string, result testkit.DB, copies ...testkit.DB) testkit.Result {
	t.Helper()
	args := []string{"merge"}
	stdin := ""
	for _, c := range copies {
		args = append(args, c.Path)
		stdin += c.Password + "\n"
	}
	return testkit.Run(t, stdin+answers, append(args, "--output", result.Path)...)
}

func resultIn(dir, name string) testkit.DB {
	return testkit.DB{Path: filepath.Join(dir, name), Password: testkit.Password}
}

func exportOf(t *testing.T, db testkit.DB) string {
	t.Helper()
	return strings.Join(db.ExportLines(t), "\n")
}

// wantAsked checks how many questions the run put to the person. The start of the dialog tells the
// total, and it does not count here.
func wantAsked(t *testing.T, got testkit.Result, want int) {
	t.Helper()
	if count := strings.Count(got.Stderr, questionMark); count != want {
		t.Errorf("%d questions, want %d:\n%s", count, want, got.Stderr)
	}
}

func wantInOutput(t *testing.T, output string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(output, want) {
			t.Errorf("no %q in:\n%s", want, output)
		}
	}
}

func wantNotInOutput(t *testing.T, output string, unwanted ...string) {
	t.Helper()
	for _, one := range unwanted {
		if strings.Contains(output, one) {
			t.Errorf("%q should not be in:\n%s", one, output)
		}
	}
}

// withoutTheEntry gives PlainXML with no entry and a record about the deletion of its entry.
func withoutTheEntry(stamp string) string {
	xml := withRecord(stamp)
	start, end := strings.Index(xml, "<Entry>"), strings.Index(xml, "</Entry>")+len("</Entry>")
	return xml[:start] + xml[end:]
}

const deletedAt = "2026-03-20T10:00:00Z"

func TestMergeAsksAboutADeletionAndNoAnswerKeepsTheEntry(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		answers string
		kept    bool
		// unanswered is the number in the words about questions with no answer.
		unanswered string
	}{
		"delete":                         {"2\n", false, ""},
		"keep":                           {"1\n", true, ""},
		"Enter":                          {"\n", true, "1"},
		"the input ended":                {"", true, "1"},
		"stop asking":                    {"с\n", true, "1"},
		"a wrong answer and then delete": {"7\nтак\n2\n", false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			gone := rawCopy(t, dir, "gone.kdbx", withoutTheEntry(deletedAt))
			held := rawCopy(t, dir, "held.kdbx", testkit.PlainXML)
			result := resultIn(dir, "new.kdbx")

			got := merged(t, tc.answers, result, gone, held)

			if kept := strings.Contains(result.CLI(t, "ls"), "Запис"); kept != tc.kept {
				t.Errorf("the entry stays = %v, want %v\n%s", kept, tc.kept, got.Stderr)
			}
			wantInOutput(t, got.Stderr,
				"Запис видалено в одній копії, а в іншій він є:",
				"Запис",
				"Видалено "+localTime(t, deletedAt)+"; остання зміна вмісту "+localTime(t, "2026-03-15T10:00:00Z"),
				"Запис про видалення: "+gone.Path,
				"Поза кошиком: "+held.Path,
			)
			if tc.unanswered != "" {
				wantLine(t, got.Stdout, "Питань без відповіді: "+tc.unanswered+". Для них діють правила без відповіді, тож жодні дані не зникли.")
			} else {
				wantNotInOutput(t, got.Stdout, "Питань без відповіді")
			}
			wantUkrainian(t, got.Stdout, got.Stderr, gone.Path, held.Path, result.Path, "cleanpass-tests")
		})
	}
}

func TestAnAnswerAboutADeletionHoldsInTheNextMerge(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		answer string
		kept   bool
	}{"delete": {"2\n", false}, "keep": {"1\n", true}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			gone := rawCopy(t, dir, "gone.kdbx", withoutTheEntry(deletedAt))
			held := rawCopy(t, dir, "held.kdbx", testkit.PlainXML)
			first, second := resultIn(dir, "first.kdbx"), resultIn(dir, "second.kdbx")
			merged(t, tc.answer, first, gone, held)

			got := merged(t, "", second, first, held)

			wantAsked(t, got, 0)
			if kept := strings.Contains(second.CLI(t, "ls"), "Запис"); kept != tc.kept {
				t.Errorf("the entry stays = %v, want %v\n%s", kept, tc.kept, got.Stderr)
			}
		})
	}
}

// The answer that deletes is the only way data leaves, so it goes no further than this entry.
func TestAnAnswerAboutADeletionLeavesTheOtherEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, second := fields("Title", "Перший"), fields("Title", "Другий")
	both := withEntries(first, second)
	record := "<DeletedObject><UUID>" + firstEntryUUID + "</UUID><DeletionTime>" + deletedAt + "</DeletionTime></DeletedObject>"
	gone := rawCopy(t, dir, "gone.kdbx", strings.Replace(withEntries("", second), "<DeletedObjects>", "<DeletedObjects>"+record, 1))
	held := rawCopy(t, dir, "held.kdbx", both)
	result := resultIn(dir, "new.kdbx")

	merged(t, "2\n", result, gone, held)

	listing := result.CLI(t, "ls")
	wantNotInOutput(t, listing, "Перший")
	wantInOutput(t, listing, "Другий")
}

// A move to the recycle bin is also a deletion. No copy has removed the entry here, so it stays in
// the bin with all its versions.
func TestAnEntryOnlyInTheBinStaysThereAfterDelete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	held := newCopy(t, dir, "held.kdbx", mergeableXML())
	recycled := copyOf(t, held, "recycled.kdbx")
	recycled.CLI(t, "rm", richWorkEntry)
	result := resultIn(dir, "new.kdbx")

	got := merged(t, "2\n", result, held, recycled)

	wantInOutput(t, got.Stderr, "У кошику: "+recycled.Path, "видалити запис: він лишиться в кошику з усіма версіями")
	listing := result.CLI(t, "ls", "-R", "-f")
	wantInOutput(t, listing, "Кошик/Друге")
	wantNotInOutput(t, listing, "Робота/Друге")
}

const (
	nearOne = "2026-01-05T10:00:00Z"
	nearTwo = "2026-01-05T12:00:00Z"
)

// entryWith gives PlainXML with one entry, with one version of this time and these fields.
func entryOf(stamp string, pairs ...string) string {
	entry := "<Entry><UUID>" + plainEntryUUID + "</UUID><Times><LastModificationTime>" + stamp + "</LastModificationTime></Times>" + fields(pairs...) + "</Entry>"
	start, end := strings.Index(testkit.PlainXML, "<Entry>"), strings.Index(testkit.PlainXML, "</Entry>")+len("</Entry>")
	return testkit.PlainXML[:start] + entry + testkit.PlainXML[end:]
}

func notesOf(t *testing.T, db testkit.DB, entry string) string {
	t.Helper()
	return strings.TrimSpace(db.CLI(t, "show", "-a", "Notes", entry))
}

func TestMergeShowsBothVersionsOfAConflictAndTheHumanChooses(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		answers string
		// main is the notes of the main version in the result.
		main string
	}{
		"Enter goes by the rule": {"\n", nearTwo},
		"the input ended":        {"", nearTwo},
		"the newer one":          {"1\n", nearTwo},
		"the older one":          {"2\n", nearOne},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			a := rawCopy(t, dir, "a.kdbx", withVersions(nearOne, v1))
			b := rawCopy(t, dir, "b.kdbx", withVersions(nearTwo, v1))
			result := resultIn(dir, "new.kdbx")

			got := merged(t, tc.answers, result, a, b)

			if notes := notesOf(t, result, "/Запис"); notes != tc.main {
				t.Errorf("the main version holds %q, want %q\n%s", notes, tc.main, got.Stderr)
			}
			export := exportOf(t, result)
			wantInOutput(t, export, v1, nearOne, nearTwo)
			wantInOutput(t, got.Stderr,
				"Версії запису сперечаються:",
				"Різниця менша за 24 години",
				"1) змінено "+localTime(t, nearTwo)+"; головна в: "+b.Path+" — так буде і без відповіді",
				"2) змінено "+localTime(t, nearOne)+"; головна в: "+a.Path,
				"відрізняється від версії 1: нотатки різні",
			)
			wantUkrainian(t, got.Stdout, got.Stderr, a.Path, b.Path, result.Path, "cleanpass-tests")
		})
	}
}

// All copies hold a version of their own here, so each stays beside the new one the answer makes.
func TestChoosingAnOlderVersionHoldsInTheNextMergeWithAnOldCopy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withVersions(nearOne, v1))
	b := rawCopy(t, dir, "b.kdbx", withVersions(nearTwo, v1))
	first, second := resultIn(dir, "first.kdbx"), resultIn(dir, "second.kdbx")
	merged(t, "2\n", first, a, b)

	got := merged(t, "", second, first, b)

	wantAsked(t, got, 0)
	if notes := notesOf(t, second, "/Запис"); notes != nearOne {
		t.Errorf("the main version holds %q, want the choice %q", notes, nearOne)
	}
}

func TestAConflictOfThreeCopiesOffersEachVersion(t *testing.T) {
	t.Parallel()
	const nearThree = "2026-01-05T14:00:00Z"
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withVersions(nearOne, v1))
	b := rawCopy(t, dir, "b.kdbx", withVersions(nearTwo, v1))
	c := rawCopy(t, dir, "c.kdbx", withVersions(nearThree, v1))
	result := resultIn(dir, "new.kdbx")

	got := merged(t, "3\n", result, a, b, c)

	wantInOutput(t, got.Stderr, "1) змінено "+localTime(t, nearThree), "2) змінено "+localTime(t, nearTwo), "3) змінено "+localTime(t, nearOne))
	if notes := notesOf(t, result, "/Запис"); notes != nearOne {
		t.Errorf("the main version holds %q, want %q", notes, nearOne)
	}
	wantInOutput(t, exportOf(t, result), v1, nearOne, nearTwo, nearThree)
}

// A gap of 24 hours or more goes by the rule with no question.
func TestAConflictFarApartAsksNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withVersions(v2, v1))
	b := rawCopy(t, dir, "b.kdbx", withVersions(v3, v1))
	result := resultIn(dir, "new.kdbx")

	got := merged(t, "", result, a, b)

	wantAsked(t, got, 0)
	if notes := notesOf(t, result, "/Запис"); notes != v3 {
		t.Errorf("the main version holds %q, want %q", notes, v3)
	}
}

// similarEntries gives copies that hold these entries, each in a copy of its own place: the first
// copy holds the first entry and so on. All of them have a similar title and login.
func similarEntries(t *testing.T, dir string, notes ...string) []testkit.DB {
	t.Helper()
	copies := make([]testkit.DB, 0, len(notes))
	for i, note := range notes {
		content := make([]string, i+1)
		content[i] = fields("Title", "Пошта", "UserName", "я", "Notes", note)
		copies = append(copies, rawCopy(t, dir, string(rune('a'+i))+".kdbx", withEntries(content...)))
	}
	return copies
}

func TestMergeAsksAboutASimilarPair(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		answers    string
		bothStay   bool
		askedAgain bool
	}{
		"merge":           {"2\n", false, false},
		"keep both":       {"1\n", true, false},
		"Enter":           {"\n", true, true},
		"the input ended": {"", true, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			copies := similarEntries(t, dir, "перша нотатка", "друга нотатка")
			first, second := resultIn(dir, "first.kdbx"), resultIn(dir, "second.kdbx")

			got := merged(t, tc.answers, first, copies...)

			wantInOutput(t, got.Stderr, "Схожі записи з різними UUID:", "Спільне: назва і логін; пароль однаковий", "Відрізняється: нотатки різні")
			export := exportOf(t, first)
			wantInOutput(t, export, firstEntryUUID, "перша нотатка", "друга нотатка")
			if stays := strings.Contains(export, secondEntryUUID); stays != tc.bothStay {
				t.Errorf("the second entry stays = %v, want %v", stays, tc.bothStay)
			}
			wantNotInOutput(t, got.Stdout, unknownInTheReport)

			again := merged(t, "", second, first, copies[1])
			if asked := strings.Contains(again.Stderr, "Схожі записи"); asked != tc.askedAgain {
				t.Errorf("the next merge asks = %v, want %v:\n%s", asked, tc.askedAgain, again.Stderr)
			}
		})
	}
}

func TestOneAnswerMergesAllPairsWithTheSameContentAndComesFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	same := fields("Title", "Пошта", "UserName", "я", "Notes", "та сама нотатка")
	other := fields("Title", "Банк", "UserName", "ти", "Notes", "інша нотатка")
	otherToo := fields("Title", "Банк", "UserName", "ти", "Notes", "ще інша нотатка")
	a := rawCopy(t, dir, "a.kdbx", withEntries(same))
	b := rawCopy(t, dir, "b.kdbx", withEntries("", same))
	c := rawCopy(t, dir, "c.kdbx", withEntries("", "", same, other))
	d := rawCopy(t, dir, "d.kdbx", withEntries("", "", "", "", otherToo))
	result := resultIn(dir, "new.kdbx")

	// The three pairs of the same content go with one answer, and the pair of Банк stays open.
	got := merged(t, "2\n1\n", result, a, b, c, d)

	wantAsked(t, got, 2)
	bulk, single := strings.Index(got.Stderr, "Пар схожих записів з однаковим вмістом: 3."), strings.Index(got.Stderr, "Схожі записи з різними UUID:")
	if bulk < 0 || single < 0 || bulk > single {
		t.Errorf("the answer about all pairs must come first:\n%s", got.Stderr)
	}
	export := exportOf(t, result)
	wantInOutput(t, export, firstEntryUUID, "інша нотатка", "ще інша нотатка")
	wantNotInOutput(t, export, secondEntryUUID, thirdEntryUUID)
	wantNotInOutput(t, got.Stderr, "Питань без відповіді")
}

// A family of three gives three questions. Two answers settle the third one in each of these cases.
func TestAQuestionTheAnswersHaveSettledIsNotAsked(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		answers string
		// stays names the entries that stay in the result.
		stays, gone []string
	}{
		"both merge with the first":                               {"2\n2\n", []string{firstEntryUUID}, []string{secondEntryUUID, thirdEntryUUID}},
		"the first two stay apart, the third goes with the first": {"1\n2\n", []string{firstEntryUUID, secondEntryUUID}, []string{thirdEntryUUID}},
		"the first two merge, the third stays apart":              {"2\n1\n", []string{firstEntryUUID, thirdEntryUUID}, []string{secondEntryUUID}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			result := resultIn(dir, "new.kdbx")

			got := merged(t, tc.answers, result, similarEntries(t, dir, "один", "два", "три")...)

			wantAsked(t, got, 2)
			export := exportOf(t, result)
			wantInOutput(t, export, tc.stays...)
			wantNotInOutput(t, export, tc.gone...)
			wantInOutput(t, export, "один", "два", "три")
			wantNotInOutput(t, got.Stdout, "Питань без відповіді", "Не злито")
		})
	}
}

func TestTheHumanCanStopAndTheRulesApplyToTheRest(t *testing.T) {
	t.Parallel()
	for name, stop := range map[string]string{"Cyrillic": "с", "Latin": "c", "word": "стоп"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			result := resultIn(dir, "new.kdbx")
			copies := similarEntries(t, dir, "один", "два", "три", "чотири")

			got := merged(t, "2\n"+stop+"\n5\n", result, copies...)

			wantAsked(t, got, 2)
			wantLine(t, got.Stdout, "Питань без відповіді: 5. Для них діють правила без відповіді, тож жодні дані не зникли.")
			export := exportOf(t, result)
			wantInOutput(t, export, firstEntryUUID, thirdEntryUUID, "один", "два", "три", "чотири")
			wantNotInOutput(t, export, secondEntryUUID)
		})
	}
}

// The human keeps two entries apart in one merge. Then in the next one a question about the same
// content cannot join them through a third entry, and the report must say so.
func TestTheReportSaysWhichPairDidNotMerge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	same := fields("Title", "Пошта", "UserName", "я", "Notes", "та сама нотатка")
	a := rawCopy(t, dir, "a.kdbx", withEntries(same))
	b := rawCopy(t, dir, "b.kdbx", withEntries("", same))
	c := rawCopy(t, dir, "c.kdbx", withEntries("", "", same))
	first, second := resultIn(dir, "first.kdbx"), resultIn(dir, "second.kdbx")
	// Each pair on its own, the first two stay apart, and then the person stops.
	merged(t, "1\n1\nс\n", first, a, b, c)

	got := merged(t, "2\n", second, first)

	wantInOutput(t, got.Stdout, "Не злито:", "один запис із двох інших, які ви лишили окремо")
	wantNotInOutput(t, got.Stdout, unknownInTheReport)
	wantUkrainian(t, got.Stdout, got.Stderr, first.Path, second.Path, "cleanpass-tests")
}

// marked gives the fields of a version with a password and notes that hold this marker.
func marked(marker string) []string {
	return []string{"Title", "Запис", "UserName", "я", "Password", secretMarker + "-пароль-" + marker, "Notes", secretMarker + "-нотатки-" + marker}
}

func TestOneSecretValueShowsOnlyOnAnExplicitRequest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", entryOf(nearOne, marked("один")...))
	b := rawCopy(t, dir, "b.kdbx", entryOf(nearTwo, marked("два")...))
	result := resultIn(dir, "new.kdbx")

	// Version 1 is the newer one, b. The list goes: its password, its notes, then the same of a.
	got := merged(t, "п\n3\n\n", result, a, b)

	wantInOutput(t, got.Stderr,
		"Увага: показане значення лишиться в прокрутці термінала",
		"1) версія 1: пароль", "2) версія 1: нотатки", "3) версія 2: пароль", "4) версія 2: нотатки",
		"версія 2, пароль: "+secretMarker+"-пароль-один",
	)
	if warning, value := strings.Index(got.Stderr, "Увага:"), strings.Index(got.Stderr, secretMarker); warning > value {
		t.Errorf("the warning must come before the value:\n%s", got.Stderr)
	}
	if count := strings.Count(got.Stderr, secretMarker); count != 1 {
		t.Errorf("%d values on the screen, want the one that was asked for:\n%s", count, got.Stderr)
	}
	wantNotInOutput(t, got.Stdout, secretMarker)
	wantNoSecretInFiles(t, dir)
}

func TestNoSecretValueShowsWithoutARequest(t *testing.T) {
	t.Parallel()
	for name, answers := range map[string]string{
		"no answer":                     "\n",
		"an answer":                     "2\n",
		"a request without a number":    "п\n\n\n",
		"a request with no such number": "п\n9\n\n",
		"a request with another word":   "п\nтак\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			a := rawCopy(t, dir, "a.kdbx", entryOf(nearOne, marked("один")...))
			b := rawCopy(t, dir, "b.kdbx", entryOf(nearTwo, marked("два")...))

			got := merged(t, answers, resultIn(dir, "new.kdbx"), a, b)

			wantNotInOutput(t, got.Stdout+got.Stderr, secretMarker)
			wantNoSecretInFiles(t, dir)
		})
	}
}

func TestAValueOfAPairOrADeletionCanBeShownToo(t *testing.T) {
	t.Parallel()
	t.Run("a pair", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := rawCopy(t, dir, "a.kdbx", withEntries(fields(marked("один")...)))
		b := rawCopy(t, dir, "b.kdbx", withEntries("", fields(marked("два")...)))

		got := merged(t, "п\n3\n\n", resultIn(dir, "new.kdbx"), a, b)

		wantInOutput(t, got.Stderr, "другий запис, пароль: "+secretMarker+"-пароль-два")
		if count := strings.Count(got.Stderr, secretMarker); count != 1 {
			t.Errorf("%d values on the screen, want one:\n%s", count, got.Stderr)
		}
	})
	t.Run("a deletion", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		gone := rawCopy(t, dir, "gone.kdbx", withoutTheEntry(deletedAt))
		held := rawCopy(t, dir, "held.kdbx", entryOf(nearOne, marked("один")...))

		got := merged(t, "п\n2\n\n", resultIn(dir, "new.kdbx"), gone, held)

		wantInOutput(t, got.Stderr, "запис, нотатки: "+secretMarker+"-нотатки-один")
		if count := strings.Count(got.Stderr, secretMarker); count != 1 {
			t.Errorf("%d values on the screen, want one:\n%s", count, got.Stderr)
		}
	})
}

// A value comes from a file. It must not be able to give orders to the terminal. XML lets through
// the control characters of the upper range, and U+009B starts such an order in a terminal.
func TestAShownValueGivesNoOrdersToTheTerminal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gone := rawCopy(t, dir, "gone.kdbx", withoutTheEntry(deletedAt))
	held := rawCopy(t, dir, "held.kdbx", entryOf(nearOne, "Title", "Запис", "Notes", secretMarker+"-початок\tстовпчик&#x9B;2J-кінець"))

	// The real KeePassXC drops such a character, so the Judge stops this merge. Only the screen counts.
	got := runMerge(t, "п\n1\n\n", resultIn(dir, "new.kdbx"), gone, held)

	wantInOutput(t, got.Stderr, "запис, нотатки: "+secretMarker+"-початок\tстовпчик�2J-кінець")
	if strings.ContainsRune(got.Stderr, '\u009b') {
		t.Errorf("an order to the terminal got out:\n%q", got.Stderr)
	}
}

func TestTheKeysOfTheResultGoByTheAnswer(t *testing.T) {
	t.Parallel()
	const passwordA, passwordB, passwordNew = "пароль копії а", "пароль копії б", "гасло результату"
	// A setting makes this copy the newest and raises no question about an entry.
	newest := replaced(t, mergeableXML(), "<SettingsChanged>2026-02-06T10:00:00Z<", "<SettingsChanged>2099-05-01T10:00:00Z<")
	root := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(root, "key.bin"), 7)
	for name, tc := range map[string]struct {
		answers string
		opens   func(path string) testkit.DB
		says    string
		stops   bool
	}{
		"no answer gives the keys of the newest": {
			"",
			func(path string) testkit.DB { return testkit.DB{Path: path, Password: passwordB} },
			"що й копію", false,
		},
		"Enter gives the keys of the newest": {
			"\n",
			func(path string) testkit.DB { return testkit.DB{Path: path, Password: passwordB} },
			"що й копію", false,
		},
		"another copy": {
			"1\n",
			func(path string) testkit.DB { return testkit.DB{Path: path, Password: passwordA} },
			"що й копію", false,
		},
		"a new password": {
			"3\n" + passwordNew + "\n" + passwordNew + "\n\n\n",
			func(path string) testkit.DB { return testkit.DB{Path: path, Password: passwordNew} },
			"нові ключі", false,
		},
		"a new password after two that differ": {
			"3\nпершого разу\nдругого разу\n" + passwordNew + "\n" + passwordNew + "\n\n\n",
			func(path string) testkit.DB { return testkit.DB{Path: path, Password: passwordNew} },
			"Паролі різні", false,
		},
		"a new key file and no password": {
			"3\n\n" + key + "\n\n",
			func(path string) testkit.DB { return testkit.DB{Path: path, KeyFile: key} },
			"нові ключі", false,
		},
		"a new password and a new key file": {
			"3\n" + passwordNew + "\n" + passwordNew + "\n" + key + "\n\n",
			func(path string) testkit.DB { return testkit.DB{Path: path, Password: passwordNew, KeyFile: key} },
			"нові ключі", false,
		},
		"a key file that is not there, and then a good one": {
			"3\n\nнемає такого файла.key\n" + key + "\n\n",
			func(path string) testkit.DB { return testkit.DB{Path: path, KeyFile: key} },
			"Не вдалося прочитати файл-ключ", false,
		},
		"neither a password nor a key file gets a second try": {
			"3\n\n\n\n" + passwordNew + "\n" + passwordNew + "\n\n\n",
			func(path string) testkit.DB { return testkit.DB{Path: path, Password: passwordNew} },
			"Новому файлу потрібен пароль, файл-ключ або YubiKey", false,
		},
		"a wrong answer": {
			"9\n1\n",
			func(path string) testkit.DB { return testkit.DB{Path: path, Password: passwordA} },
			"Такої відповіді немає", false,
		},
		"the input ends in the middle of new keys": {
			"3\n" + passwordNew + "\n",
			nil,
			"Результат не створено", true,
		},
		"the input ends before the new key file": {
			"3\n" + passwordNew + "\n" + passwordNew + "\n",
			nil,
			"Результат не створено", true,
		},
		"the input ends before the new slot": {
			"3\n" + passwordNew + "\n" + passwordNew + "\n\n",
			nil,
			"Результат не створено", true,
		},
		"a slot that does not exist, and then none": {
			"3\n" + passwordNew + "\n" + passwordNew + "\n\n7\n\n",
			func(path string) testkit.DB { return testkit.DB{Path: path, Password: passwordNew} },
			"Слот YubiKey — це 1 або 2", false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: passwordA}.Import(t, mergeableXML())
			b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: passwordB}.Import(t, newest)
			output := filepath.Join(dir, "new.kdbx")

			got := runMerge(t, tc.answers, testkit.DB{Path: output}, a, b)

			if tc.stops {
				wantStopped(t, got, output)
				wantInOutput(t, got.Stderr, tc.says)
				return
			}
			wantMerged(t, got)
			wantInOutput(t, got.Stdout+got.Stderr, tc.says)
			wantNotInOutput(t, got.Stdout+got.Stderr, passwordNew, passwordA, passwordB)
			wantInOutput(t, exportOf(t, tc.opens(output)), "Друге")
		})
	}
}

// The cipher and the KDF go with the keys of the copy, and new keys take those of the newest copy.
func TestTheCipherAndTheKDFGoByTheAnswerAboutTheKeys(t *testing.T) {
	t.Parallel()
	// A setting makes this copy the newest and raises no question about an entry.
	newer := replaced(t, mergeableXML(), "<SettingsChanged>2026-02-06T10:00:00Z<", "<SettingsChanged>2099-05-01T10:00:00Z<")
	for name, tc := range map[string]struct {
		answers string
		cipher  string
		kdf     string
		opens   string
	}{
		"the older copy": {"1\n", "ChaCha20", "Argon2d", testkit.Password},
		"new keys":       {"3\nінший\nінший\n\n\n", "Twofish", "Argon2id", "інший"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			oldBase := testkit.DB{Path: filepath.Join(dir, "old-base.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
			newBase := testkit.DB{Path: filepath.Join(dir, "new-base.kdbx"), Password: testkit.Password}.Import(t, newer)
			old := oldBase.WithEncryption(t, filepath.Join(dir, "old.kdbx"), testkit.ChaCha20, testkit.KDFArgon2d)
			fresh := newBase.WithEncryption(t, filepath.Join(dir, "fresh.kdbx"), testkit.Twofish, testkit.KDFArgon2id)
			result := testkit.DB{Path: filepath.Join(dir, "result.kdbx"), Password: tc.opens}

			merged(t, tc.answers, result, old, fresh)

			block := passports(t, testkit.Run(t, tc.opens+"\n", "compare", result.Path).Stdout, result.Path)[result.Path]
			wantField(t, block, "Шифр", tc.cipher)
			if kdf := field(t, block, "KDF"); !strings.HasPrefix(kdf, tc.kdf) {
				t.Errorf("KDF = %q, want %s", kdf, tc.kdf)
			}
		})
	}
}

func TestTheAnswerToStopAlsoLeavesTheKeysToTheRule(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gone := rawCopy(t, dir, "gone.kdbx", withoutTheEntry(deletedAt))
	held := rawCopy(t, dir, "held.kdbx", testkit.PlainXML)
	result := resultIn(dir, "new.kdbx")

	got := merged(t, "с\n", result, gone, held)

	wantNotInOutput(t, got.Stderr, "Чим відкриватиметься новий файл?")
	wantInOutput(t, result.CLI(t, "ls"), "Запис")
}

func TestTheSummarySaysWhatTheMergeDid(t *testing.T) {
	t.Parallel()
	summary := func(added, updated, history, deleted string) []string {
		return []string{
			"  Додано записів, яких не було в усіх копіях: " + added,
			"  Оновлено записів, у яких копії мали різні версії: " + updated,
			"  Збережено в історії колишніх головних версій: " + history,
			"  Видалено записів за вашим рішенням: " + deleted,
		}
	}
	t.Run("a new entry and a conflict", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a, b := divergedPair(t, dir)
		got := merged(t, "", resultIn(dir, "new.kdbx"), a, b)
		wantLines(t, outputLines(got.Stdout), summary("1", "1", "1", "0")...)
	})
	t.Run("an answer that deletes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		gone := rawCopy(t, dir, "gone.kdbx", withoutTheEntry(deletedAt))
		held := rawCopy(t, dir, "held.kdbx", testkit.PlainXML)
		got := merged(t, "2\n", resultIn(dir, "new.kdbx"), gone, held)
		wantLines(t, outputLines(got.Stdout), summary("0", "0", "0", "1")...)
	})
	t.Run("an answer that keeps", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		gone := rawCopy(t, dir, "gone.kdbx", withoutTheEntry(deletedAt))
		held := rawCopy(t, dir, "held.kdbx", testkit.PlainXML)
		got := merged(t, "1\n", resultIn(dir, "new.kdbx"), gone, held)
		wantLines(t, outputLines(got.Stdout), summary("0", "0", "0", "0")...)
	})
	t.Run("an older version chosen: both go to the history under the new one", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := rawCopy(t, dir, "a.kdbx", withVersions(nearOne, v1))
		b := rawCopy(t, dir, "b.kdbx", withVersions(nearTwo, v1))
		got := merged(t, "2\n", resultIn(dir, "new.kdbx"), a, b)
		wantLines(t, outputLines(got.Stdout), summary("0", "1", "2", "0")...)
	})
	t.Run("a copy that lags", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := rawCopy(t, dir, "a.kdbx", withVersions(v1))
		b := rawCopy(t, dir, "b.kdbx", withVersions(v2, v1))
		got := merged(t, "", resultIn(dir, "new.kdbx"), a, b)
		wantLines(t, outputLines(got.Stdout), summary("0", "1", "1", "0")...)
	})
	t.Run("a merged pair", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		got := merged(t, "2\n", resultIn(dir, "new.kdbx"), similarEntries(t, dir, "один", "два")...)
		lines := outputLines(got.Stdout)
		wantLines(t, lines, "  Злито схожих записів в інші: 1")
		wantInOutput(t, got.Stdout, "Злиті схожі записи.", "сюди злито записів: 1")
		wantNotInOutput(t, got.Stdout, unknownInTheReport)
	})
}

// All kinds of question in one run, of three copies with a password of their own each. The markers
// of the spec must not get out in clear text, and the audit must let through what the answers did.
func TestEveryKindOfQuestionInOneRunOfThreeCopies(t *testing.T) {
	t.Parallel()
	const (
		passwordA, passwordB, passwordC = "пароль а", "пароль б", "пароль в"
		goneMarker                      = secretMarker + "-видалене"
	)
	dir := t.TempDir()
	// Two entries that look alike and have a conflict each, and one entry that a copy has deleted.
	similar := func(notes string) []string {
		return []string{"Title", "Пошта", "UserName", "я", "Password", secretMarker + "-пароль", "Notes", secretMarker + notes}
	}
	xmlWith := func(stamp, notes, third string) string {
		return strings.ReplaceAll(withEntries(fields(similar(notes)...), fields(similar(notes+"-друга")...), third), v1, stamp)
	}
	record := "<DeletedObject><UUID>" + thirdEntryUUID + "</UUID><DeletionTime>" + deletedAt + "</DeletionTime></DeletedObject>"
	a := testkit.RawFile{Major: 4, Minor: 1, XML: strings.Replace(xmlWith(nearOne, "-а", ""), "<DeletedObjects>", "<DeletedObjects>"+record, 1)}.Write(t, filepath.Join(dir, "a.kdbx"), passwordA)
	b := testkit.RawFile{Major: 4, Minor: 1, XML: xmlWith(nearTwo, "-б", fields("Title", "Третє", "Password", goneMarker))}.Write(t, filepath.Join(dir, "b.kdbx"), passwordB)
	c := testkit.RawFile{Major: 4, Minor: 1, XML: xmlWith(nearOne, "-а", "")}.Write(t, filepath.Join(dir, "c.kdbx"), passwordC)
	const passwordNew = "гасло результату"
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: passwordNew}

	// The first conflict by the rule, the second with the older version, delete, merge the pair,
	// and then new keys.
	got := merged(t, "1\n2\n2\n2\n4\n"+passwordNew+"\n"+passwordNew+"\n\n\n", result, a, b, c)

	wantNotInOutput(t, got.Stdout+got.Stderr, secretMarker, passwordA, passwordB, passwordC, passwordNew)
	wantNoSecretInFiles(t, dir)
	export := exportOf(t, result)
	wantInOutput(t, export, firstEntryUUID, secretMarker+"-а", secretMarker+"-б", secretMarker+"-а-друга", secretMarker+"-б-друга")
	wantNotInOutput(t, export, secondEntryUUID, goneMarker)
	wantLines(t, outputLines(got.Stdout),
		"  Видалено записів за вашим рішенням: 1",
		"  Злито схожих записів в інші: 1",
	)
	wantUkrainian(t, got.Stdout, got.Stderr, a.Path, b.Path, c.Path, result.Path)
}

var questionCount = regexp.MustCompile(`Питань до вас: (\d+)\.`)

// The dialog says at its start how many questions wait, so a person knows what lies ahead.
func TestTheDialogSaysHowManyQuestionsWait(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got := merged(t, "", resultIn(dir, "new.kdbx"), similarEntries(t, dir, "один", "два", "три")...)
	found := questionCount.FindStringSubmatch(got.Stderr)
	if found == nil || found[1] != "3" {
		t.Errorf("no word about 3 questions:\n%s", got.Stderr)
	}
}

// The report of the copies comes before the questions, on stdout, and the questions on stderr.
func TestMergeShowsTheReportOfTheCopiesFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withVersions(nearOne, v1))
	b := rawCopy(t, dir, "b.kdbx", withVersions(nearTwo, v1))

	got := merged(t, "", resultIn(dir, "new.kdbx"), a, b)

	blocks := passports(t, got.Stdout, a.Path, b.Path)
	wantField(t, blocks[a.Path], "Записів", "1")
	wantInOutput(t, got.Stdout, "Версії сперечаються:")
	wantNotInOutput(t, got.Stdout, "Версії запису сперечаються:")
}

// A question about the same data must give the same dialog whichever copy comes first.
func TestAnAnswerFitsWhicheverCopyComesFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := rawCopy(t, dir, "a.kdbx", withVersions(nearOne, v1))
	b := rawCopy(t, dir, "b.kdbx", withVersions(nearTwo, v1))
	forward, backward := resultIn(dir, "forward.kdbx"), resultIn(dir, "backward.kdbx")

	merged(t, "2\n", forward, a, b)
	merged(t, "2\n", backward, b, a)

	for _, result := range []testkit.DB{forward, backward} {
		if notes := notesOf(t, result, "/Запис"); notes != nearOne {
			t.Errorf("%s: the main version holds %q, want %q", result.Path, notes, nearOne)
		}
	}
}

// New keys can hold a key in the slot of a YubiKey, with or without a password. No real key ran this.
func TestNewKeysCanHoldAYubiKey(t *testing.T) {
	t.Parallel()
	const passwordNew = "гасло результату"
	for name, tc := range map[string]struct {
		answers, opensWith string
	}{
		"with a password": {"2\n" + passwordNew + "\n" + passwordNew + "\n\n2\n", passwordNew + "\n"},
		"alone":           {"2\n\n\n2\n", "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			db := rawCopy(t, dir, "a.kdbx", testkit.PlainXML)
			fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})
			result := filepath.Join(dir, "new.kdbx")

			got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n" + tc.answers, Env: noJudge(fake)}, "merge", db.Path, "--output", result)

			wantMerged(t, got)
			wantInOutput(t, got.Stdout, "Новий файл відкривають нові ключі", "Новий файл потребує і YubiKey, слот 2.")
			wantInOutput(t, got.Stderr, "Торкніться YubiKey")
			wantChallenges(t, fake, "2", challengeOf(t, result))

			opened := testkit.RunWith(t, testkit.Options{Stdin: tc.opensWith, Env: fake.Env()}, "compare", result, "--yubikey", "2")
			wantCode(t, opened, 0)
			blocked := testkit.RunWith(t, testkit.Options{Stdin: tc.opensWith, Env: fake.Env()}, "compare", result)
			wantCode(t, blocked, 3)
		})
	}
}

// The tool says at once when the program for a key is not there, and the person can go without it.
func TestANewSlotWithoutTheProgramOfTheKeyAsksAgain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := rawCopy(t, dir, "a.kdbx", testkit.PlainXML)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: "гасло результату"}
	opts := testkit.Options{
		Stdin: db.Password + "\n2\n" + result.Password + "\n" + result.Password + "\n\n2\n\n",
		Env:   []string{"PATH=" + t.TempDir(), safewriter.FaultVariable + "=" + judge.FaultNoInstallPlaces},
	}

	got := testkit.RunWith(t, opts, "merge", db.Path, "--output", result.Path)

	wantMerged(t, got)
	wantInOutput(t, got.Stderr, "ykman")
	wantNotInOutput(t, got.Stdout, "слот")
}

func TestAResultStaysUnderTheOutputNameOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gone := rawCopy(t, dir, "gone.kdbx", withoutTheEntry(deletedAt))
	held := rawCopy(t, dir, "held.kdbx", testkit.PlainXML)
	merged(t, "2\n", resultIn(dir, "new.kdbx"), gone, held)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("%d files in the directory, want the copies and the result", len(entries))
	}
}

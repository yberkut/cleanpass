package main_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

const (
	deletionSection = "Видалення"
	pairsSection    = "Схожі записи"
)

// A record about a deletion needs a bin that is off: otherwise rm only moves the entry to the bin.
func TestCompareShowsADeletionWithItsDatesAndCopies(t *testing.T) {
	noBin := strings.Replace(testkit.RichXML(), "<RecycleBinEnabled>True<", "<RecycleBinEnabled>False<", 1)
	dir := t.TempDir()
	kept := newCopy(t, dir, "kept.kdbx", noBin)
	deleted := copyOf(t, kept, "deleted.kdbx")
	deleted.CLI(t, "rm", workEntry)

	got := testkit.Run(t, passwords(2), "compare", kept.Path, deleted.Path)

	wantCode(t, got, 0)
	lines := section(t, got.Stdout, deletionSection)
	wantLines(t, lines,
		"  Друге (логін: u2, сайт: b.example)",
		"    Запис про видалення: "+deleted.Path,
		"    Поза кошиком: "+kept.Path,
	)
	dates := slices.IndexFunc(lines, func(line string) bool {
		return strings.HasPrefix(line, "    Видалено 20") && strings.HasSuffix(line, "; остання зміна вмісту "+localTime(t, "2026-03-01T10:00:03Z"))
	})
	if dates < 0 {
		t.Errorf("no dates of the deletion and of the last change:\n%s", strings.Join(lines, "\n"))
	}
	wantNoSection(t, got.Stdout, onlyInSomeCopies)
}

func TestCompareTakesAMoveToTheBinForADeletion(t *testing.T) {
	dir := t.TempDir()
	kept := newCopy(t, dir, "kept.kdbx", testkit.RichXML())
	recycled := copyOf(t, kept, "recycled.kdbx")
	recycled.CLI(t, "rm", workEntry)

	got := testkit.Run(t, passwords(2), "compare", kept.Path, recycled.Path)

	wantLines(t, section(t, got.Stdout, deletionSection),
		"  Друге (логін: u2, сайт: b.example)",
		"    У кошику: "+recycled.Path,
		"    Поза кошиком: "+kept.Path,
	)
}

// withEntries gives PlainXML with these entries in place of its one entry. Each one has a UUID of
// its own, by its position, and its fields as XML. An empty item leaves the position free.
func withEntries(entries ...string) string {
	ids := []string{"AAAAAAAAAAAAAAAAAAACAA==", "AAAAAAAAAAAAAAAAAAACAQ==", "AAAAAAAAAAAAAAAAAAACAg==", "AAAAAAAAAAAAAAAAAAACAw=="}
	var all strings.Builder
	for i, content := range entries {
		if content != "" {
			all.WriteString("<Entry><UUID>" + ids[i] + "</UUID><Times><LastModificationTime>" + v1 + "</LastModificationTime></Times>" + content + "</Entry>")
		}
	}
	start := strings.Index(testkit.PlainXML, "<Entry>")
	end := strings.Index(testkit.PlainXML, "</Entry>") + len("</Entry>")
	return testkit.PlainXML[:start] + all.String() + testkit.PlainXML[end:]
}

// fields gives string fields as XML from their names and values.
func fields(pairs ...string) string {
	var result strings.Builder
	for i := 0; i < len(pairs); i += 2 {
		result.WriteString("<String><Key>" + pairs[i] + "</Key><Value>" + pairs[i+1] + "</Value></String>")
	}
	return result.String()
}

func TestCompareShowsSimilarEntriesAndNoPassword(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second string
		want          []string
		bulk          bool
	}{
		{
			name:   "the same title and login, the same password",
			first:  fields("Title", "Пошта", "UserName", "я", "Password", "SECRET-MARKER-same"),
			second: fields("Title", " ПОШТА ", "UserName", "Я", "Password", "SECRET-MARKER-same", "Notes", "SECRET-MARKER-notes"),
			want: []string{
				"    Спільне: назва і логін; пароль однаковий",
				"    Відрізняється: нотатки різні, назва різна, логін різний",
			},
		},
		{
			name:   "the same login and host, another password",
			first:  fields("Title", "Пошта", "UserName", "я", "URL", "https://mail.example.com/a", "Password", "SECRET-MARKER-one"),
			second: fields("Title", "Лист", "UserName", "я", "URL", "https://mail.example.com/b", "Password", "SECRET-MARKER-two"),
			want: []string{
				"    Спільне: логін і сайт; пароль різний",
				"    Відрізняється: назва різна, URL різний",
			},
		},
		{
			name:   "both at once",
			first:  fields("Title", "Пошта", "UserName", "я", "URL", "https://mail.example.com/a"),
			second: fields("Title", "Пошта", "UserName", "я", "URL", "https://mail.example.com/b"),
			want: []string{
				"    Спільне: назва і логін, логін і сайт; пароль однаковий",
				"    Відрізняється: URL різний",
			},
		},
		{
			name:   "the same content",
			first:  fields("Title", "Пошта", "UserName", "я", "Password", "SECRET-MARKER-same"),
			second: fields("Title", "Пошта", "UserName", "я", "Password", "SECRET-MARKER-same"),
			want:   []string{"    Спільне: назва і логін; пароль однаковий; вміст однаковий"},
			bulk:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			a := rawCopy(t, dir, "a.kdbx", withEntries(tc.first))
			b := rawCopy(t, dir, "b.kdbx", withEntries("", tc.second))

			got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

			wantCode(t, got, 0)
			lines := section(t, got.Stdout, pairsSection)
			wantLines(t, lines, tc.want...)
			wantLines(t, lines,
				"    Перший запис: змінено "+localTime(t, v1)+"; є в "+a.Path,
				"    Другий запис: змінено "+localTime(t, v1)+"; є в "+b.Path,
			)
			if tc.bulk {
				wantLines(t, lines, "  Пар з однаковим вмістом: 1. Під час злиття їх можна злити одним рішенням.")
			}
			if strings.Contains(got.Stdout+got.Stderr, "SECRET-MARKER") {
				t.Errorf("the output holds a value:\n%s\n%s", got.Stdout, got.Stderr)
			}
		})
	}
}

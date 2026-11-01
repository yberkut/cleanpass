package main_test

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/testkit"
)

const futureWarning = "часова мітка з майбутнього"

// Each case moves one time stamp of the fixture past the current time. A wrong clock on any
// system leaves such a stamp, also when the entry was only read or the file only saved.
func TestCompareWarnsAboutAStampFromTheFuture(t *testing.T) {
	future := time.Now().AddDate(1, 0, 0).UTC().Truncate(time.Second)
	stamp := future.Format(time.RFC3339)
	for _, tc := range []struct {
		name, from string
	}{
		{"main version changed", "2026-03-15T10:00:00Z"},
		{"entry created", "2026-01-01T10:00:02Z"},
		{"entry moved", "2026-02-01T10:00:02Z"},
		{"entry read", "2026-04-02T10:00:00Z"},
		{"older version changed", "2026-02-20T10:00:00Z"},
		{"older version read", "2026-02-20T10:00:03Z"},
		{"group changed", "2026-03-01T10:00:02Z"},
		{"group read", "2026-04-01T10:00:02Z"},
		{"group moved", "2026-02-01T10:00:04Z"},
		{"record about a deletion", "2026-02-10T10:00:00Z"},
		{"database name changed", "2026-02-02T10:00:01Z"},
		{"master key changed", "2026-02-03T10:00:00Z"},
		{"custom icon changed", "2026-02-04T10:00:00Z"},
		{"settings changed", "2026-02-06T10:00:00Z"},
		{"custom data of the database", "2026-02-07T10:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(testkit.RichXML(), tc.from) {
				t.Fatalf("no time stamp %s in the fixture", tc.from)
			}
			dir := t.TempDir()
			right := newCopy(t, dir, "right.kdbx", testkit.RichXML())
			wrong := newCopy(t, dir, "wrong.kdbx", strings.ReplaceAll(testkit.RichXML(), tc.from, stamp))

			got := testkit.Run(t, passwords(2), "compare", right.Path, wrong.Path)

			wantCode(t, got, 0)
			blocks := passports(t, got.Stdout, right.Path, wrong.Path)
			if strings.Contains(blocks[right.Path], futureWarning) {
				t.Errorf("a warning about the copy with the right clock:\n%s", blocks[right.Path])
			}
			want := "  Попередження: у цій копії є " + futureWarning + ": " + localTime(t, stamp) +
				". Годинник системи, де змінювали цю копію, міг бути збитий, тому її позначка найсвіжішої теж може бути хибною."
			wantLine(t, blocks[wrong.Path], want)
		})
	}
}

// KeePassXC writes the time of a custom data item only for the database, so these files come from
// the test writer. KPXC_RANDOM_SLUG holds the moment of the last save, also a save with no edit.
func TestCompareWarnsAboutTheTimeOfCustomData(t *testing.T) {
	stamp := time.Now().AddDate(1, 0, 0).UTC().Truncate(time.Second).Format(time.RFC3339)
	item := func(key string) string {
		return "<CustomData><Item><Key>" + key + "</Key><Value>aa</Value><LastModificationTime>" + stamp + "</LastModificationTime></Item></CustomData>"
	}
	for _, tc := range []struct {
		name     string
		from, to string
	}{
		{"a save with no edit", "</Meta>", item("KPXC_RANDOM_SLUG") + "</Meta>"},
		{"custom data of a group", "<Entry>", item("own") + "<Entry>"},
		{"custom data of an entry", "</Entry>", item("own") + "</Entry>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := rawCopy(t, t.TempDir(), "a.kdbx", strings.Replace(testkit.PlainXML, tc.from, tc.to, 1))

			got := testkit.Run(t, passwords(1), "compare", db.Path)

			if !strings.Contains(got.Stdout, futureWarning+": "+localTime(t, stamp)) {
				t.Errorf("no warning:\n%s", got.Stdout)
			}
		})
	}
}

// An expiry date lies in the future by design, and copies from the right clock are the usual case.
func TestCompareRaisesNoFalseAlarmAboutTheFuture(t *testing.T) {
	expires := time.Now().AddDate(5, 0, 0).UTC().Format(time.RFC3339)
	dir := t.TempDir()
	db := newCopy(t, dir, "a.kdbx", strings.ReplaceAll(testkit.RichXML(), "2030-01-01T10:00:02Z", expires))
	saved := copyOf(t, db, "b.kdbx")
	saved.CLI(t, "edit", "--notes", "щойно збережено", workEntry)

	got := testkit.Run(t, passwords(2), "compare", db.Path, saved.Path)

	if strings.Contains(got.Stdout, futureWarning) {
		t.Errorf("false alarm:\n%s", got.Stdout)
	}
}

const unrelatedWarning = "Попередження: копії схожі на різні бази"

var base64UUID = regexp.MustCompile(`AAAAAAAAAAAAAAAAAAA[A-Za-z0-9+/]{2}[A-Za-z0-9+/]?=+`)

// withNewUUIDs gives the same data under other UUIDs, as an import into a new database does.
func withNewUUIDs(xml string, first byte) string {
	return base64UUID.ReplaceAllStringFunc(xml, func(id string) string {
		raw, err := base64.StdEncoding.DecodeString(id)
		if err != nil {
			panic(err)
		}
		raw[0] = first
		return base64.StdEncoding.EncodeToString(raw)
	})
}

// anotherDatabase is synthetic data with no UUID and no similar entry in common with RichXML.
func anotherDatabase(first byte) string {
	return strings.NewReplacer(
		"Друге", "Банк", "Пошта", "Форум", "У кошику", "Магазин",
		"<Value>u2<", "<Value>клієнт<", "<Value>u3<", "<Value>покупець<", "user@example.com", "учасник",
		"mail.example.com", "forum.example.org", "b.example", "bank.example",
	).Replace(withNewUUIDs(testkit.RichXML(), first))
}

func TestCompareWarnsAboutCopiesThatLookLikeDifferentDatabases(t *testing.T) {
	dir := t.TempDir()
	newCopy(t, dir, "home.kdbx", testkit.RichXML())
	newCopy(t, dir, "work.kdbx", anotherDatabase(0x10))

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "compare", "home.kdbx", "work.kdbx")

	wantCode(t, got, 0)
	lines := section(t, got.Stdout, unrelatedWarning)
	wantLines(t, lines, "  Група 1: home.kdbx", "  Група 2: work.kdbx")
	wantLine(t, got.Stdout, "Різні бази зливати не треба. Лише якщо це копії однієї бази, злийте їх із опцією, яка знімає цей захист.")
	if want, command := "home.kdbx work.kdbx --allow-unrelated --output home-merged.kdbx", mergeCommand(t, got.Stdout); command != want {
		t.Errorf("command %q, want %q", command, want)
	}
	wantUkrainian(t, got.Stdout, got.Stderr, "home.kdbx", "work.kdbx", "user@example.com", "mail.example.com", "forum.example.org", "bank.example", "b.example", "quoted")
}

// The warning is only as good as its silence on real copies.
func TestCompareRaisesNoFalseAlarmAboutDifferentDatabases(t *testing.T) {
	dir := t.TempDir()
	base := newCopy(t, dir, "base.kdbx", testkit.RichXML())
	edited := copyOf(t, base, "edited.kdbx")
	edited.CLI(t, "edit", "--notes", "зміна", workEntry)
	imported := newCopy(t, dir, "imported.kdbx", withNewUUIDs(testkit.RichXML(), 0x20))
	if strings.Contains(withNewUUIDs(testkit.RichXML(), 0x20), "AAAAAAAAAAAAAAAAAAABAA==") {
		t.Fatal("the fixture keeps a UUID of the original")
	}

	for _, tc := range []struct {
		name  string
		paths []string
	}{
		{"a plain copy of a file", []string{base.Path, edited.Path}},
		{"an import under new UUIDs", []string{base.Path, imported.Path}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := testkit.Run(t, passwords(2), append([]string{"compare"}, tc.paths...)...)

			wantCode(t, got, 0)
			if strings.Contains(got.Stdout, unrelatedWarning) {
				t.Errorf("false alarm:\n%s", got.Stdout)
			}
			if strings.Contains(got.Stdout, "--allow-unrelated") {
				t.Errorf("the command takes off a protection nobody needs off:\n%s", got.Stdout)
			}
		})
	}
}

// One copy of three is enough: the two others must not hide it.
func TestCompareNamesTheOddCopyOutOfThree(t *testing.T) {
	dir := t.TempDir()
	first := newCopy(t, dir, "first.kdbx", testkit.RichXML())
	copyOf(t, first, "second.kdbx")
	newCopy(t, dir, "third.kdbx", anotherDatabase(0x10))

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(3), Dir: dir}, "compare", "first.kdbx", "second.kdbx", "third.kdbx")

	wantCode(t, got, 0)
	wantLines(t, section(t, got.Stdout, unrelatedWarning), "  Група 1: first.kdbx, second.kdbx", "  Група 2: third.kdbx")
}

func TestCompareAllowUnrelatedCommandGoesBeforeFilesThatLookLikeOptions(t *testing.T) {
	dir := t.TempDir()
	newCopy(t, dir, "--home.kdbx", testkit.RichXML())
	newCopy(t, dir, "work.kdbx", anotherDatabase(0x10))

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "compare", "--", "--home.kdbx", "work.kdbx")

	if want, command := "--allow-unrelated --output --home-merged.kdbx -- --home.kdbx work.kdbx", mergeCommand(t, got.Stdout); command != want {
		t.Errorf("command %q, want %q", command, want)
	}
}

// Copies that hold the same data need no merge, whatever their UUIDs say.
func TestCompareOffersNoCommandForDifferentDatabasesThatHoldTheSame(t *testing.T) {
	dir := t.TempDir()
	empty := "<Entry>"
	start := strings.Index(testkit.PlainXML, empty)
	end := strings.Index(testkit.PlainXML, "</Entry>") + len("</Entry>")
	noEntries := testkit.PlainXML[:start] + testkit.PlainXML[end:]
	a := rawCopy(t, dir, "a.kdbx", noEntries)
	b := rawCopy(t, dir, "b.kdbx", strings.Replace(noEntries, "AAAAAAAAAAAAAAAAAAAAEA==", "AAAAAAAAAAAAAAAAAAAAEQ==", 1))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	if !strings.Contains(got.Stdout, unrelatedWarning) {
		t.Errorf("the rule says these look like different databases:\n%s", got.Stdout)
	}
	wantLine(t, got.Stdout, "Наступна команда не потрібна.")
}

// The option does not exist before the stop that needs it lands. From then on this test runs.
func TestCompareAllowUnrelatedCommandRunsAsTheRealMerge(t *testing.T) {
	skipWithoutMerge(t)
	dir := t.TempDir()
	newCopy(t, dir, "home.kdbx", testkit.RichXML())
	newCopy(t, dir, "work.kdbx", anotherDatabase(0x10))

	got := testkit.RunWith(t, testkit.Options{Stdin: passwords(2), Dir: dir}, "compare", "home.kdbx", "work.kdbx")

	line := theCommandLine(t, got.Stdout)
	_, stderr, code := inShell(t, dir, line, passwords(2))
	if strings.Contains(stderr, "Невідома опція: --allow-unrelated") {
		t.Skip("merge does not take --allow-unrelated yet")
	}
	if code != 0 {
		t.Fatalf("%s: exit code %d\n%s", line, code, stderr)
	}
}

package merge_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/testkit"
)

// These tests let the real KeePassXC write the file that holds the memory, because the memory
// is worth nothing if a save loses it.

// saveResult writes a result with the Gateway, the way the source copy is stored, and lets the
// real KeePassXC save it again, as a person does between two merges.
func saveResult(t *testing.T, source testkit.DB, merged *snapshot.Snapshot) testkit.DB {
	t.Helper()
	db := testkit.DB{Path: filepath.Join(filepath.Dir(source.Path), "result.kdbx"), Password: testkit.Password}
	writeFile(t, db, merged, source)
	db.Resave(t)
	return db
}

func writeFile(t *testing.T, db testkit.DB, merged *snapshot.Snapshot, source testkit.DB) {
	t.Helper()
	var file bytes.Buffer
	format := gateway.OutputFormat(openCopy(t, source).Format)
	if err := gateway.Write(&file, merged, format, gateway.Credentials{Password: []byte(db.Password)}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db.Path, file.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// laterThanAnySave is a current time for the next merge. KeePassXC stamps its saves with the
// clock of this run, and a fixed time would make them come from the future.
func laterThanAnySave() time.Time { return time.Now().Add(time.Hour).UTC().Truncate(time.Second) }

// answerAll answers each question of these kinds of the merge of the real files.
func answerAll(t *testing.T, in merge.Input, choose func(merge.Question) (int, bool)) *merge.Result {
	t.Helper()
	for _, q := range mustRun(t, in).Questions {
		if choice, answer := choose(q); answer {
			in.Decisions = append(in.Decisions, merge.Decision{Question: q.ID, Choice: choice})
		}
	}
	if len(in.Decisions) == 0 {
		t.Fatal("no question to answer")
	}
	return mustRun(t, in)
}

func TestKeePassXCKeepsEachKeyOfTheMemoryThroughASaveAndAnEdit(t *testing.T) {
	// Each kind of decision, made by the engine.
	firstOfPair, secondOfPair := snapshot.UUID{15: 0x61}, snapshot.UUID{15: 0x62}
	bank1, bank2 := snapshot.UUID{15: 0x63}, snapshot.UUID{15: 0x64}
	keptID, deletedID := snapshot.UUID{15: 0x65}, snapshot.UUID{15: 0x66}
	behind := account(mailID, "Anomaly", "a", "", "replaced")
	behind.Times.LastModification = hours(10)
	edited := account(mailID, "Anomaly", "a", "", "edited")
	edited.Times.LastModification = hours(5)
	edited.History = []snapshot.EntryVersion{behind.EntryVersion}
	first := tree(node(groupB, "Work", 1))
	first.Root.Entries = []snapshot.Entry{
		account(firstOfPair, "Mail", "user", "", "one"), account(secondOfPair, "Mail", "user", "", "two"),
		account(bank1, "Bank", "b", "", "one"), account(bank2, "Bank", "b", "", "two"),
		account(keptID, "Kept", "k", "", "x"), account(deletedID, "Deleted", "d", "", "x"),
		behind,
	}
	second := tree(node(groupA, "Work", 1))
	second.Root.Entries = []snapshot.Entry{edited}
	second.Deletions = []snapshot.Deletion{{UUID: keptID, Time: hours(9)}, {UUID: deletedID, Time: hours(9)}}
	wanted := map[merge.QuestionID]int{
		merge.QuestionID("pair:" + firstOfPair.String() + ":" + secondOfPair.String()): 1,
		merge.QuestionID("pair:" + bank1.String() + ":" + bank2.String()):              0,
		merge.QuestionID("deletion:" + keptID.String()):                                merge.AnswerKeep,
		merge.QuestionID("deletion:" + deletedID.String()):                             merge.AnswerDelete,
		merge.QuestionID("conflict:" + mailID.String()):                                0,
	}
	result := answerAll(t, input(named(first, second)...), func(q merge.Question) (int, bool) {
		choice, known := wanted[q.ID]
		return choice, known
	})
	items := memoryItems(result.Merged)
	for _, kind := range []string{"entries", "groups", "different", "rejected", "accepted", "replaced"} {
		if !slices.ContainsFunc(items, func(item snapshot.CustomDataItem) bool { return strings.Contains(item.Key, "/"+kind+"/") }) {
			t.Fatalf("the engine made no decision of the kind %q: %s", kind, printed(items))
		}
	}

	var xmlItems strings.Builder
	for _, item := range items {
		xmlItems.WriteString("<Item><Key>" + item.Key + "</Key><Value>" + item.Value + "</Value></Item>")
	}
	db := testkit.DB{Path: filepath.Join(t.TempDir(), "memory.kdbx"), Password: testkit.Password}.
		Import(t, strings.Replace(testkit.RichXML(), "<Item><Key>meta-custom-key</Key>", xmlItems.String()+"<Item><Key>meta-custom-key</Key>", 1))
	db.Resave(t)
	db.CLI(t, "edit", "--notes", "нова нотатка", "/Робота/Друге")

	saved := openCopy(t, db)

	got := map[string]string{}
	for _, item := range memoryItems(saved) {
		got[item.Key] = item.Value
	}
	if len(got) != len(items) {
		t.Errorf("KeePassXC kept %d items of %d", len(got), len(items))
	}
	for _, item := range items {
		if got[item.Key] != item.Value {
			t.Errorf("KeePassXC has the item %q with the value %q, want %q", item.Key, got[item.Key], item.Value)
		}
	}
}

// The whole way, for each kind of question that real files raise: an answer, the file with the
// result, a save of KeePassXC, and the next merge with each old copy.
func TestTheNextMergeAfterASaveOfKeePassXCAsksNothingTheHumanAnswered(t *testing.T) {
	const newEntry = "/Нове"
	addSimilar := func(t *testing.T, db testkit.DB) {
		t.Helper()
		db.CLI(t, "add", "-u", "u2", "--url", "https://b.example", newEntry)
	}
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, original, forked testkit.DB)
		kind   string
		choice int
		// wantGone says the entry of the fixture must not be in the next result.
		wantGone bool
	}{
		{
			name: "a deletion the human accepted",
			change: func(t *testing.T, _, forked testkit.DB) {
				forked.CLI(t, "rm", entryPath)
				forked.CLI(t, "rm", entryInBinPath)
			},
			kind: "deletion", choice: merge.AnswerDelete, wantGone: true,
		},
		{
			name: "a deletion the human rejected",
			change: func(t *testing.T, _, forked testkit.DB) {
				forked.CLI(t, "rm", entryPath)
				forked.CLI(t, "rm", entryInBinPath)
			},
			kind: "deletion", choice: merge.AnswerKeep,
		},
		{
			name:   "a move to the bin the human accepted",
			change: func(t *testing.T, _, forked testkit.DB) { forked.CLI(t, "rm", entryPath) },
			kind:   "deletion", choice: merge.AnswerDelete,
		},
		{
			name:   "a move to the bin the human rejected",
			change: func(t *testing.T, _, forked testkit.DB) { forked.CLI(t, "rm", entryPath) },
			kind:   "deletion", choice: merge.AnswerKeep,
		},
		{
			name:   "a pair the human merged",
			change: func(t *testing.T, _, forked testkit.DB) { addSimilar(t, forked) },
			kind:   "pair", choice: 1,
		},
		{
			name:   "a pair the human kept apart",
			change: func(t *testing.T, _, forked testkit.DB) { addSimilar(t, forked) },
			kind:   "pair", choice: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, forked := forkedFiles(t)
			tc.change(t, original, forked)
			in := mergeInput(t, original, forked)

			first := answerAll(t, in, func(q merge.Question) (int, bool) { return tc.choice, strings.HasPrefix(string(q.ID), tc.kind+":") })

			saved := openCopy(t, saveResult(t, original, first.Merged))
			for _, old := range []testkit.DB{original, forked} {
				again := mustRun(t, merge.Input{
					Copies: []merge.Copy{{Name: "result", Snapshot: saved}, {Name: "old", Snapshot: openCopy(t, old)}},
					Now:    laterThanAnySave(),
					Key:    testKey,
				})
				for _, q := range again.Questions {
					if strings.HasPrefix(string(q.ID), tc.kind+":") {
						t.Errorf("the merge with %s asks %s again", old.Path, q.ID)
					}
				}
				if _, stays := allEntries(again.Merged)[secondEntryID]; stays == tc.wantGone {
					t.Errorf("the merge with %s: the entry stays = %v", old.Path, stays)
				}
				if got, want := countVersions(again.Merged), countVersions(first.Merged); got != want {
					t.Errorf("the merge with %s has %d versions, the result has %d", old.Path, got, want)
				}
			}
		})
	}
}

// A real edit in an old copy after the human deleted the entry: the file that remembers, a save of
// KeePassXC, and the next merge must ask and keep what the human has not seen.
func TestARealEditInAnOldCopyAsksAfterAnAcceptedDeletion(t *testing.T) {
	original, forked := forkedFiles(t)
	forked.CLI(t, "rm", entryPath)
	forked.CLI(t, "rm", entryInBinPath)
	first := answerAll(t, mergeInput(t, original, forked), func(q merge.Question) (int, bool) {
		return merge.AnswerDelete, strings.HasPrefix(string(q.ID), "deletion:")
	})
	saved := openCopy(t, saveResult(t, original, first.Merged))

	original.CLI(t, "edit", "--notes", "правка після рішення", entryPath)
	again := mustRun(t, merge.Input{
		Copies: []merge.Copy{{Name: "result", Snapshot: saved}, {Name: "old", Snapshot: openCopy(t, original)}},
		Now:    laterThanAnySave(),
		Key:    testKey,
	})

	if _, asked := deletionQuestions(again.Questions)[secondEntryID]; !asked {
		t.Errorf("the merge asks %s", printed(again.Questions))
	}
	if allEntries(again.Merged)[secondEntryID] == nil {
		t.Error("the edit is gone with the entry")
	}
}

func TestGroupsThatJoinedStayOneAfterASaveOfKeePassXC(t *testing.T) {
	original, forked := forkedFiles(t)
	original.CLI(t, "mkdir", "/Спільна")
	forked.CLI(t, "mkdir", "/Спільна")
	in := mergeInput(t, original, forked)

	first := mustRun(t, in)
	if len(first.MergedGroups) != 1 {
		t.Fatalf("merged groups = %v, want 1 pair", first.MergedGroups)
	}

	saved := openCopy(t, saveResult(t, original, first.Merged))

	again := mustRun(t, merge.Input{
		Copies: []merge.Copy{{Name: "result", Snapshot: saved}, {Name: "old", Snapshot: openCopy(t, forked)}},
		Now:    laterThanAnySave(),
		Key:    testKey,
	})
	if got, want := again.Merged.Stats().Groups, saved.Stats().Groups; got != want {
		t.Errorf("the next merge has %d groups, the result has %d", got, want)
	}
}

// historyXML gives a database with one entry with this many versions, each with a long note, and
// these limits.
func historyXML(prefix string, versions int, items, size string) string {
	var history strings.Builder
	version := func(n int) string {
		return `<UUID>AAAAAAAAAAAAAAAAAAABAA==</UUID><Times><LastModificationTime>2026-02-` + twoDigits(n) + `T10:00:00Z</LastModificationTime></Times>` +
			`<String><Key>Title</Key><Value>Запис</Value></String><String><Key>Notes</Key><Value>` + prefix + strings.Repeat("x", 200) + twoDigits(n) + `</Value></String>`
	}
	for n := 1; n < versions; n++ {
		history.WriteString("<Entry>" + version(n) + "</Entry>")
	}
	return `<?xml version="1.0" encoding="utf-8" standalone="yes"?><KeePassFile><Meta><Generator>cleanpass-tests</Generator>` +
		`<HistoryMaxItems>` + items + `</HistoryMaxItems><HistoryMaxSize>` + size + `</HistoryMaxSize></Meta><Root><Group><UUID>AAAAAAAAAAAAAAAAAAAAEA==</UUID><Name>Root</Name>` +
		`<Entry>` + version(versions) + `<History>` + history.String() + `</History></Entry></Group></Root></KeePassFile>`
}

func twoDigits(n int) string { return string(rune('0'+n/10)) + string(rune('0'+n%10)) }

// The limits count against the real KeePassXC: it cuts a copy with the old limits at an edit, and
// it cuts nothing in the result.
func TestKeePassXCDoesNotCutTheMergedHistory(t *testing.T) {
	const versions = 8
	for name, limits := range map[string][2]string{
		"the limit of items":    {"3", "-1"},
		"the limit of the size": {"-1", "1000"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, historyXML("a", versions, limits[0], limits[1]))
			b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, historyXML("b", versions, limits[0], limits[1]))
			merged := mustRun(t, merge.Input{
				Copies: []merge.Copy{{Name: "a", Snapshot: openCopy(t, a)}, {Name: "b", Snapshot: openCopy(t, b)}},
				Now:    laterThanAnySave(),
				Key:    testKey,
			}).Merged
			before := len(onlyEntry(t, merged).History)
			if before < 2*versions-1 {
				t.Fatalf("the result has %d older versions", before)
			}
			result := testkit.DB{Path: filepath.Join(dir, "result.kdbx"), Password: testkit.Password}
			writeFile(t, result, merged, a)

			a.CLI(t, "edit", "--title", "Запис нової версії", "/Запис")
			result.CLI(t, "edit", "--title", "Запис нової версії", "/Запис")

			if got := len(onlyEntry(t, openCopy(t, a)).History); got >= versions {
				t.Errorf("the copy with the old limits has %d older versions after the edit, so the test proves nothing", got)
			}
			if got := len(onlyEntry(t, openCopy(t, result)).History); got != before+1 {
				t.Errorf("the result has %d older versions after the edit, want %d", got, before+1)
			}
		})
	}
}

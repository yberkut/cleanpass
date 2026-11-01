package report_test

import (
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/report"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// withSecretsEverywhere puts generated secret values into every part of every version of the
// scenario that a report might show, beside the passwords the generator already marks. Each new
// value follows from the old one, so two versions stay equal or different as they were.
func withSecretsEverywhere(s mergetest.Scenario) {
	marker := mergetest.SecretMarker
	mark := func(v *snapshot.EntryVersion) {
		// All entries with one of the titles hold their label as protected values, so the label
		// shows nothing of them. The others still give pairs.
		protectLabel := slices.ContainsFunc(v.Fields, func(f snapshot.Field) bool { return f.Key == "Title" && f.Value == "Git" })
		for i := range v.Fields {
			f := &v.Fields[i]
			switch {
			case protectLabel && (f.Key == "Title" || f.Key == "UserName" || f.Key == "URL"):
				f.Value, f.Protected = marker+f.Value, true
			case f.Key == "Notes" || f.Key == "Custom":
				f.Value = marker + f.Value
			case f.Key == "URL" && f.Value != "":
				// The host stays, so the entries still pair by it. All the rest is secret.
				f.Value = strings.Replace(f.Value, "https://", "https://me:"+marker+"url@", 1) + "?token=" + marker + "query"
			}
		}
		v.Fields = append(v.Fields,
			snapshot.Field{Key: "Open", Value: marker + "open"},
			snapshot.Field{Key: "Closed", Value: marker + "closed", Protected: true},
		)
		v.Tags += ";" + marker + "tag"
		v.OverrideURL = marker + "override"
		v.AutoType.DefaultSequence = marker + "sequence"
		v.AutoType.Associations = append(v.AutoType.Associations, snapshot.AutoTypeAssociation{Window: marker + "window", KeystrokeSequence: marker + "keys"})
		v.CustomData = append(v.CustomData, snapshot.CustomDataItem{Key: "extension", Value: marker + "data"})
		for i := range v.Attachments {
			v.Attachments[i].Name = marker + "name"
		}
	}
	for i := range s.Copies {
		s.Copies[i].Snapshot.Root.Walk(func(g *snapshot.Group) {
			g.Notes = marker + "group"
			for j := range g.Entries {
				e := &g.Entries[j]
				mark(&e.EntryVersion)
				for k := range e.History {
					mark(&e.History[k])
				}
			}
		})
	}
}

func reportOf(t rapid.TB, s mergetest.Scenario) (string, merge.Facts) {
	t.Helper()
	result, err := merge.Run(merge.Input{Copies: s.Copies, Now: s.Now, Key: merge.Key{1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	facts := result.Facts()
	var out strings.Builder
	if err := report.WriteDifferences(&out, facts); err != nil {
		t.Fatal(err)
	}
	return out.String(), facts
}

// "Без секретів у звіті" from the spec.
func TestPropertyNoSecretsInTheReport(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		withSecretsEverywhere(s)

		text, facts := reportOf(t, s)

		if strings.Contains(text, mergetest.SecretMarker) {
			t.Fatalf("the report holds a secret value:\n%s", text)
		}
		if facts.Unknown != 0 {
			t.Fatalf("the report cannot show %d events or questions:\n%s", facts.Unknown, text)
		}
	})
}

// A property says nothing about a part of the report it never fills. So this counts the phrases
// over a fixed set of scenarios, with the same secrets as above.
func TestScenariosFillEveryPartOfTheReport(t *testing.T) {
	seen := map[string]int{}
	phrases := []string{
		"Записи лише в частині копій", "Копія відстає", "Версії сперечаються", "Видалення.", "Схожі записи",
		"Пар з однаковим вмістом",
		"Різниця 24 години або більше", "Різниця менша за 24 години", "Одна з версій має дату з майбутнього", "Увага: збитий годинник",
		"запису немає", "ця версія є в історії іншої копії", "цієї версії немає в історії інших копій", "вміст як у версії, що стане головною",
		"пароль різний", "назва різна", "логін різний", "URL різний", "нотатки різні", "поле «Custom» різне", "значок різний", "кольори різні", "теги різні",
		"вкладення різні", "Auto-Type різний", "дані розширень різні",
		"Запис про видалення:", "У кошику:", "Поза кошиком:", "Видалено ",
		"Спільне: назва і логін", "логін і сайт", "пароль однаковий", "вміст однаковий", "Відрізняється:", "Перший запис:", "Другий запис:",
		"(без назви, UUID", "логін: ", "сайт: ",
	}
	for i := range 500 {
		s := mergetest.Scenarios().Example(i)
		withSecretsEverywhere(s)
		text, _ := reportOf(t, s)
		for _, phrase := range phrases {
			if strings.Contains(text, phrase) {
				seen[phrase]++
			}
		}
	}
	t.Logf("scenarios with each phrase: %v", seen)
	for _, phrase := range phrases {
		if seen[phrase] == 0 {
			t.Errorf("no scenario reaches %q", phrase)
		}
	}
}

// The generator does not change these parts of a version, so a plain table keeps them honest.
func TestEveryAttributeHasItsOwnWords(t *testing.T) {
	for attribute, want := range map[merge.Attribute]string{
		merge.AttributeIcon:         "значок різний",
		merge.AttributeColors:       "кольори різні",
		merge.AttributeOverrideURL:  "перевизначення URL різне",
		merge.AttributeQualityCheck: "перевірка якості різна",
		merge.AttributeTags:         "теги різні",
		merge.AttributeCreation:     "дата створення різна",
		merge.AttributeExpiry:       "термін дії різний",
		merge.AttributeAttachments:  "вкладення різні",
		merge.AttributeAutoType:     "Auto-Type різний",
		merge.AttributeCustomData:   "дані розширень різні",
	} {
		var out strings.Builder
		err := report.WriteDifferences(&out, merge.Facts{Entries: []merge.EntryChange{{
			Outcome: merge.OutcomeUpdated,
			Copies:  []merge.CopyState{{Name: "copy", Holds: true, Differences: merge.Difference{Attributes: []merge.Attribute{attribute}}}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), ": "+want+"\n") {
			t.Errorf("attribute %d: no %q in:\n%s", attribute, want, out.String())
		}
	}
}

// A new kind of event must not disappear without a word.
func TestTheReportSaysSoWhenItCannotShowSomething(t *testing.T) {
	var out strings.Builder
	if err := report.WriteDifferences(&out, merge.Facts{Unknown: 2}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "яких цей звіт не вміє показати: 2") {
		t.Errorf("no word about what is missing:\n%s", out.String())
	}
}

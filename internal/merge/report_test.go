package merge_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// printed gives a form of any value that shows what is behind its pointers.
func printed(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// theEntryChange returns the only event of the result, which must be about an entry.
func theEntryChange(t *testing.T, result *merge.Result) merge.EntryChange {
	t.Helper()
	if len(result.Report) != 1 {
		t.Fatalf("got %d events, want 1: %s", len(result.Report), printed(result.Report))
	}
	change, ok := result.Report[0].(merge.EntryChange)
	if !ok {
		t.Fatalf("the event is %T, want merge.EntryChange", result.Report[0])
	}
	return change
}

func TestEqualCopiesGiveAnEmptyReport(t *testing.T) {
	same := database(entry(version(hours(1), "same")))

	result := mergeOf(t, same, same)

	if len(result.Report) != 0 {
		t.Errorf("report = %s, want empty", printed(result.Report))
	}
}

func TestTheReportToldsTheOutcomeOfAnEntry(t *testing.T) {
	older := version(hours(0), "older")
	newer := version(hours(1), "newer")
	longAfter := version(hours(240), "long after")

	for _, tc := range []struct {
		name      string
		snapshots []*snapshot.Snapshot
		want      merge.Outcome
	}{
		{"only in one copy", []*snapshot.Snapshot{database(entry(older)), database()}, merge.OutcomeNew},
		{"lagging", []*snapshot.Snapshot{database(entry(older)), database(entry(newer, older))}, merge.OutcomeUpdated},
		{"long conflict", []*snapshot.Snapshot{database(entry(older)), database(entry(longAfter))}, merge.OutcomeConflict},
		{"close conflict", []*snapshot.Snapshot{database(entry(older)), database(entry(newer))}, merge.OutcomeAsked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := theEntryChange(t, mergeOf(t, tc.snapshots...)).Outcome; got != tc.want {
				t.Errorf("outcome = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTheReportToldsWhenTheHumanChoseAVersion(t *testing.T) {
	in := input(named(twoDivergedCopies(time.Hour)...)...)
	q, _ := theConflict(t, mustRun(t, in))
	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}

	if got := theEntryChange(t, mustRun(t, in)).Outcome; got != merge.OutcomeChosen {
		t.Errorf("outcome = %v, want %v", got, merge.OutcomeChosen)
	}
}

// Each copy in the report: its date, whether another copy has its main version in history,
// and what differs from the result. Sorted by name, so the order of the copies does not count.
func TestTheReportShowsEachCopy(t *testing.T) {
	base := version(hours(0), "base")
	ahead := version(hours(5), "ahead")
	ahead.Tags = "tag"

	result := mustRun(t, input(
		merge.Copy{Name: "b-ahead", Snapshot: database(entry(ahead, base))},
		merge.Copy{Name: "c-missing", Snapshot: database()},
		merge.Copy{Name: "a-behind", Snapshot: database(entry(base))},
	))

	change := theEntryChange(t, result)
	if change.Entry != mailID || change.Label.Title != "Mail" {
		t.Errorf("entry = %s, label = %+v", change.Entry, change.Label)
	}
	names := make([]string, len(change.Copies))
	for i, c := range change.Copies {
		names[i] = c.Name
	}
	if want := []string{"a-behind", "b-ahead", "c-missing"}; !slices.Equal(names, want) {
		t.Fatalf("copies = %q, want %q", names, want)
	}

	behind, top, missing := change.Copies[0], change.Copies[1], change.Copies[2]
	if !behind.Holds || !behind.Behind || !behind.Modified.Equal(hours(0)) {
		t.Errorf("the copy behind = %+v", behind)
	}
	if !slices.Equal(behind.Differences.Fields, []string{"Password"}) ||
		!slices.Equal(behind.Differences.Attributes, []merge.Attribute{merge.AttributeTags}) {
		t.Errorf("the copy behind differs in %+v", behind.Differences)
	}
	if !top.Holds || top.Behind || !top.Modified.Equal(hours(5)) ||
		len(top.Differences.Fields) != 0 || len(top.Differences.Attributes) != 0 {
		t.Errorf("the copy ahead = %+v", top)
	}
	if missing.Holds || !missing.Modified.IsZero() {
		t.Errorf("the copy without the entry = %+v", missing)
	}
}

func TestTheReportIsInAFixedOrder(t *testing.T) {
	second := snapshot.UUID{15: 0x02}
	third := snapshot.UUID{15: 0x03}
	withEntries := database(
		snapshot.Entry{UUID: third, EntryVersion: version(hours(1), "c")},
		entry(version(hours(1), "a")),
		snapshot.Entry{UUID: second, EntryVersion: version(hours(1), "b")},
	)

	result := mergeOf(t, withEntries, database())

	var got []snapshot.UUID
	for _, event := range result.Report {
		got = append(got, event.(merge.EntryChange).Entry)
	}
	if want := []snapshot.UUID{mailID, second, third}; !reflect.DeepEqual(got, want) {
		t.Errorf("entries in the report = %v, want %v", got, want)
	}
}

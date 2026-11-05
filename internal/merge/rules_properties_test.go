package merge_test

import (
	"fmt"
	"reflect"
	"testing"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
)

// TestPropertyTheOrderOfTheRulesInAPhaseChangesNothing: the rules of one phase run in the order
// of the names of their files. So no rule may depend on another rule of its phase, and any other
// order gives the same result.
func TestPropertyTheOrderOfTheRulesInAPhaseChangesNothing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := mergetest.Scenarios().Draw(t, "scenario")
		in := merge.Input{Copies: s.Copies, Now: s.Now, Key: drawKey(t)}
		in.Decisions = drawDecisions(t, runOn(t, in).Questions)
		want := runOn(t, in)

		var order [][]int
		for p, count := range merge.RuleCounts() {
			order = append(order, rapid.Permutation(indexes(count)).Draw(t, fmt.Sprintf("order in phase %d", p)))
		}
		got, err := merge.RunInOrder(in, order)
		if err != nil {
			t.Fatalf("merge.RunInOrder: %v", err)
		}

		wantSameSnapshot(t, got.Merged, want.Merged)
		if !reflect.DeepEqual(got.Questions, want.Questions) {
			t.Errorf("the questions differ:\n got %s\nwant %s", printed(got.Questions), printed(want.Questions))
		}
		if !reflect.DeepEqual(got.Events, want.Events) {
			t.Errorf("the report differs:\n got %s\nwant %s", printed(got.Events), printed(want.Events))
		}
		for _, q := range want.Questions {
			if !reflect.DeepEqual(got.VersionsAsked(q.ID), want.VersionsAsked(q.ID)) {
				t.Errorf("the versions asked about in %s differ", q.ID)
			}
		}
		if !reflect.DeepEqual(got.Absorbed, want.Absorbed) {
			t.Errorf("the absorbed entries differ:\n got %s\nwant %s", printed(got.Absorbed), printed(want.Absorbed))
		}
		if !reflect.DeepEqual(got.MergedGroups, want.MergedGroups) {
			t.Errorf("the merged groups differ:\n got %v\nwant %v", got.MergedGroups, want.MergedGroups)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the result differs")
		}
	})
}

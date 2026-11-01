package merge_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"pgregory.net/rapid"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// login is an entry with its own UUID, for a copy that shares no UUID with another copy.
func login(id byte, title, user, url, password string) snapshot.Entry {
	v := version(hours(1), password)
	v.Fields = []snapshot.Field{
		{Key: "Title", Value: title},
		{Key: "UserName", Value: user},
		{Key: "URL", Value: url},
		{Key: "Password", Value: password, Protected: true},
	}
	return snapshot.Entry{UUID: snapshot.UUID{14: id}, EntryVersion: v}
}

// rootedAt is a copy with its own root group.
func rootedAt(root byte, entries ...snapshot.Entry) *snapshot.Snapshot {
	return &snapshot.Snapshot{Root: snapshot.Group{UUID: snapshot.UUID{0: root}, Entries: entries}}
}

func TestRelatedGroupsByTheRuleOfTheSpec(t *testing.T) {
	mail := func(id byte) snapshot.Entry { return login(id, "Mail", "me", "", "first") }
	bank := func(id byte) snapshot.Entry { return login(id, "Bank", "me", "", "second") }
	shop := func(id byte) snapshot.Entry { return login(id, "Shop", "me", "", "third") }
	game := func(id byte) snapshot.Entry { return login(id, "Game", "me", "", "fourth") }
	zeroRoot := func(entries ...snapshot.Entry) *snapshot.Snapshot {
		return &snapshot.Snapshot{Root: snapshot.Group{Entries: entries}}
	}

	for _, tc := range []struct {
		name   string
		copies []*snapshot.Snapshot
		want   [][]int
	}{
		{"one copy", []*snapshot.Snapshot{rootedAt(1, mail(1))}, [][]int{{0}}},
		{"nothing in common", []*snapshot.Snapshot{rootedAt(1, mail(1)), rootedAt(2, bank(2))}, [][]int{{0}, {1}}},
		{"the same root group", []*snapshot.Snapshot{rootedAt(1, mail(1)), rootedAt(1, bank(2))}, [][]int{{0, 1}}},
		{"a root group with a zero UUID is no root group in common", []*snapshot.Snapshot{zeroRoot(mail(1)), zeroRoot(bank(2))}, [][]int{{0}, {1}}},
		{
			"the same entry under different root groups",
			[]*snapshot.Snapshot{rootedAt(1, mail(1), bank(2), shop(3), game(4)), rootedAt(2, login(1, "Other", "x", "", "y"))},
			[][]int{{0, 1}},
		},
		{
			"an import with half of the entries of the smaller copy similar",
			[]*snapshot.Snapshot{rootedAt(1, mail(1), bank(2)), rootedAt(2, mail(3), shop(4), game(5), login(6, "Forum", "me", "", "x"))},
			[][]int{{0, 1}},
		},
		{
			"a share below a half in both copies",
			[]*snapshot.Snapshot{rootedAt(1, mail(1), bank(2), shop(3)), rootedAt(2, mail(4), game(5), login(6, "Forum", "me", "", "x"))},
			[][]int{{0}, {1}},
		},
		{
			"similar by login and host",
			[]*snapshot.Snapshot{
				rootedAt(1, login(1, "Mail", "me", "https://mail.example.com/a", "x")),
				rootedAt(2, login(2, "Пошта", "me", "mail.example.com/b", "y")),
			},
			[][]int{{0, 1}},
		},
		{
			"similar in another spelling",
			[]*snapshot.Snapshot{rootedAt(1, login(1, " MAIL", "Me ", "", "x")), rootedAt(2, login(2, "mail", "me", "", "y"))},
			[][]int{{0, 1}},
		},
		{
			"the same password makes nothing similar",
			[]*snapshot.Snapshot{rootedAt(1, login(1, "Mail", "me", "", "same")), rootedAt(2, login(2, "Bank", "you", "", "same"))},
			[][]int{{0}, {1}},
		},
		{"two empty new databases", []*snapshot.Snapshot{rootedAt(1), rootedAt(2)}, [][]int{{0}, {1}}},
		{"an empty new database against a full one", []*snapshot.Snapshot{rootedAt(1), rootedAt(2, mail(1))}, [][]int{{0}, {1}}},
		{
			"one copy of three is another database",
			[]*snapshot.Snapshot{rootedAt(1, mail(1)), rootedAt(2, bank(2)), rootedAt(1, shop(3))},
			[][]int{{0, 2}, {1}},
		},
		{
			"a copy links two others that have nothing in common",
			[]*snapshot.Snapshot{rootedAt(1, mail(1)), rootedAt(2, bank(2)), rootedAt(1, bank(2))},
			[][]int{{0, 1, 2}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := merge.RelatedGroups(tc.copies); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("groups = %v, want %v", got, tc.want)
			}
		})
	}
}

// The order of the copies must not decide whether merge stops.
func TestPropertyRelatedGroupsDoNotDependOnTheOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		copies := snapshotsOf(mergetest.Scenarios().Draw(t, "scenario"))
		order := rapid.Permutation(indexes(len(copies))).Draw(t, "order")
		shuffled := make([]*snapshot.Snapshot, len(copies))
		for i, from := range order {
			shuffled[i] = copies[from]
		}

		want := partition(merge.RelatedGroups(copies), copies)
		if got := partition(merge.RelatedGroups(shuffled), shuffled); !slices.Equal(got, want) {
			t.Fatalf("groups %v in order %v, want %v", got, order, want)
		}
	})
}

func snapshotsOf(s mergetest.Scenario) []*snapshot.Snapshot {
	result := make([]*snapshot.Snapshot, len(s.Copies))
	for i := range s.Copies {
		result[i] = s.Copies[i].Snapshot
	}
	return result
}

func indexes(n int) []int {
	result := make([]int, n)
	for i := range result {
		result[i] = i
	}
	return result
}

// partition gives the groups in a form that does not depend on the positions of the copies.
func partition(groups [][]int, copies []*snapshot.Snapshot) []string {
	result := make([]string, 0, len(groups))
	for _, group := range groups {
		members := make([]string, 0, len(group))
		for _, i := range group {
			members = append(members, fmt.Sprintf("%p", copies[i]))
		}
		slices.Sort(members)
		result = append(result, fmt.Sprint(members))
	}
	slices.Sort(result)
	return result
}

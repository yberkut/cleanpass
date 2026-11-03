package merge_test

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

var (
	firstID  = snapshot.UUID{15: 0x11}
	secondID = snapshot.UUID{15: 0x12}
	thirdID  = snapshot.UUID{15: 0x13}
)

// account is an entry with one version, at hours(1), unless a test changes it.
func account(id snapshot.UUID, title, user, url, password string) snapshot.Entry {
	v := version(hours(1), password)
	v.Fields = []snapshot.Field{
		{Key: "Title", Value: title},
		{Key: "UserName", Value: user},
		{Key: "Password", Value: password, Protected: true},
		{Key: "URL", Value: url},
	}
	return snapshot.Entry{UUID: id, EntryVersion: v}
}

// pairQuestions gives the questions about one similar pair, by the UUIDs of the pair.
func pairQuestions(result *merge.Result) map[[2]snapshot.UUID]merge.Question {
	found := map[[2]snapshot.UUID]merge.Question{}
	for _, q := range result.Questions {
		if pair, ok := q.Detail.(merge.SimilarPair); ok {
			found[[2]snapshot.UUID{pair.First.Entry, pair.Second.Entry}] = q
		}
	}
	return found
}

func TestWhatMakesTwoEntriesSimilar(t *testing.T) {
	type names struct{ title, user, url string }
	for _, tc := range []struct {
		name string
		a, b names
		want bool
	}{
		{"the same title and user name", names{"Mail", "user", ""}, names{"Mail", "user", ""}, true},
		{"title and user name in another case", names{"MAIL", "USER", ""}, names{"mail", "user", ""}, true},
		{"title and user name in Cyrillic in another case", names{"ПОШТА", "ЯРОСЛАВ", ""}, names{"пошта", "ярослав", ""}, true},
		{"spaces around title and user name", names{"  Mail\t", " user ", ""}, names{"Mail", "user", ""}, true},
		{"the same title and no user name", names{"Wi-Fi", "", ""}, names{"Wi-Fi", "", ""}, true},
		{"the same title, another user name", names{"Mail", "user", ""}, names{"Mail", "admin", ""}, false},
		{"another title, the same user name, no URL", names{"Mail", "user", ""}, names{"Post", "user", ""}, false},
		{"spaces inside a title", names{"Ma il", "user", ""}, names{"Mail", "user", ""}, false},

		{"the same user name and host", names{"Mail", "user", "https://example.com"}, names{"Post", "user", "https://example.com"}, true},
		{"the user name in another case", names{"Mail", "USER", "https://example.com"}, names{"Post", " user ", "https://example.com"}, true},
		{"the same host on another path", names{"Mail", "user", "https://example.com/a"}, names{"Post", "user", "https://example.com/b?c=d"}, true},
		{"the same host on another scheme and port", names{"Mail", "user", "https://example.com"}, names{"Post", "user", "http://example.com:8080"}, true},
		{"the host in another case", names{"Mail", "user", "https://EXAMPLE.com"}, names{"Post", "user", "https://example.COM"}, true},
		{"a URL with no scheme", names{"Mail", "user", "example.com/login"}, names{"Post", "user", "https://example.com"}, true},
		{"spaces around a URL", names{"Mail", "user", " https://example.com "}, names{"Post", "user", "https://example.com"}, true},
		{"another host", names{"Mail", "user", "https://example.com"}, names{"Post", "user", "https://mail.example.com"}, false},
		{"the same host, another user name", names{"Mail", "user", "https://example.com"}, names{"Post", "admin", "https://example.com"}, false},

		// An empty value says nothing about the account. Without this rule all notes with no
		// user name and no URL would be one big family of pairs.
		{"no user name and no URL", names{"Note one", "", ""}, names{"Note two", "", ""}, false},
		{"the same host and no user name", names{"Mail", "", "https://example.com"}, names{"Post", "", "https://example.com"}, false},
		{"the same user name and no host in a URL that is no URL", names{"Mail", "user", "a b"}, names{"Post", "user", "a b"}, false},
		{"no title and the same user name", names{"", "user", ""}, names{"", "user", ""}, false},
		{"nothing at all", names{"", "", ""}, names{"", "", ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := mergeOf(t,
				database(account(firstID, tc.a.title, tc.a.user, tc.a.url, "one")),
				database(account(secondID, tc.b.title, tc.b.user, tc.b.url, "two")),
			)

			if _, got := pairQuestions(result)[[2]snapshot.UUID{firstID, secondID}]; got != tc.want {
				t.Errorf("a question about the pair = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOneEntryInTwoCopiesIsNoPair(t *testing.T) {
	same := account(firstID, "Mail", "user", "", "one")

	result := mergeOf(t, database(same), database(same))

	wantNoQuestions(t, result)
}

func TestTwoEntriesInOneCopyCanBeAPair(t *testing.T) {
	result := mergeOf(t, database(
		account(firstID, "Mail", "user", "", "one"),
		account(secondID, "Mail", "user", "", "two"),
	))

	if len(pairQuestions(result)) != 1 {
		t.Errorf("questions: %s", printed(result.Questions))
	}
}

// The password takes no part in the search: that would give false pairs and show which
// accounts share a password.
func TestTheSamePasswordDoesNotMakeAPair(t *testing.T) {
	result := mergeOf(t,
		database(account(firstID, "Mail", "user", "https://mail.example.com", "shared")),
		database(account(secondID, "Forum", "admin", "https://forum.example.com", "shared")),
	)

	wantNoQuestions(t, result)
}

// A protected value is a secret, whatever its field is called. So it takes no part either: the
// two values below differ, and to the search they do not exist.
func TestAProtectedValueTakesNoPartInTheSearch(t *testing.T) {
	protect := func(e snapshot.Entry, key string) snapshot.Entry {
		e.Fields = slices.Clone(e.Fields)
		for i := range e.Fields {
			if e.Fields[i].Key == key {
				e.Fields[i].Protected = true
			}
		}
		return e
	}
	sameSecretLogin := mergeOf(t,
		database(protect(account(firstID, "Mail", "secret-login", "https://example.com", "one"), "UserName")),
		database(protect(account(secondID, "Post", "secret-login", "https://example.com", "two"), "UserName")),
	)
	wantNoQuestions(t, sameSecretLogin)

	seenAsEmpty := mergeOf(t,
		database(protect(account(firstID, "Mail", "secret-one", "", "one"), "UserName")),
		database(protect(account(secondID, "Mail", "secret-two", "", "two"), "UserName")),
	)
	if len(pairQuestions(seenAsEmpty)) != 1 {
		t.Errorf("questions: %s", printed(seenAsEmpty.Questions))
	}
}

// A question stays the same whether another question has an answer or not, so one pass over the
// questions is enough, and an answer about a pair never loses its question.
func TestAnAnswerAboutAConflictChangesNoQuestionAboutAPair(t *testing.T) {
	newer := account(firstID, "Mail", "user", "", "newer")
	newer.Times.LastModification = hours(2)
	older := account(firstID, "Post", "user", "", "older")
	similarToNewer := account(secondID, "Mail", "user", "", "other")
	in := input(named(database(newer, similarToNewer), database(older))...)
	before := mustRun(t, in)
	asked := pairQuestions(before)[[2]snapshot.UUID{firstID, secondID}]
	var conflictAnswer merge.Decision
	for _, q := range before.Questions {
		if _, ok := q.Detail.(merge.Conflict); ok {
			conflictAnswer = merge.Decision{Question: q.ID, Choice: 1}
		}
	}

	in.Decisions = []merge.Decision{conflictAnswer, {Question: asked.ID, Choice: 0}}
	after := mustRun(t, in)

	if !reflect.DeepEqual(pairQuestions(after), pairQuestions(before)) {
		t.Errorf("the pair questions changed:\n got %s\nwant %s", printed(after.Questions), printed(before.Questions))
	}
	if got := fieldValue(&allEntries(after.Merged)[firstID].EntryVersion, "Title"); got != "Post" {
		t.Errorf("the entry has title %q, want the one the answer chose", got)
	}
}

// answerPair runs the merge again with the answer to the question about the first two entries.
func answerPair(t *testing.T, in merge.Input, first, second snapshot.UUID, choice int) *merge.Result {
	t.Helper()
	q, asked := pairQuestions(mustRun(t, in))[[2]snapshot.UUID{first, second}]
	if !asked {
		t.Fatalf("no question about the pair %s and %s", first, second)
	}
	in.Decisions = append(slices.Clone(in.Decisions), merge.Decision{Question: q.ID, Choice: choice})
	return mustRun(t, in)
}

// Answer 1 means "merge": the versions of both entries end up under the smaller UUID.
func TestMergingAPairJoinsTheVersionsUnderTheSmallerUUID(t *testing.T) {
	v1, v2, v3, v4 := version(hours(1), "v1"), version(hours(2), "v2"), version(hours(3), "v3"), version(hours(4), "v4")
	a := snapshot.Entry{UUID: firstID, EntryVersion: v3, History: []snapshot.EntryVersion{v1}}
	b := snapshot.Entry{UUID: secondID, EntryVersion: v4, History: []snapshot.EntryVersion{v2}}

	for name, snapshots := range map[string][]*snapshot.Snapshot{
		"in this order of the copies": {database(a), database(b)},
		"in the other order":          {database(b), database(a)},
		"both in one copy":            {database(a, b)},
		"both in one copy, other way": {database(b, a)},
	} {
		t.Run(name, func(t *testing.T) {
			result := answerPair(t, input(named(snapshots...)...), firstID, secondID, 1)

			e := onlyEntry(t, result.Merged)
			if e.UUID != firstID {
				t.Errorf("the merged entry has UUID %s, want the smaller one", e.UUID)
			}
			wantVersions(t, e, "v4", "v1", "v2", "v3")
		})
	}
}

func TestAnAnswerToKeepBothKeepsBoth(t *testing.T) {
	in := input(named(
		database(account(firstID, "Mail", "user", "", "one")),
		database(account(secondID, "Mail", "user", "", "two")),
	)...)

	merged := allEntries(answerPair(t, in, firstID, secondID, 0).Merged)

	if merged[firstID] == nil || merged[secondID] == nil {
		t.Errorf("the result has %d entries, want both", len(merged))
	}
}

// thePair returns the only question about a pair and its detail.
func thePair(t *testing.T, result *merge.Result) (merge.Question, merge.SimilarPair) {
	t.Helper()
	asked := pairQuestions(result)
	if len(asked) != 1 {
		t.Fatalf("got %d questions about pairs, want 1: %s", len(asked), printed(result.Questions))
	}
	for _, q := range asked {
		return q, q.Detail.(merge.SimilarPair)
	}
	panic("unreachable")
}

func TestAPairQuestionShowsBothEntries(t *testing.T) {
	a := account(firstID, "Mail", "user", "https://example.com/a", "one")
	a.Times.LastModification = hours(3)
	b := account(secondID, "POST", "USER", "http://example.com/b", "two")

	result := mustRun(t, input(
		merge.Copy{Name: "b-copy", Snapshot: database(a, b)},
		merge.Copy{Name: "a-copy", Snapshot: database(b)},
	))

	q, pair := thePair(t, result)
	if want := merge.QuestionID("pair:" + firstID.String() + ":" + secondID.String()); q.ID != want {
		t.Errorf("ID = %s, want %s", q.ID, want)
	}
	if q.Choices != 2 {
		t.Errorf("choices = %d, want 2", q.Choices)
	}
	wantFirst := merge.PairSide{
		Entry:    firstID,
		Label:    merge.Label{Title: "Mail", UserName: "user", URL: "https://example.com/a"},
		Modified: hours(3),
		Copies:   []string{"b-copy"},
	}
	wantSecond := merge.PairSide{
		Entry:    secondID,
		Label:    merge.Label{Title: "POST", UserName: "USER", URL: "http://example.com/b"},
		Modified: hours(1),
		Copies:   []string{"a-copy", "b-copy"},
	}
	if !reflect.DeepEqual(pair.First, wantFirst) {
		t.Errorf("first = %+v, want %+v", pair.First, wantFirst)
	}
	if !reflect.DeepEqual(pair.Second, wantSecond) {
		t.Errorf("second = %+v, want %+v", pair.Second, wantSecond)
	}
	if want := []string{"Password", "Title", "URL", "UserName"}; !slices.Equal(pair.Differences.Fields, want) {
		t.Errorf("differences = %+v, want the fields %q", pair.Differences, want)
	}
}

func TestAPairQuestionTellsWhyThePairIsSimilar(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		a, b                       snapshot.Entry
		wantTitleAndUser, wantHost bool
	}{
		{
			"title and user name",
			account(firstID, "Mail", "user", "", "same"), account(secondID, "Mail", "user", "", "same"),
			true, false,
		},
		{
			"user name and host",
			account(firstID, "Mail", "user", "https://example.com", "same"), account(secondID, "Post", "user", "https://example.com", "same"),
			false, true,
		},
		{
			"both",
			account(firstID, "Mail", "user", "https://example.com", "same"), account(secondID, "Mail", "user", "https://example.com", "same"),
			true, true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, pair := thePair(t, mergeOf(t, database(tc.a), database(tc.b)))

			if pair.SameTitleAndUser != tc.wantTitleAndUser || pair.SameUserAndHost != tc.wantHost {
				t.Errorf("same title and user = %v, same user and host = %v, want %v and %v",
					pair.SameTitleAndUser, pair.SameUserAndHost, tc.wantTitleAndUser, tc.wantHost)
			}
		})
	}
}

func TestAPairQuestionTellsWhetherThePasswordIsTheSame(t *testing.T) {
	withoutPassword := func(id snapshot.UUID) snapshot.Entry {
		e := account(id, "Mail", "user", "", "")
		e.Fields = slices.DeleteFunc(slices.Clone(e.Fields), func(f snapshot.Field) bool { return f.Key == "Password" })
		return e
	}
	notProtected := account(secondID, "Mail", "user", "", "same")
	notProtected.Fields = slices.Clone(notProtected.Fields)
	for i := range notProtected.Fields {
		notProtected.Fields[i].Protected = false
	}

	for _, tc := range []struct {
		name string
		a, b snapshot.Entry
		want bool
	}{
		{"the same", account(firstID, "Mail", "user", "", "same"), account(secondID, "Mail", "user", "", "same"), true},
		{"different", account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "two"), false},
		{"another case is another password", account(firstID, "Mail", "user", "", "one"), account(secondID, "Mail", "user", "", "ONE"), false},
		{"both with none", withoutPassword(firstID), withoutPassword(secondID), true},
		{"one with none", withoutPassword(firstID), account(secondID, "Mail", "user", "", "two"), false},
		{"the same value with another protection", account(firstID, "Mail", "user", "", "same"), notProtected, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, pair := thePair(t, mergeOf(t, database(tc.a), database(tc.b)))

			if pair.SamePassword != tc.want {
				t.Errorf("same password = %v, want %v", pair.SamePassword, tc.want)
			}
		})
	}
}

// The data of a question holds nothing of a password but "the same or not": other passwords,
// of any length and with any bytes, must give exactly the same data. A value, a part of it, its
// length or a hash of it would change something here. The test looks at all fields of the
// values, not only at what a printed form shows.
func TestAPairQuestionHoldsNothingOfThePasswordButTheAnswerSameOrNot(t *testing.T) {
	askWith := func(one, two string) merge.Question {
		q, _ := thePair(t, mergeOf(t,
			database(account(firstID, "Mail", "user", "", one)),
			database(account(secondID, "Mail", "user", "", two)),
		))
		return q
	}

	if !reflect.DeepEqual(askWith("a", "b"), askWith("a much longer password", "ключ")) {
		t.Errorf("the question depends on the values of two different passwords")
	}
	if !reflect.DeepEqual(askWith("same", "same"), askWith("another much longer one", "another much longer one")) {
		t.Errorf("the question depends on the value of the same password")
	}
}

// The merged entry lies where the newest move of either entry put it.
func TestAMergedPairLiesWhereTheNewestMovePutIt(t *testing.T) {
	workID, homeID := snapshot.UUID{15: 0xA1}, snapshot.UUID{15: 0xA2}
	inGroup := func(groupID snapshot.UUID, e snapshot.Entry) *snapshot.Snapshot {
		s := database()
		s.Root.Groups = []snapshot.Group{{UUID: groupID, Name: "Group " + groupID.String(), Entries: []snapshot.Entry{e}}}
		return s
	}

	for _, tc := range []struct {
		name                    string
		movedFirst, movedSecond time.Time
		want                    snapshot.UUID
	}{
		{"the absorbed entry moved last", hours(1), hours(2), homeID},
		{"the kept entry moved last", hours(2), hours(1), workID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := account(firstID, "Mail", "user", "", "one")
			a.Times.LocationChanged = tc.movedFirst
			b := account(secondID, "Mail", "user", "", "two")
			b.Times.LocationChanged = tc.movedSecond
			in := input(named(inGroup(workID, a), inGroup(homeID, b))...)

			merged := answerPair(t, in, firstID, secondID, 1).Merged

			groups := groupsWithEntry(merged, firstID)
			if len(groups) != 1 || groups[0].UUID != tc.want {
				t.Errorf("the entry lies in %d groups, the first one %s, want %s", len(groups), groups[0].UUID, tc.want)
			}
			if got := onlyEntry(t, merged).Times.LocationChanged; !got.Equal(later(tc.movedFirst, tc.movedSecond)) {
				t.Errorf("the entry says it moved at %s", got)
			}
		})
	}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Reads of either entry count: usage joins as a maximum, as it does for one UUID in two copies.
func TestAMergedPairHasTheUsageOfBoth(t *testing.T) {
	a := account(firstID, "Mail", "user", "", "one")
	a.Times.LastModification, a.Times.LastAccess, a.Times.UsageCount = hours(2), hours(3), 2
	b := account(secondID, "Mail", "user", "", "two")
	b.Times.LastModification, b.Times.LastAccess, b.Times.UsageCount = hours(1), hours(9), 7
	in := input(named(database(a), database(b))...)

	merged := onlyEntry(t, answerPair(t, in, firstID, secondID, 1).Merged)

	if !merged.Times.LastAccess.Equal(hours(9)) || merged.Times.UsageCount != 7 {
		t.Errorf("times = %+v, want the last access and the count of the other entry", merged.Times)
	}
}

func TestAMergedPairKeepsTheAttachmentsOfBoth(t *testing.T) {
	first, second := []byte("first bytes"), []byte("second bytes")
	withAttachment := func(e snapshot.Entry, content []byte) *snapshot.Snapshot {
		id := snapshot.NewAttachmentID(content)
		e.Attachments = []snapshot.AttachmentRef{{Name: "file.bin", ID: id}}
		s := database(e)
		s.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{id: {Content: content}}
		return s
	}
	in := input(named(
		withAttachment(account(firstID, "Mail", "user", "", "one"), first),
		withAttachment(account(secondID, "Mail", "user", "", "one"), second),
	)...)

	merged := answerPair(t, in, firstID, secondID, 1).Merged

	for _, content := range [][]byte{first, second} {
		if got := merged.Attachments[snapshot.NewAttachmentID(content)].Content; !bytes.Equal(got, content) {
			t.Errorf("the attachment %q is gone, got %q", content, got)
		}
	}
}

// The absorbed UUIDs leave the tree, so whoever checks or remembers the merge must learn them
// from the result.
func TestTheResultNamesWhatAMergedPairAbsorbed(t *testing.T) {
	in := input(named(
		database(account(firstID, "Mail", "user", "", "one")),
		database(account(secondID, "Mail", "user", "", "two")),
	)...)

	if got := mustRun(t, in).Absorbed; len(got) != 0 {
		t.Errorf("without an answer, absorbed = %+v, want none", got)
	}

	result := answerPair(t, in, firstID, secondID, 1)

	want := []merge.Absorption{{Absorbed: secondID, Into: firstID}}
	if !reflect.DeepEqual(result.Absorbed, want) {
		t.Errorf("absorbed = %+v, want %+v", result.Absorbed, want)
	}
}

func TestTheReportTellsAboutAMergedPair(t *testing.T) {
	in := input(named(
		database(account(firstID, "Mail", "user", "", "one")),
		database(account(secondID, "Mail", "user", "", "two")),
	)...)

	result := answerPair(t, in, firstID, secondID, 1)

	var got []merge.PairMerged
	for _, event := range result.Events {
		if merged, ok := event.(merge.PairMerged); ok {
			got = append(got, merged)
		}
	}
	want := []merge.PairMerged{{
		Entry:    firstID,
		Label:    merge.Label{Title: "Mail", UserName: "user"},
		Absorbed: []snapshot.UUID{secondID},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %s, want %s", printed(got), printed(want))
	}
}

func TestABadAnswerAboutAPairIsAnError(t *testing.T) {
	in := input(named(
		database(account(firstID, "Mail", "user", "", "one")),
		database(account(secondID, "Mail", "user", "", "two")),
	)...)
	q, _ := thePair(t, mustRun(t, in))

	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 2}}
	if _, err := merge.Run(in); !errors.Is(err, merge.ErrBadChoice) {
		t.Errorf("err = %v, want %v", err, merge.ErrBadChoice)
	}
}

// The ID stays the same over runs: it does not depend on the key of the run or on the order of
// the copies, and an entry keeps it whichever copy it comes from.
func TestTheIDOfAPairQuestionIsStable(t *testing.T) {
	a := database(account(firstID, "Mail", "user", "", "one"))
	b := database(account(secondID, "Mail", "user", "", "two"))
	want := merge.QuestionID("pair:00000000000000000000000000000011:00000000000000000000000000000012")

	for name, in := range map[string]merge.Input{
		"one order":   input(named(a, b)...),
		"other order": input(named(b, a)...),
		"another key": {Copies: named(a, b), Now: now, Key: merge.Key{9, 9, 9}},
		"another day": {Copies: named(a, b), Now: now.Add(day), Key: testKey},
	} {
		t.Run(name, func(t *testing.T) {
			if q, _ := thePair(t, mustRun(t, in)); q.ID != want {
				t.Errorf("ID = %s, want %s", q.ID, want)
			}
		})
	}
}

// answerPairs runs the merge with one answer for each of the given pairs.
func answerPairs(t *testing.T, in merge.Input, answers map[[2]snapshot.UUID]int) *merge.Result {
	t.Helper()
	asked := pairQuestions(mustRun(t, in))
	for pair, choice := range answers {
		q, ok := asked[pair]
		if !ok {
			t.Fatalf("no question about the pair %v", pair)
		}
		in.Decisions = append(in.Decisions, merge.Decision{Question: q.ID, Choice: choice})
	}
	return mustRun(t, in)
}

// threeOfOneAccount holds three entries that are all similar to each other.
func threeOfOneAccount() merge.Input {
	first := account(firstID, "Mail", "user", "", "one")
	second := account(secondID, "Mail", "user", "", "two")
	third := account(thirdID, "Mail", "user", "", "three")
	first.Times.LastModification, second.Times.LastModification = hours(3), hours(2)
	return input(named(database(first), database(second), database(third))...)
}

func TestThreeSimilarEntriesGiveThreeQuestions(t *testing.T) {
	asked := pairQuestions(mustRun(t, threeOfOneAccount()))

	for _, pair := range [][2]snapshot.UUID{{firstID, secondID}, {firstID, thirdID}, {secondID, thirdID}} {
		if _, ok := asked[pair]; !ok {
			t.Errorf("no question about the pair %v", pair)
		}
	}
}

// Two answers that share an entry make one entry of all three.
func TestMergedPairsThatShareAnEntryBecomeOneEntry(t *testing.T) {
	for name, answers := range map[string]map[[2]snapshot.UUID]int{
		"through the smallest": {{firstID, secondID}: 1, {firstID, thirdID}: 1},
		"through the middle":   {{firstID, secondID}: 1, {secondID, thirdID}: 1},
		"through the largest":  {{firstID, thirdID}: 1, {secondID, thirdID}: 1},
		"all three answers":    {{firstID, secondID}: 1, {firstID, thirdID}: 1, {secondID, thirdID}: 1},
	} {
		t.Run(name, func(t *testing.T) {
			result := answerPairs(t, threeOfOneAccount(), answers)

			e := onlyEntry(t, result.Merged)
			if e.UUID != firstID {
				t.Errorf("the entry has UUID %s, want the smallest one", e.UUID)
			}
			wantVersions(t, e, "one", "three", "two")
			wantAbsorbed := []merge.Absorption{{Absorbed: secondID, Into: firstID}, {Absorbed: thirdID, Into: firstID}}
			if !reflect.DeepEqual(result.Absorbed, wantAbsorbed) {
				t.Errorf("absorbed = %+v, want %+v", result.Absorbed, wantAbsorbed)
			}
			want := []merge.Event{merge.PairMerged{
				Entry:    firstID,
				Label:    merge.Label{Title: "Mail", UserName: "user"},
				Absorbed: []snapshot.UUID{secondID, thirdID},
			}}
			if got := pairEvents(result); !reflect.DeepEqual(got, want) {
				t.Errorf("events = %s, want %s", printed(got), printed(want))
			}
		})
	}
}

// pairEvents gives the events about pairs.
func pairEvents(result *merge.Result) []merge.Event {
	var events []merge.Event
	for _, event := range result.Events {
		switch event.(type) {
		case merge.PairMerged, merge.PairNotMerged:
			events = append(events, event)
		}
	}
	return events
}

// "Keep both" is a promise: two accounts must not end up as one through a third entry, so a merge
// that would break the promise does not happen, and the report says so. No data depends on it.
func TestAnAnswerToKeepBothStopsAMergeThroughAThirdEntry(t *testing.T) {
	for name, tc := range map[string]struct {
		answers    map[[2]snapshot.UUID]int
		wantEntry  []snapshot.UUID
		wantStayed [2]snapshot.UUID
	}{
		"the last merge would break it": {
			map[[2]snapshot.UUID]int{{firstID, secondID}: 1, {firstID, thirdID}: 1, {secondID, thirdID}: 0},
			[]snapshot.UUID{firstID, thirdID},
			[2]snapshot.UUID{firstID, thirdID},
		},
		"the promise is about the two larger ones": {
			map[[2]snapshot.UUID]int{{firstID, thirdID}: 1, {secondID, thirdID}: 1, {firstID, secondID}: 0},
			[]snapshot.UUID{firstID, secondID},
			[2]snapshot.UUID{secondID, thirdID},
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := answerPairs(t, threeOfOneAccount(), tc.answers)

			merged := allEntries(result.Merged)
			if len(merged) != len(tc.wantEntry) {
				t.Errorf("the result has %d entries, want %d", len(merged), len(tc.wantEntry))
			}
			for _, id := range tc.wantEntry {
				if merged[id] == nil {
					t.Errorf("the entry %s is gone", id)
				}
			}
			var notMerged []merge.PairNotMerged
			for _, event := range pairEvents(result) {
				if e, ok := event.(merge.PairNotMerged); ok {
					notMerged = append(notMerged, e)
				}
			}
			if len(notMerged) != 1 || notMerged[0].First.Entry != tc.wantStayed[0] || notMerged[0].Second.Entry != tc.wantStayed[1] {
				t.Errorf("not merged = %s, want the pair %v", printed(notMerged), tc.wantStayed)
			}
		})
	}
}

// theBulkQuestion returns the question about all pairs with the same content.
func theBulkQuestion(t *testing.T, result *merge.Result) (merge.Question, merge.IdenticalPairs) {
	t.Helper()
	for _, q := range result.Questions {
		if detail, ok := q.Detail.(merge.IdenticalPairs); ok {
			return q, detail
		}
	}
	t.Fatalf("no question about all pairs with the same content: %s", printed(result.Questions))
	panic("unreachable")
}

// What counts as the same content: what a person would call the same entry. An import writes
// new times, and the merge keeps the versions anyway, so times and history do not count.
func TestWhichPairsHaveTheSameContent(t *testing.T) {
	base := func() snapshot.Entry { return account(secondID, "Mail", "user", "https://example.com", "same") }
	for _, tc := range []struct {
		name   string
		change func(e *snapshot.Entry)
		want   bool
	}{
		{"nothing", func(*snapshot.Entry) {}, true},
		{"the time of the change", func(e *snapshot.Entry) { e.Times.LastModification = hours(9) }, true},
		{"the time of creation", func(e *snapshot.Entry) { e.Times.Creation = hours(9) }, true},
		{"the time of the last read", func(e *snapshot.Entry) { e.Times.LastAccess = hours(9) }, true},
		{"the count of reads", func(e *snapshot.Entry) { e.Times.UsageCount = 5 }, true},
		{"the time of the last move", func(e *snapshot.Entry) { e.Times.LocationChanged = hours(9) }, true},
		{"the history", func(e *snapshot.Entry) { e.History = []snapshot.EntryVersion{version(hours(0), "old")} }, true},
		{"the order of the fields", func(e *snapshot.Entry) { slices.Reverse(e.Fields) }, true},
		{"service custom data", func(e *snapshot.Entry) {
			e.CustomData = []snapshot.CustomDataItem{{Key: snapshot.CustomDataLastModified, Value: "today"}}
		}, true},

		{"the password", func(e *snapshot.Entry) { e.Fields[2].Value = "other" }, false},
		{"the case of the title", func(e *snapshot.Entry) { e.Fields[0].Value = "MAIL" }, false},
		{"spaces around the user name", func(e *snapshot.Entry) { e.Fields[1].Value = " user " }, false},
		{"the path of the URL", func(e *snapshot.Entry) { e.Fields[3].Value = "https://example.com/path" }, false},
		{"one more field", func(e *snapshot.Entry) { e.Fields = append(e.Fields, snapshot.Field{Key: "Notes", Value: "text"}) }, false},
		{"the tags", func(e *snapshot.Entry) { e.Tags = "tag" }, false},
		{"the icon", func(e *snapshot.Entry) { e.IconID = 3 }, false},
		{"a color", func(e *snapshot.Entry) { e.BackgroundColor = "#112233" }, false},
		{"an attachment", func(e *snapshot.Entry) { e.Attachments = []snapshot.AttachmentRef{{Name: "file.bin"}} }, false},
		{"Auto-Type", func(e *snapshot.Entry) { e.AutoType.Enabled = true }, false},
		{"custom data", func(e *snapshot.Entry) { e.CustomData = []snapshot.CustomDataItem{{Key: "KPXC_BROWSER", Value: "x"}} }, false},
		{"the expiry", func(e *snapshot.Entry) { e.Times.Expires = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := base()
			changed.Fields = slices.Clone(changed.Fields)
			tc.change(&changed)

			_, pair := thePair(t, mergeOf(t, database(account(firstID, "Mail", "user", "https://example.com", "same")), database(changed)))

			if pair.SameContent != tc.want {
				t.Errorf("same content = %v, want %v, differences %+v", pair.SameContent, tc.want, pair.Differences)
			}
		})
	}
}

// bulkInput holds three pairs: one with the same content, one in another spelling and one with another password.
func bulkInput() (merge.Input, map[string][2]snapshot.UUID) {
	id := func(n byte) snapshot.UUID { return snapshot.UUID{15: n} }
	in := input(named(
		database(
			account(id(1), "Mail", "user", "", "same"),
			account(id(3), "Bank", "user", "", "same"),
			account(id(5), "Git", "user", "", "one"),
		),
		database(
			account(id(2), "mail", "user", "", "same"),
			account(id(4), "Bank", "user", "", "same"),
			account(id(6), "Git", "user", "", "two"),
		),
	)...)
	return in, map[string][2]snapshot.UUID{
		"another case":     {id(1), id(2)},
		"the same":         {id(3), id(4)},
		"another password": {id(5), id(6)},
	}
}

func TestOneAnswerMergesAllPairsWithTheSameContentAndNoOthers(t *testing.T) {
	in, pairs := bulkInput()
	q, detail := theBulkQuestion(t, mustRun(t, in))
	asked := pairQuestions(mustRun(t, in))
	if want := []merge.QuestionID{asked[pairs["the same"]].ID}; !slices.Equal(detail.Pairs, want) {
		t.Errorf("the question is about %q, want %q", detail.Pairs, want)
	}
	if q.Choices != 2 {
		t.Errorf("choices = %d, want 2", q.Choices)
	}

	in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}}
	result := mustRun(t, in)

	merged := allEntries(result.Merged)
	if merged[pairs["the same"][0]] == nil || merged[pairs["the same"][1]] != nil {
		t.Errorf("the pair with the same content did not become one entry")
	}
	for _, name := range []string{"another case", "another password"} {
		if merged[pairs[name][0]] == nil || merged[pairs[name][1]] == nil {
			t.Errorf("the pair with %s lost an entry", name)
		}
	}
}

func TestWithoutAnAnswerTheBulkQuestionMergesNothing(t *testing.T) {
	in, _ := bulkInput()
	q, _ := theBulkQuestion(t, mustRun(t, in))

	for name, decisions := range map[string][]merge.Decision{
		"no answer": nil,
		"answer 0":  {{Question: q.ID, Choice: 0}},
	} {
		t.Run(name, func(t *testing.T) {
			in.Decisions = decisions
			if merged := allEntries(mustRun(t, in).Merged); len(merged) != 6 {
				t.Errorf("the result has %d entries, want all 6", len(merged))
			}
		})
	}
}

// One answer about one pair says more than one answer about all pairs.
func TestAnAnswerAboutOnePairBeatsTheBulkAnswer(t *testing.T) {
	in, pairs := bulkInput()
	asked := pairQuestions(mustRun(t, in))
	q, _ := theBulkQuestion(t, mustRun(t, in))

	t.Run("keep both", func(t *testing.T) {
		in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}, {Question: asked[pairs["the same"]].ID, Choice: 0}}
		if merged := allEntries(mustRun(t, in).Merged); len(merged) != 6 {
			t.Errorf("the result has %d entries, want all 6", len(merged))
		}
	})
	t.Run("merge a pair with another content", func(t *testing.T) {
		in.Decisions = []merge.Decision{{Question: q.ID, Choice: 1}, {Question: asked[pairs["another password"]].ID, Choice: 1}}
		if merged := allEntries(mustRun(t, in).Merged); len(merged) != 4 {
			t.Errorf("the result has %d entries, want 4", len(merged))
		}
	})
	t.Run("merge a pair and say no to the bulk answer", func(t *testing.T) {
		in.Decisions = []merge.Decision{{Question: q.ID, Choice: 0}, {Question: asked[pairs["the same"]].ID, Choice: 1}}
		if merged := allEntries(mustRun(t, in).Merged); len(merged) != 5 {
			t.Errorf("the result has %d entries, want 5", len(merged))
		}
	})
}

// The Loss Audit learns from the decisions alone which entries may become one, not from the
// result. An answer about one pair beats the answer about all pairs here too.
func TestTheDecisionsNameThePairsTheyLetMerge(t *testing.T) {
	in, pairs := bulkInput()
	questions := mustRun(t, in).Questions
	asked := pairQuestions(mustRun(t, in))
	bulk, _ := theBulkQuestion(t, mustRun(t, in))
	same, other := asked[pairs["the same"]].ID, asked[pairs["another password"]].ID

	for name, tc := range map[string]struct {
		decisions []merge.Decision
		want      [][2]snapshot.UUID
	}{
		"no answer":           {nil, nil},
		"merge one pair":      {[]merge.Decision{{Question: other, Choice: 1}}, [][2]snapshot.UUID{pairs["another password"]}},
		"keep one pair":       {[]merge.Decision{{Question: other, Choice: 0}}, nil},
		"merge all the same":  {[]merge.Decision{{Question: bulk.ID, Choice: 1}}, [][2]snapshot.UUID{pairs["the same"]}},
		"no to all the same":  {[]merge.Decision{{Question: bulk.ID, Choice: 0}}, nil},
		"one pair beats all":  {[]merge.Decision{{Question: bulk.ID, Choice: 1}, {Question: same, Choice: 0}}, nil},
		"both kinds together": {[]merge.Decision{{Question: bulk.ID, Choice: 1}, {Question: other, Choice: 1}}, [][2]snapshot.UUID{pairs["another password"], pairs["the same"]}},
	} {
		t.Run(name, func(t *testing.T) {
			got := merge.PairsToMerge(questions, tc.decisions)
			slices.SortFunc(got, func(a, b [2]snapshot.UUID) int { return bytes.Compare(a[0][:], b[0][:]) })
			slices.SortFunc(tc.want, func(a, b [2]snapshot.UUID) int { return bytes.Compare(a[0][:], b[0][:]) })
			if !slices.Equal(got, tc.want) {
				t.Errorf("pairs = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNoPairWithTheSameContentGivesNoBulkQuestion(t *testing.T) {
	result := mergeOf(t,
		database(account(firstID, "Mail", "user", "", "one")),
		database(account(secondID, "Mail", "user", "", "two")),
	)

	for _, q := range result.Questions {
		if _, ok := q.Detail.(merge.IdenticalPairs); ok {
			t.Errorf("a question nobody needs: %s", printed(q))
		}
	}
}

// binned puts entries in the recycle bin of the copy, in a group below the bin if deep.
func binned(s *snapshot.Snapshot, deep bool, entries ...snapshot.Entry) *snapshot.Snapshot {
	binID := snapshot.UUID{15: 0xB0}
	bin := snapshot.Group{UUID: binID, Name: "Bin", Entries: entries}
	if deep {
		bin = snapshot.Group{UUID: binID, Name: "Bin", Groups: []snapshot.Group{{UUID: snapshot.UUID{15: 0xB1}, Name: "Old", Entries: entries}}}
	}
	s.Root.Groups = append(s.Root.Groups, bin)
	s.Meta.RecycleBin = binID
	return s
}

// A merge with an entry somebody deleted could put a living account into the recycle bin, or
// take a deleted one out of it. And it could contradict the answer about the deletion. So the
// pair waits until the deletion is settled: a later merge sees it again.
func TestAnEntryThatIsDeletedSomewhereMakesNoPair(t *testing.T) {
	live := account(firstID, "Mail", "user", "", "one")
	other := account(secondID, "Mail", "user", "", "two")

	for name, copies := range map[string][]*snapshot.Snapshot{
		"in the bin in all copies": {database(live), binned(database(), false, other)},
		"below the bin":            {database(live), binned(database(), true, other)},
		"in the bin in one copy":   {database(live, other), binned(database(), false, other)},
		"with a record of deletion": {
			database(live, other),
			{Root: snapshot.Group{UUID: rootID}, Deletions: []snapshot.Deletion{{UUID: secondID, Time: hours(5)}}},
		},
		"both in the bin": {binned(database(), false, live, other)},
	} {
		t.Run(name, func(t *testing.T) {
			result := mergeOf(t, copies...)

			if len(pairQuestions(result)) != 0 {
				t.Errorf("questions: %s", printed(result.Questions))
			}
		})
	}
}

func TestABinOrARecordOfAnotherEntryChangesNothingForAPair(t *testing.T) {
	third := account(thirdID, "Bank", "admin", "", "three")
	withBin := binned(database(account(firstID, "Mail", "user", "", "one")), false, third)
	withRecord := database(account(secondID, "Mail", "user", "", "two"))
	withRecord.Deletions = []snapshot.Deletion{{UUID: snapshot.UUID{15: 0xDD}, Time: hours(5)}}

	result := mergeOf(t, withBin, withRecord)

	if _, ok := pairQuestions(result)[[2]snapshot.UUID{firstID, secondID}]; !ok {
		t.Errorf("no question about the pair: %s", printed(result.Questions))
	}
}

// A copy with no recycle bin has a zero UUID there, and a group must not count as the bin for that.
func TestAGroupWithAZeroUUIDIsNoBinInACopyWithNoBin(t *testing.T) {
	s := database()
	s.Root.Groups = []snapshot.Group{{Name: "No UUID", Entries: []snapshot.Entry{account(secondID, "Mail", "user", "", "two")}}}

	result := mergeOf(t, database(account(firstID, "Mail", "user", "", "one")), s)

	if len(pairQuestions(result)) != 1 {
		t.Errorf("questions: %s", printed(result.Questions))
	}
}

// Both answers hold together: the version the human chose stays on top of the merged entry, and
// the versions of both entries stay below it.
func TestAMergedPairKeepsTheAnswerAboutAConflict(t *testing.T) {
	newer := account(firstID, "Mail", "user", "", "newer")
	newer.Times.LastModification = hours(2)
	older := account(firstID, "Mail", "user", "", "older")
	other := account(secondID, "Mail", "user", "", "other")
	other.Times.LastModification = hours(3)
	in := input(named(database(newer, other), database(older))...)
	for _, q := range mustRun(t, in).Questions {
		if conflict, ok := q.Detail.(merge.Conflict); ok {
			if conflict.Entry != firstID || !conflict.Versions[1].Modified.Equal(hours(1)) {
				t.Fatalf("unexpected conflict: %s", printed(conflict))
			}
			in.Decisions = append(in.Decisions, merge.Decision{Question: q.ID, Choice: 1})
		}
	}

	result := answerPair(t, in, firstID, secondID, 1)

	e := onlyEntry(t, result.Merged)
	wantVersions(t, e, "older", "older", "newer", "other")
	if !e.Times.LastModification.Equal(now.Truncate(time.Second)) {
		t.Errorf("the main version has time %s, want the time of the answer", e.Times.LastModification)
	}
}

func TestSimilarEntriesGiveAQuestionAndStayApartWithoutAnAnswer(t *testing.T) {
	result := mergeOf(t,
		database(account(firstID, "Mail", "user", "", "one")),
		database(account(secondID, "Mail", "user", "", "two")),
	)

	if _, asked := pairQuestions(result)[[2]snapshot.UUID{firstID, secondID}]; !asked {
		t.Errorf("no question about the pair: %s", printed(result.Questions))
	}
	merged := allEntries(result.Merged)
	if merged[firstID] == nil || merged[secondID] == nil {
		t.Errorf("the result lost an entry of the pair: %d entries", len(merged))
	}
}

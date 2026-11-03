package session

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/report"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/text"
)

// Answers is what the person said to the questions of a merge.
type Answers struct {
	// Decisions holds the explicit answers for the next run of the engine.
	Decisions []merge.Decision
	// Unanswered counts the questions that go by the rule for no answer.
	Unanswered int
}

// Decide asks the questions of a merge one by one, and what comes back goes to the engine. Enter
// leaves a question without an answer. The end of the input and the answer "stop" leave all the
// rest without one: then the rules for no answer apply, and they lose nothing.
//
// A family of K similar entries gives K(K-1)/2 questions, so the dialog keeps this usable: the
// answer about all pairs with the same content comes first, and it asks nothing about a pair that
// the answers so far have settled.
func (s *Session) Decide(result *merge.Result) Answers {
	questions := inDialogOrder(result.Questions)
	var answers Answers
	if len(questions) == 0 {
		return answers
	}
	s.say(text.DialogStart(len(questions)))
	pairs := newPairLedger()
	asked := 0
	for i := range questions {
		q := &questions[i]
		switch {
		case pairs.settles(q):
			continue
		case s.finished:
			answers.Unanswered++
			continue
		}
		asked++
		remaining := 0
		for j := i + 1; j < len(questions); j++ {
			if !pairs.settles(&questions[j]) {
				remaining++
			}
		}
		s.say("")
		s.say(text.QuestionNumber(asked, asked+remaining))
		choice, answered := s.askOne(q, result)
		if !answered {
			answers.Unanswered++
			continue
		}
		pairs.note(q, choice, questions)
		// To ask about each pair separately changes nothing in the data and in the memory. The
		// questions that follow say what comes of it.
		if _, isBulk := q.Detail.(merge.IdenticalPairs); isBulk && choice == 0 {
			continue
		}
		answers.Decisions = append(answers.Decisions, merge.Decision{Question: q.ID, Choice: choice})
	}
	return answers
}

// inDialogOrder puts the question that can settle many others first. The rest keep their order.
func inDialogOrder(questions []merge.Question) []merge.Question {
	ordered := slices.Clone(questions)
	slices.SortStableFunc(ordered, func(a, b merge.Question) int {
		_, firstIsBulk := a.Detail.(merge.IdenticalPairs)
		_, secondIsBulk := b.Detail.(merge.IdenticalPairs)
		switch {
		case firstIsBulk && !secondIsBulk:
			return -1
		case secondIsBulk && !firstIsBulk:
			return 1
		}
		return 0
	})
	return ordered
}

// askOne shows a question and reads answers until one fits. It says whether an answer came.
func (s *Session) askOne(q *merge.Question, result *merge.Result) (choice int, answered bool) {
	showables := showablesOf(q, result)
	s.writeQuestion(q)
	for {
		fmt.Fprint(s.prompts, text.AnswerPrompt(q.Choices, len(showables) > 0))
		line, ok := s.readAnswer()
		switch {
		case !ok:
			return 0, false
		case line == "":
			return 0, false
		case text.IsStopWord(line):
			s.finished = true
			return 0, false
		case len(showables) > 0 && text.IsShowWord(line):
			s.showOne(showables)
			continue
		}
		number, err := strconv.Atoi(line)
		if err == nil && number >= 1 && number <= q.Choices {
			return number - 1, true
		}
		s.say(text.AnswerNotUnderstood(q.Choices))
	}
}

func (s *Session) writeQuestion(q *merge.Question) {
	switch detail := q.Detail.(type) {
	case merge.Conflict:
		_ = report.WriteConflictQuestion(s.prompts, detail)
	case merge.Deletion:
		_ = report.WriteDeletionQuestion(s.prompts, detail)
	case merge.SimilarPair:
		_ = report.WritePairQuestion(s.prompts, detail)
	case merge.IdenticalPairs:
		_ = report.WriteIdenticalPairsQuestion(s.prompts, len(detail.Pairs))
	}
}

// readAnswer reads one line of the dialog. It says whether a line came. Nothing more comes after
// the end of the input.
func (s *Session) readAnswer() (string, bool) {
	line, err := s.readPlainLine()
	if err != nil {
		s.finished = true
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(string(line))), true
}

// showableValue is one value a person can ask to see.
type showableValue struct {
	holder, field, value string
}

// showablesOf lists the values of the versions a question is about. A title and a login stand in
// the question already, unless they are protected. An empty value is nothing to see.
func showablesOf(q *merge.Question, result *merge.Result) []showableValue {
	versions := result.VersionsAsked(q.ID)
	var list []showableValue
	for i := range versions {
		holder := holderOf(q, i)
		for _, field := range versions[i].Fields {
			if field.Value == "" || (!field.Protected && (field.Key == snapshot.FieldTitle || field.Key == snapshot.FieldUserName)) {
				continue
			}
			list = append(list, showableValue{holder: holder, field: field.Key, value: field.Value})
		}
	}
	return list
}

func holderOf(q *merge.Question, version int) string {
	switch q.Detail.(type) {
	case merge.Deletion:
		return text.OnlyEntry
	case merge.SimilarPair:
		if version == 0 {
			return text.PairFirstEntry
		}
		return text.PairSecondEntry
	}
	return text.VersionName(version + 1)
}

// showOne is the separate action that shows one value of one version: the warning about the
// scrollback, the list of names, then the one value the person names by its number.
func (s *Session) showOne(showables []showableValue) {
	list := make([]report.Showable, len(showables))
	for i, one := range showables {
		list[i] = report.Showable{Holder: one.holder, Field: one.field}
	}
	_ = report.WriteShowables(s.prompts, list)
	fmt.Fprint(s.prompts, text.ShowPrompt)
	line, ok := s.readAnswer()
	number, err := strconv.Atoi(line)
	if !ok || err != nil || number < 1 || number > len(showables) {
		s.say(text.ShowNothing)
		return
	}
	one := showables[number-1]
	s.say(text.ShownValue(one.holder, sanitizedLine(one.field), sanitizedValue(one.value)))
}

// sanitizedValue keeps what a person needs to read and takes what would move the cursor or give
// orders to the terminal: a value comes from a file, and only a person with the file made it.
func sanitizedValue(value string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return unicode.ReplacementChar
		}
		return r
	}, value)
}

func sanitizedLine(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}

// pairLedger follows what the answers so far say about similar entries.
type pairLedger struct {
	heads map[snapshot.UUID]snapshot.UUID
	apart [][2]snapshot.UUID
}

func newPairLedger() *pairLedger {
	return &pairLedger{heads: map[snapshot.UUID]snapshot.UUID{}}
}

func (l *pairLedger) head(id snapshot.UUID) snapshot.UUID {
	for {
		next, ok := l.heads[id]
		if !ok {
			return id
		}
		id = next
	}
}

// settles says the answers so far leave nothing to ask about this question: both entries are in
// one family already, or a merge would make one family of two entries the person keeps apart, and
// the engine would refuse it.
func (l *pairLedger) settles(q *merge.Question) bool {
	pair, isPair := q.Detail.(merge.SimilarPair)
	if !isPair {
		return false
	}
	a, b := l.head(pair.First.Entry), l.head(pair.Second.Entry)
	return a == b || slices.ContainsFunc(l.apart, func(other [2]snapshot.UUID) bool {
		x, y := l.head(other[0]), l.head(other[1])
		return (x == a && y == b) || (x == b && y == a)
	})
}

// note takes in an answer. AnswerMergePair merges, both for one pair and for all pairs with the same
// content. AnswerKeepBoth keeps a pair apart and says nothing about all such pairs.
func (l *pairLedger) note(q *merge.Question, choice int, questions []merge.Question) {
	switch detail := q.Detail.(type) {
	case merge.SimilarPair:
		if choice == merge.AnswerMergePair {
			l.join(detail)
		} else {
			l.apart = append(l.apart, [2]snapshot.UUID{detail.First.Entry, detail.Second.Entry})
		}
	case merge.IdenticalPairs:
		if choice != merge.AnswerMergePair {
			return
		}
		for i := range questions {
			if pair, isPair := questions[i].Detail.(merge.SimilarPair); isPair && slices.Contains(detail.Pairs, questions[i].ID) {
				l.join(pair)
			}
		}
	}
}

func (l *pairLedger) join(pair merge.SimilarPair) {
	if a, b := l.head(pair.First.Entry), l.head(pair.Second.Entry); a != b {
		l.heads[b] = a
	}
}

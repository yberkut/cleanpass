package merge

import (
	"errors"
	"fmt"
)

// QuestionID identifies a question within one set of copies. It does not depend on the
// Key or on the order of the copies, so a Decision can refer to it.
type QuestionID string

// Question is a decision the engine wants from the human.
type Question struct {
	ID QuestionID
	// Choices is the number of possible answers, from 0 to Choices-1. Answer 0 is always
	// what happens without an answer.
	Choices int
	// Detail tells what the question is about. Each rule defines its own type, so look at
	// the type with a type switch. It never holds a secret value.
	Detail any
}

// Decision is the answer of the human to one question.
type Decision struct {
	Question QuestionID
	Choice   int
}

// The errors about Decisions. They mean a Decision does not fit the Questions.
var (
	ErrUnknownQuestion = errors.New("merge: decision for a question nobody asked")
	ErrBadChoice       = errors.New("merge: decision with a choice the question does not have")
)

// ask adds the question to the result and returns the answer, or 0 and false if the human
// has not answered. A question is the same whether answered or not, so it never
// disappears from the result.
func (m *merger) ask(q Question) (choice int, answered bool) {
	m.questions = append(m.questions, q)
	choice, answered = m.answers[q.ID]
	if !answered {
		return 0, false
	}
	m.used[q.ID] = true
	if choice < 0 || choice >= q.Choices {
		m.fail(fmt.Errorf("%w: %d of %d for %s", ErrBadChoice, choice, q.Choices, q.ID))
		return 0, false
	}
	return choice, true
}

// fail keeps the first error. Run returns it after the rules have run.
func (m *merger) fail(err error) {
	if m.failure == nil {
		m.failure = err
	}
}

func (m *merger) checkAllDecisionsUsed() error {
	for _, d := range m.in.Decisions {
		if !m.used[d.Question] {
			return fmt.Errorf("%w: %s", ErrUnknownQuestion, d.Question)
		}
	}
	return nil
}

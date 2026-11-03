package report

import (
	"io"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/text"
)

// typed gives the number a person types for an answer of the engine: the numbers start at 1.
func typed(choice int) int { return choice + 1 }

// WriteDeletionQuestion shows a question about a deletion with its answers.
func WriteDeletionQuestion(w io.Writer, d merge.Deletion) error {
	out := &writer{w: w}
	out.line("", text.QuestionDeletion)
	out.line("  ", labelText(d.Label, d.Entry))
	out.deletionFacts(&d)
	out.line("    ", text.AnswerKeepEntry(typed(merge.AnswerKeep)))
	if len(d.Recorded) > 0 {
		out.line("    ", text.AnswerDeleteEntry(typed(merge.AnswerDelete)))
	} else {
		out.line("    ", text.AnswerDeleteToBin(typed(merge.AnswerDelete)))
	}
	return out.err
}

// WriteConflictQuestion shows the versions of an entry side by side, with no secret value: what
// tells them apart is the name of each part that differs.
func WriteConflictQuestion(w io.Writer, c merge.Conflict) error {
	out := &writer{w: w}
	out.line("", text.QuestionConflict)
	out.line("  ", labelText(c.Versions[0].Label, c.Entry))
	if c.Close {
		out.line("    ", text.ConflictClose)
	}
	if c.Future {
		out.line("    ", text.ConflictFuture)
	}
	if c.Replaced {
		out.line("    ", text.ConflictClock)
	}
	out.line("    ", text.ConflictHint)
	for i := range c.Versions {
		v := &c.Versions[i]
		out.line("    ", text.AnswerVersion(typed(i), formatTime(v.Modified), v.Copies))
		out.line("       ", labelText(v.Label, c.Entry))
		if i > 0 {
			out.line("       ", differsFromFirst(v.Differences))
		}
	}
	return out.err
}

func differsFromFirst(d merge.Difference) string {
	names := differenceNames(d)
	if len(names) == 0 {
		return text.SameAsFirstVersion
	}
	return text.DiffersFromFirstVersion(names)
}

// WritePairQuestion shows a question about two similar entries with its answers.
func WritePairQuestion(w io.Writer, p merge.SimilarPair) error {
	out := &writer{w: w}
	out.line("", text.QuestionPair)
	out.pair(&p)
	out.line("    ", text.AnswerKeepBoth(typed(merge.AnswerKeepBoth)))
	out.line("    ", text.AnswerMergePair(typed(merge.AnswerMergePair)))
	return out.err
}

// WriteIdenticalPairsQuestion shows the one question about all pairs with the same content.
func WriteIdenticalPairsQuestion(w io.Writer, count int) error {
	out := &writer{w: w}
	out.line("", text.QuestionIdenticalPairs(count))
	out.line("    ", text.AnswerAskEach(typed(merge.AnswerKeepBoth)))
	out.line("    ", text.AnswerMergeAll(typed(merge.AnswerMergePair)))
	return out.err
}

// Showable is one thing a person can ask to see. It holds names, and no value.
type Showable struct {
	// Holder says whose field this is: a version or an entry of the question.
	Holder string
	// Field is the name of the field.
	Field string
}

// WriteShowables warns about the scrollback of the terminal and lists what can be shown.
func WriteShowables(w io.Writer, showables []Showable) error {
	out := &writer{w: w}
	out.line("", text.ShowWarning)
	for i, s := range showables {
		out.line("  ", text.ShowableField(i+1, s.Holder, oneLine(s.Field)))
	}
	return out.err
}

// WriteKeysQuestion shows the question about what opens the result.
func WriteKeysQuestion(w io.Writer, paths []string, newest int) error {
	out := &writer{w: w}
	out.line("", text.QuestionKeys)
	for i, path := range paths {
		out.line("    ", text.KeysFromCopy(i+1, path, i == newest))
	}
	out.line("    ", text.KeysNew(len(paths)+1))
	return out.err
}

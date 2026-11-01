package report

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"

	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/text"
)

// WriteDifferences writes what the merge said about the copies, section by section, each after
// an empty line. A section with nothing to say stays out. It tells the state before any answer.
func WriteDifferences(w io.Writer, facts merge.Facts) error {
	out := &writer{w: w}
	onlyInSome, lagging, disputes := sortedByOutcome(facts.Entries)
	if len(onlyInSome) > 0 {
		out.header(text.SectionOnlyInSome)
		for i := range onlyInSome {
			out.entryLabel(&onlyInSome[i])
			out.copies(&onlyInSome[i], false)
		}
	}
	if len(lagging) > 0 {
		out.header(text.SectionLagging)
		for i := range lagging {
			out.entryLabel(&lagging[i])
			out.copies(&lagging[i], true)
		}
	}
	if len(disputes) > 0 {
		out.header(text.SectionDisputes)
		for i := range disputes {
			out.entryLabel(&disputes[i])
			out.reasons(&disputes[i], facts.Conflicts)
			out.copies(&disputes[i], true)
		}
	}
	if len(facts.Deletions) > 0 {
		out.header(text.SectionDeletions)
		for _, d := range byTitle(facts.Deletions, func(d merge.DeletionChange) string { return d.Label.Title }) {
			out.deletion(&d.Deletion)
		}
	}
	if len(facts.Pairs) > 0 {
		out.header(text.SectionPairs)
		for _, pair := range byTitle(facts.Pairs, func(p merge.SimilarPair) string { return p.First.Label.Title }) {
			out.pair(&pair)
		}
		if facts.IdenticalPairs > 0 {
			out.line("  ", text.PairsIdentical(facts.IdenticalPairs))
		}
	}
	if facts.Unknown > 0 {
		out.header(text.UnknownFacts(facts.Unknown))
	}
	return out.err
}

// byTitle orders a section, so a person finds an entry. The facts come in a fixed order, so the
// same titles stay in a fixed order too.
func byTitle[T any](items []T, title func(T) string) []T {
	sorted := slices.Clone(items)
	slices.SortStableFunc(sorted, func(a, b T) int { return strings.Compare(title(a), title(b)) })
	return sorted
}

func sortedByOutcome(entries []merge.EntryChange) (onlyInSome, lagging, disputes []merge.EntryChange) {
	sorted := byTitle(entries, func(e merge.EntryChange) string { return e.Label.Title })
	for i := range sorted {
		switch sorted[i].Outcome {
		case merge.OutcomeNew:
			onlyInSome = append(onlyInSome, sorted[i])
		case merge.OutcomeUpdated:
			lagging = append(lagging, sorted[i])
		default:
			disputes = append(disputes, sorted[i])
		}
	}
	return onlyInSome, lagging, disputes
}

// writer keeps the first error, so the sections read as text and not as error checks.
type writer struct {
	w   io.Writer
	err error
}

func (o *writer) line(indent, line string) {
	if o.err == nil {
		_, o.err = fmt.Fprintln(o.w, indent+line)
	}
}

func (o *writer) header(line string) {
	o.line("", "")
	o.line("", line)
}

func (o *writer) entryLabel(e *merge.EntryChange) {
	o.line("  ", labelText(e.Label, e.Entry))
}

func (o *writer) reasons(e *merge.EntryChange, conflicts map[snapshot.UUID]merge.Conflict) {
	conflict := conflicts[e.Entry]
	if e.Outcome == merge.OutcomeConflict {
		o.line("    ", text.DisputeFarApart)
	}
	if conflict.Close {
		o.line("    ", text.DisputeClose)
	}
	if conflict.Future {
		o.line("    ", text.DisputeFuture)
	}
	if e.Replaced {
		o.line("    ", text.DisputeClock)
	}
}

// copies writes one line for each copy. With tellHistory it says whether another copy holds this
// version in its history and how it differs from the version that becomes the main one.
func (o *writer) copies(e *merge.EntryChange, tellHistory bool) {
	for i := range e.Copies {
		c := &e.Copies[i]
		switch {
		case !c.Holds:
			o.line("    ", text.EntryNotInCopy(c.Name))
		case tellHistory:
			o.line("    ", text.EntryInCopy(c.Name, formatTime(c.Modified), historyMark(c.Behind), differenceText(c.Differences)))
		default:
			o.line("    ", text.EntryInCopy(c.Name, formatTime(c.Modified)))
		}
	}
}

func (o *writer) deletion(d *merge.Deletion) {
	o.line("  ", labelText(d.Label, d.Entry))
	o.line("    ", text.DeletionDates(formatTime(d.Deleted), formatTime(d.Modified)))
	if len(d.Recorded) > 0 {
		o.line("    ", text.DeletionRecordedIn(d.Recorded))
	}
	if len(d.Recycled) > 0 {
		o.line("    ", text.DeletionInBin(d.Recycled))
	}
	if len(d.Held) > 0 {
		o.line("    ", text.DeletionOutsideBin(d.Held))
	}
}

func (o *writer) pair(p *merge.SimilarPair) {
	o.line("  ", text.PairHeader(labelText(p.First.Label, p.First.Entry), labelText(p.Second.Label, p.Second.Entry)))
	o.line("    ", text.PairFirst(formatTime(p.First.Modified), p.First.Copies))
	o.line("    ", text.PairSecond(formatTime(p.Second.Modified), p.Second.Copies))
	o.line("    ", text.PairCommon(p.SameTitleAndUser, p.SameUserAndHost, p.SamePassword, p.SameContent))
	// The line above has told about the password, whatever the fields say.
	other := p.Differences
	other.Fields = slices.DeleteFunc(slices.Clone(other.Fields), func(field string) bool { return field == "Password" })
	if names := differenceNames(other); !p.SameContent && len(names) > 0 {
		o.line("    ", text.PairDiffers(names))
	}
}

func historyMark(behind bool) string {
	if behind {
		return text.InHistoryOfAnother
	}
	return text.NotInHistoryOfOthers
}

func differenceText(d merge.Difference) string {
	names := differenceNames(d)
	if len(names) == 0 {
		return text.SameAsMainVersion
	}
	return text.DiffersFromMainVersion(names)
}

// differenceNames says what differs, one phrase for each part. A Difference holds names only.
func differenceNames(d merge.Difference) []string {
	names := make([]string, 0, len(d.Fields)+len(d.Attributes))
	for _, field := range d.Fields {
		names = append(names, text.FieldDiffers(oneLine(field)))
	}
	for _, attribute := range d.Attributes {
		names = append(names, attributeDiffers(attribute))
	}
	return names
}

func attributeDiffers(a merge.Attribute) string {
	switch a {
	case merge.AttributeIcon:
		return text.DiffersIcon
	case merge.AttributeColors:
		return text.DiffersColors
	case merge.AttributeOverrideURL:
		return text.DiffersOverrideURL
	case merge.AttributeQualityCheck:
		return text.DiffersQualityCheck
	case merge.AttributeTags:
		return text.DiffersTags
	case merge.AttributeCreation:
		return text.DiffersCreation
	case merge.AttributeExpiry:
		return text.DiffersExpiry
	case merge.AttributeAttachments:
		return text.DiffersAttachments
	case merge.AttributeAutoType:
		return text.DiffersAutoType
	case merge.AttributeCustomData:
		return text.DiffersCustomData
	}
	return ""
}

// labelText tells a person which entry this is. Of a URL it takes only the host, because the rest
// can hold a password or a token.
func labelText(label merge.Label, id snapshot.UUID) string {
	return text.EntryLabel(oneLine(label.Title), oneLine(label.UserName), oneLine(label.Host()), id.String())
}

// oneLine replaces every character that breaks a line or moves the cursor. A value from a file
// must not be able to write a line of its own in the report, such as a false next command.
func oneLine(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp) {
			return ' '
		}
		return r
	}, value)
}

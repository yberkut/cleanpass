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
// an empty line. A section with nothing to say stays out. It tells the state before any answer,
// apart from the sections about what answers did to similar entries.
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
			out.reasons(&lagging[i], facts.Conflicts)
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
			out.deletion(&d)
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
	if len(facts.RememberedPairs) > 0 {
		out.header(text.SectionRememberedPairs)
		for _, p := range byTitle(facts.RememberedPairs, func(p merge.PairRemembered) string { return p.Label.Title }) {
			out.line("  ", labelText(p.Label, p.Entry))
			out.line("    ", text.PairRemembered(len(p.Absorbed), p.Copies))
		}
	}
	out.pairsAnswered(&facts)
	if len(facts.GroupsJoined)+len(facts.GroupsKeptApart)+len(facts.GroupCycles) > 0 {
		out.header(text.SectionGroups)
		for _, g := range byTitle(facts.GroupsJoined, func(g merge.GroupsJoined) string { return g.Name }) {
			out.line("  ", text.GroupsJoin(groupName(g.Name, g.Into), len(g.Joined)))
		}
		for _, g := range byTitle(facts.GroupsKeptApart, func(g merge.GroupsKeptApart) string { return g.Name }) {
			out.line("  ", text.GroupsStayApart(groupName(g.Name, g.Groups[0]), len(g.Groups)-1))
		}
		for _, g := range byTitle(facts.GroupCycles, func(g merge.GroupCycleBroken) string { return g.Name }) {
			out.line("  ", text.GroupLeavesCycle(groupName(g.Name, g.Group)))
		}
	}
	if facts.Unknown > 0 {
		out.header(text.UnknownFacts(facts.Unknown))
	}
	return out.err
}

// WritePairsAnswered writes what the answers of this run did to similar entries. It writes nothing
// when they did nothing.
func WritePairsAnswered(w io.Writer, facts merge.Facts) error {
	out := &writer{w: w}
	out.pairsAnswered(&facts)
	return out.err
}

func (o *writer) pairsAnswered(facts *merge.Facts) {
	if len(facts.PairsMerged) > 0 {
		o.header(text.SectionPairsMerged)
		for _, p := range byTitle(facts.PairsMerged, func(p merge.PairMerged) string { return p.Label.Title }) {
			o.line("  ", text.PairMergedInto(labelText(p.Label, p.Entry), len(p.Absorbed)))
		}
	}
	if len(facts.PairsNotMerged) > 0 {
		o.header(text.SectionPairsNotMerged)
		for _, p := range byTitle(facts.PairsNotMerged, func(p merge.PairNotMerged) string { return p.First.Label.Title }) {
			o.line("  ", text.PairHeader(labelText(p.First.Label, p.First.Entry), labelText(p.Second.Label, p.Second.Entry)))
		}
	}
}

// WriteSummary writes the last words about what the merge did.
func WriteSummary(w io.Writer, summary merge.Summary) error {
	out := &writer{w: w}
	out.header(text.SummaryHeader)
	out.line("  ", text.SummaryAdded(summary.Added))
	out.line("  ", text.SummaryUpdated(summary.Updated))
	out.line("  ", text.SummaryKeptInHistory(summary.KeptInHistory))
	out.line("  ", text.SummaryDeleted(summary.DeletedByDecision))
	if summary.Absorbed > 0 {
		out.line("  ", text.SummaryAbsorbed(summary.Absorbed))
	}
	return out.err
}

// WriteUnrelated warns that the copies look like different databases. Each group holds the paths
// of the copies that look like one database.
func WriteUnrelated(w io.Writer, groups [][]string) error {
	out := &writer{w: w}
	out.header(text.UnrelatedWarning)
	out.err = WriteUnrelatedGroups(w, groups)
	return out.err
}

// WriteUnrelatedGroups names the copies that look like one database, one line for each group.
func WriteUnrelatedGroups(w io.Writer, groups [][]string) error {
	out := &writer{w: w}
	for i, group := range groups {
		out.line("  ", text.UnrelatedGroup(i+1, group))
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

// reasons says why the entry stands where it stands. A clock that has put a replaced version on
// top is the case a person must see: with no question left about it, an earlier decision has
// settled it, and the report still names it.
func (o *writer) reasons(e *merge.EntryChange, conflicts map[snapshot.UUID]merge.Conflict) {
	conflict, asked := conflicts[e.Entry]
	if e.Outcome == merge.OutcomeConflict {
		o.line("    ", text.DisputeFarApart)
	}
	if conflict.Close {
		o.line("    ", text.DisputeClose)
	}
	if conflict.Future {
		o.line("    ", text.DisputeFuture)
	}
	switch {
	case e.Replaced && asked:
		o.line("    ", text.DisputeClock)
	case e.Replaced:
		o.line("    ", text.DisputeClockDecided)
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

func (o *writer) deletion(d *merge.DeletionChange) {
	o.line("  ", labelText(d.Label, d.Entry))
	switch d.Outcome {
	case merge.DeletionOpen:
		o.line("    ", text.DeletionWillAsk)
	case merge.DeletionRejected:
		o.line("    ", text.DeletionDecidedKeep)
	case merge.DeletionApplied:
		o.line("    ", text.DeletionDecidedDelete)
	}
	o.deletionFacts(&d.Deletion)
}

// deletionFacts writes what a person weighs in a question about a deletion.
func (o *writer) deletionFacts(d *merge.Deletion) {
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
	other.Fields = slices.DeleteFunc(slices.Clone(other.Fields), func(field string) bool { return field == snapshot.FieldPassword })
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

func groupName(name string, id snapshot.UUID) string {
	return text.EntryLabel(oneLine(name), "", "", id.String())
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

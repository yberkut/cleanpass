// Package report is the Report module: it turns data into text. Its input never
// holds secret values: it takes these plain structures, not a Snapshot.
package report

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/text"
)

// Passport is what the report shows about one copy.
type Passport struct {
	Path     string
	Size     int64
	FileDate time.Time
	// LastChange is the newest change inside the database, or zero if there is none.
	LastChange time.Time
	Stats      snapshot.Stats
	Format     snapshot.Format
	Freshest   bool
	// FromTheFuture is the newest time stamp of the copy if it lies after the current time, or
	// zero if none does.
	FromTheFuture time.Time
	// FieldLosses is what a save of this copy would not keep. It holds no values.
	FieldLosses []snapshot.FieldLoss
}

const timeLayout = "2006-01-02 15:04:05 -07:00"

// WritePassports writes one block for each copy.
func WritePassports(w io.Writer, passports []Passport) error {
	for i := range passports {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if err := writePassport(w, i+1, &passports[i]); err != nil {
			return err
		}
	}
	return nil
}

func writePassport(w io.Writer, number int, p *Passport) error {
	if _, err := fmt.Fprintln(w, text.CopyHeader(number, p.Path, p.Freshest)); err != nil {
		return err
	}
	table := tabwriter.NewWriter(w, 0, 0, 1, ' ', 0)
	rows := [][2]string{
		{text.LabelFileSize, strconv.FormatInt(p.Size, 10)},
		{text.LabelFileDate, formatTime(p.FileDate)},
		{text.LabelLastChange, formatTime(p.LastChange)},
		{text.LabelEntries, strconv.Itoa(p.Stats.Entries)},
		{text.LabelGroups, strconv.Itoa(p.Stats.Groups)},
		{text.LabelAttachments, strconv.Itoa(p.Stats.Attachments)},
		{text.LabelDeletions, strconv.Itoa(p.Stats.Deletions)},
		{text.LabelFormat, text.FormatName(p.Format.Major, p.Format.Minor)},
		{text.LabelCipher, cipherName(p.Format.Cipher)},
		{text.LabelKDF, kdfName(p.Format.KDF)},
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(table, "  %s:\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if !p.FromTheFuture.IsZero() {
		if _, err := fmt.Fprintln(w, "  "+text.FutureStampWarning(formatTime(p.FromTheFuture))); err != nil {
			return err
		}
	}
	return WriteFieldLosses(w, p.FieldLosses)
}

// WriteFieldLosses writes the warning of the field guard and names every path. It writes
// nothing when there is nothing to lose.
func WriteFieldLosses(w io.Writer, losses []snapshot.FieldLoss) error {
	if len(losses) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "  "+text.FieldLossWarning); err != nil {
		return err
	}
	for _, loss := range losses {
		if _, err := fmt.Fprintln(w, "    "+text.FieldLossLine(loss.Path, loss.InFile, loss.Kept)); err != nil {
			return err
		}
	}
	return nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return text.NoDate
	}
	return t.Local().Format(timeLayout)
}

func cipherName(c snapshot.Cipher) string {
	switch c {
	case snapshot.CipherAES256:
		return text.CipherAES256
	case snapshot.CipherChaCha20:
		return text.CipherChaCha20
	case snapshot.CipherTwofish:
		return text.CipherTwofish
	}
	return ""
}

func kdfName(k snapshot.KDF) string {
	const mebibyte = 1024 * 1024
	switch k.Kind {
	case snapshot.KDFAES:
		return text.KDFAES(k.Rounds)
	case snapshot.KDFArgon2d:
		return text.KDFArgon2("Argon2d", k.MemoryBytes/mebibyte, k.Iterations, k.Parallelism)
	case snapshot.KDFArgon2id:
		return text.KDFArgon2("Argon2id", k.MemoryBytes/mebibyte, k.Iterations, k.Parallelism)
	}
	return ""
}

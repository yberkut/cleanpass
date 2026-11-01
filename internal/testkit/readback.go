package testkit

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/tobischo/gokeepasslib/v3"
)

// serviceItems are the custom data items KeePassXC renews itself: _LAST_MODIFIED comes from
// the export at the moment it runs, and the slug comes from the last save.
var serviceItems = regexp.MustCompile(`(?s)<Item>\s*<Key>(_LAST_MODIFIED|KPXC_RANDOM_SLUG)</Key>.*?</Item>`)

// ExportLines gives what the real keepassxc-cli sees in the file: the lines of its export,
// without indentation and without service items, sorted. The order of entries and the
// depth of a group do not change it. The lines hold secret values in clear text, so a test
// must not print them.
func (db DB) ExportLines(t testing.TB) []string {
	t.Helper()
	export := serviceItems.ReplaceAllString(db.CLI(t, "export"), "")
	var lines []string
	for line := range strings.SplitSeq(export, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	slices.Sort(lines)
	return lines
}

// AttachmentBytes gives the bytes of an attachment of a main version as the real
// keepassxc-cli reads them. The export does not hold these bytes.
func (db DB) AttachmentBytes(t testing.TB, entry, name string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attachment.bin")
	db.CLI(t, "attachment-export", entry, name, path)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// AttachFile adds an attachment with these bytes to an entry, with the real keepassxc-cli.
func (db DB) AttachFile(t testing.TB, entry, name string, content []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attachment.bin")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	db.CLI(t, "attachment-import", entry, name, path)
}

// Randoms are the random values a save must renew.
type Randoms struct {
	MasterSeed, EncryptionIV, KDFSalt, InnerStreamKey []byte
}

// Randoms reads the random values of a KDBX 4 file.
func (db DB) Randoms(t testing.TB) Randoms {
	t.Helper()
	lib := db.openWithLibrary(t)
	headers := lib.Header.FileHeaders
	return Randoms{
		MasterSeed:     bytes.Clone(headers.MasterSeed),
		EncryptionIV:   bytes.Clone(headers.EncryptionIV),
		KDFSalt:        bytes.Clone(headers.KdfParameters.Salt[:]),
		InnerStreamKey: bytes.Clone(lib.Content.InnerHeader.InnerRandomStreamKey),
	}
}

// VersionsWithoutLocationTime counts the versions of entries in the file, main and older ones,
// that have no LocationChanged element.
func (db DB) VersionsWithoutLocationTime(t testing.TB) int {
	t.Helper()
	lib := db.openWithLibrary(t)
	count := 0
	var countEntries func(entries []gokeepasslib.Entry)
	countEntries = func(entries []gokeepasslib.Entry) {
		for i := range entries {
			if entries[i].Times.LocationChanged == nil {
				count++
			}
			for j := range entries[i].Histories {
				countEntries(entries[i].Histories[j].Entries)
			}
		}
	}
	var countGroups func(groups []gokeepasslib.Group)
	countGroups = func(groups []gokeepasslib.Group) {
		for i := range groups {
			countEntries(groups[i].Entries)
			countGroups(groups[i].Groups)
		}
	}
	countGroups(lib.Content.Root.Groups)
	return count
}

// AttachmentsInFile counts the attachments the file itself stores. The same bytes under two
// names must be stored once.
func (db DB) AttachmentsInFile(t testing.TB) int {
	t.Helper()
	lib := db.openWithLibrary(t)
	if lib.Header.IsKdbx4() {
		return len(lib.Content.InnerHeader.Binaries)
	}
	return len(lib.Content.Meta.Binaries)
}

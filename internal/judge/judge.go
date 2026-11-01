// Package judge is the KeePassXC Judge: it gives a written result to the real keepassxc-cli and
// compares what that program sees with the expected result.
//
// The Judge never trusts a read of this tool where KeePassXC can tell by itself. What
// keepassxc-cli prints holds secret values in clear text, so it stays in memory: no error, log,
// report or file gets any of it. The password goes to the program only through its stdin.
package judge

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/snapshot"
)

// ErrCannotRead means KeePassXC could not read the result, or what it printed makes no sense.
var ErrCannotRead = errors.New("KeePassXC cannot read the result")

// callTimeout ends a call that hangs. It leaves room for a slow KDF on a slow machine.
const callTimeout = 10 * time.Minute

// Check gives the temporary file of a result to KeePassXC and compares what it sees with what the
// result should hold. It gives ErrCannotRead, a MismatchError, or another error when the Judge
// itself could not work. None of them holds a value from the database.
//
// Each call of the program runs the KDF of the file again.
func (p Program) Check(temp string, want *snapshot.Snapshot, keys gateway.Credentials) error {
	temp, err := filepath.Abs(temp)
	if err != nil {
		return err
	}
	var export bytes.Buffer
	if err := p.run(keys, "export", &export, temp); err != nil {
		return err
	}
	if err := CompareExport(export.Bytes(), want); err != nil {
		return err
	}
	attachments := attachmentsOf(&want.Root)
	for i := range attachments.reachable {
		if err := p.checkBytes(temp, keys, &attachments.reachable[i]); err != nil {
			return err
		}
	}
	if attachments.needSave {
		return p.checkAfterSave(temp, want, keys)
	}
	return nil
}

// unlockArguments gives what opens the file in every call of the program. The password goes in
// through stdin alone, see passwordLine. A YubiKey slot (ticket #14) belongs here: -y <slot>.
func unlockArguments(keys gateway.Credentials) []string {
	arguments := []string{"--quiet"}
	if keys.Password == nil {
		arguments = append(arguments, "--no-password")
	}
	if keys.KeyFile != "" {
		arguments = append(arguments, "--key-file", keys.KeyFile)
	}
	return arguments
}

// passwordLine is what the program reads from stdin: one line, also for an empty password.
func passwordLine(keys gateway.Credentials) io.Reader {
	if keys.Password == nil {
		return nil
	}
	return bytes.NewReader(append(bytes.Clone(keys.Password), '\n'))
}

// command makes a call of the program. After -- every word is a path or a name, whatever it looks
// like: an attachment can be named --force. The standard error stays out of everything: the
// program writes there in the language of the system, and it can name entries.
func (p Program) command(ctx context.Context, keys gateway.Credentials, name string, positional ...string) *exec.Cmd {
	arguments := append([]string{name}, unlockArguments(keys)...)
	arguments = append(arguments, "--")
	arguments = append(arguments, positional...)
	cmd := exec.CommandContext(ctx, p.Path, arguments...)
	cmd.Stdin = passwordLine(keys)
	return cmd
}

func (p Program) run(keys gateway.Credentials, name string, stdout io.Writer, positional ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	cmd := p.command(ctx, keys, name, positional...)
	cmd.Stdout = stdout
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s failed", ErrCannotRead, name)
	}
	return nil
}

// reachableAttachment is an attachment of a main version that attachment-export can read.
type reachableAttachment struct {
	entry     snapshot.UUID
	entryPath string
	ref       snapshot.AttachmentRef
}

type attachmentPlan struct {
	reachable []reachableAttachment
	// needSave says that attachment-export cannot read all attachments.
	needSave bool
}

// attachmentsOf sorts the attachments by what can read them. The program reads attachments of
// main versions only, and it finds an entry by its path: of two entries on one path it takes the
// first. The walk below goes in the order of that search. The other attachments wait for a save.
func attachmentsOf(root *snapshot.Group) attachmentPlan {
	var plan attachmentPlan
	taken := map[string]bool{}
	var walk func(g *snapshot.Group, base string)
	walk = func(g *snapshot.Group, base string) {
		for i := range g.Entries {
			entry := &g.Entries[i]
			path := base + titleOf(&entry.EntryVersion)
			if taken[path] {
				plan.needSave = plan.needSave || len(entry.Attachments) > 0
			} else {
				for _, ref := range entry.Attachments {
					plan.reachable = append(plan.reachable, reachableAttachment{entry.UUID, path, ref})
				}
			}
			taken[path] = true
			for j := range entry.History {
				plan.needSave = plan.needSave || len(entry.History[j].Attachments) > 0
			}
		}
		for i := range g.Groups {
			walk(&g.Groups[i], base+g.Groups[i].Name+"/")
		}
	}
	walk(root, "/")
	return plan
}

func titleOf(v *snapshot.EntryVersion) string {
	for i := range v.Fields {
		if v.Fields[i].Key == "Title" {
			return v.Fields[i].Value
		}
	}
	return ""
}

// checkBytes compares the bytes of one attachment as the program reads them. The bytes come
// through a stream and never reach a file, and only their hash stays.
func (p Program) checkBytes(temp string, keys gateway.Credentials, attachment *reachableAttachment) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	cmd := p.command(ctx, keys, "attachment-export", temp, attachment.entryPath, attachment.ref.Name)
	hash := sha256.New()
	target, finish, err := attachmentStream(cmd, hash)
	if err != nil {
		return err
	}
	cmd.Args = append(cmd.Args, target)
	runErr := cmd.Run()
	if err := finish(); err != nil {
		return err
	}
	if runErr != nil {
		return fmt.Errorf("%w: attachment-export failed", ErrCannotRead)
	}
	if snapshot.AttachmentID(hash.Sum(nil)) != attachment.ref.ID {
		return &MismatchError{Place: attachmentsPlace(attachment.entry)}
	}
	return nil
}

func attachmentsPlace(entry snapshot.UUID) string { return "Attachments[" + entry.String() + "]" }

// checkAfterSave makes the program save a copy of the result. To save an attachment of an older
// version it has to read it, and only then can this tool see what it read. The copy is as
// encrypted as the result, and it goes away at the end.
func (p Program) checkAfterSave(temp string, want *snapshot.Snapshot, keys gateway.Credentials) error {
	saved, err := copyBeside(temp)
	if saved != "" {
		defer os.Remove(saved)
	}
	if err != nil {
		return err
	}
	group, err := newGroupName()
	if err != nil {
		return err
	}
	if err := p.run(keys, "mkdir", io.Discard, saved, "/"+group); err != nil {
		return err
	}
	seen, err := gateway.Open(saved, keys)
	if err != nil {
		return fmt.Errorf("%w: the saved copy", ErrCannotRead)
	}
	if place := attachmentsDiffer(want, seen); place != "" {
		return &MismatchError{Place: place}
	}
	return nil
}

// copyBeside copies the file to a new temporary file in its directory. It gives the path even
// with an error, so that the caller can remove what is there.
func copyBeside(path string) (string, error) {
	source, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer source.Close()
	target, err := os.CreateTemp(filepath.Dir(path), "cleanpass-*.tmp")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(target, source)
	return target.Name(), errors.Join(err, target.Close())
}

// newGroupName gives a name no database holds, so that the save has something to do.
func newGroupName() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "cleanpass-judge-" + hex.EncodeToString(random[:]), nil
}

// attachmentsDiffer names the first entry with other attachments, in any version, or gives an
// empty string. The saved copy holds one group more, and that is all a save adds.
func attachmentsDiffer(want, seen *snapshot.Snapshot) string {
	entries := map[snapshot.UUID]*snapshot.Entry{}
	seen.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			entries[g.Entries[i].UUID] = &g.Entries[i]
		}
	})
	place := ""
	want.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			entry := &g.Entries[i]
			if place == "" && !sameAttachmentsInAllVersions(entry, entries[entry.UUID]) {
				place = attachmentsPlace(entry.UUID)
			}
		}
	})
	return place
}

func sameAttachmentsInAllVersions(want, seen *snapshot.Entry) bool {
	if seen == nil || len(want.History) != len(seen.History) ||
		!sameAttachments(want.Attachments, seen.Attachments) {
		return false
	}
	for i := range want.History {
		if !sameAttachments(want.History[i].Attachments, seen.History[i].Attachments) {
			return false
		}
	}
	return true
}

func sameAttachments(want, seen []snapshot.AttachmentRef) bool {
	return slices.Equal(sortedByName(want), sortedByName(seen))
}

func sortedByName(refs []snapshot.AttachmentRef) []snapshot.AttachmentRef {
	sorted := slices.Clone(refs)
	slices.SortFunc(sorted, func(a, b snapshot.AttachmentRef) int { return cmp.Compare(a.Name, b.Name) })
	return sorted
}

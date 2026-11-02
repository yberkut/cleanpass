package testkit

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit/softkey"
)

// kdbxwebFiles is a script around unchanged files from npm: kdbxweb 2.1.1 and @xmldom/xmldom 0.8.15,
// both MIT. Only tests run them, on synthetic files. An independent implementation has to read
// what the tool writes with a key in software, and nothing else in reach can.
//
//go:embed testdata/kdbxweb
var kdbxwebFiles embed.FS

// KdbxwebTitles gives the titles of the entries as kdbxweb sees them, with the password and the
// key file of db and a key with this secret. It stops the test when kdbxweb cannot open the file.
// Argon2 of Node exists from 24.7. Without it the test is skipped, and CI has such a Node.
func (db DB) KdbxwebTitles(t testing.TB, secret string) []string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node: kdbxweb cannot read the file")
	}
	if err := exec.Command(node, "-e", "process.exit(require('crypto').argon2 ? 0 : 1)").Run(); err != nil {
		t.Skip("this node has no Argon2: kdbxweb cannot read the file")
	}
	dir := t.TempDir()
	files, err := fs.Sub(kdbxwebFiles, "testdata/kdbxweb")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dir, files); err != nil {
		t.Fatal(err)
	}
	arguments := []string{filepath.Join(dir, "read.cjs"), db.Path, db.Password, secret}
	if db.KeyFile != "" {
		arguments = append(arguments, db.KeyFile)
	}
	output, err := exec.Command(node, arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("kdbxweb did not open %s: %v\n%s", db.Path, err, output)
	}
	var titles []string
	if err := json.Unmarshal(output, &titles); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	return titles
}

// WithSoftKey writes a copy of the database to dst that also needs the response of a key with this
// secret. The library does this by itself here, not the code of the tool. A key file stays.
func (db DB) WithSoftKey(t testing.TB, dst, secret string) DB {
	t.Helper()
	lib := db.openWithLibrary(t)
	freshen(t, lib)
	hash := sha256.Sum256(softkey.Respond(secret, softkey.ChallengeOf(lib.Header.FileHeaders.KdfParameters.Salt[:])))
	lib.Credentials.Windows = hash[:]
	writeWithLibrary(t, lib, dst)
	result := db
	result.Path = dst
	return result
}

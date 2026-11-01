// Package testkit holds what the tests share: fixtures made by the real
// keepassxc-cli, and a way to run the built cleanpass as a separate process.
//
// Production code never imports this package. A test in cmd/cleanpass checks that.
package testkit

import (
	"bytes"
	_ "embed"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Password opens the fixtures unless a test picks another one. It is synthetic.
const Password = "synthetic-test-password"

//go:embed testdata/rich.xml
var richXML string

// RichXML returns a synthetic database with every kind of data the tool keeps.
// Import it with DB.Import. Every time stamp in it is unique, so a test can move
// exactly one of them with strings.Replace.
//
// What it holds:
//   - groups: Root, Робота, Кошик (3)
//   - entries: Пошта in Root, Друге in Робота, У кошику in Кошик (3)
//   - attachments: file.bin on Пошта and same-content.bin on Друге with the same
//     bytes, old.bin with other bytes only in the history of Пошта
//   - deleted objects: 1
//   - the newest time stamp that counts as a change: 2026-03-15T10:00:00Z, Пошта
func RichXML() string { return richXML }

// RichLastChange is the newest time stamp in RichXML that counts as a change.
const RichLastChange = "2026-03-15T10:00:00Z"

// DB names a database file and the credentials that open it. An empty Password
// means the database has no password.
type DB struct {
	Path     string
	Password string
	KeyFile  string
}

// CLIPath finds the real keepassxc-cli. Tests fail without it instead of skipping,
// so a missing KeePassXC can never look like a passed test.
func CLIPath() (string, error) {
	if path, err := exec.LookPath("keepassxc-cli"); err == nil {
		return path, nil
	}
	for _, path := range []string{
		`C:\Program Files\KeePassXC\keepassxc-cli.exe`,
		"/Applications/KeePassXC.app/Contents/MacOS/keepassxc-cli",
	} {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", errors.New("keepassxc-cli not found: install KeePassXC or add it to PATH")
}

func runCLI(t testing.TB, stdin string, args ...string) string {
	t.Helper()
	cli, err := CLIPath()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("keepassxc-cli %s: %v\n%s", args[0], err, stderr.String())
	}
	return stdout.String()
}

// Import creates the database from XML with the real keepassxc-cli, at the lowest
// KDF cost it allows.
func (db DB) Import(t testing.TB, xml string) DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(db.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.xml")
	if err := os.WriteFile(source, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"import", "-q", "-t", "100"}
	stdin := ""
	if db.Password != "" {
		args = append(args, "-p")
		stdin = db.Password + "\n" + db.Password + "\n"
	}
	if db.KeyFile != "" {
		args = append(args, "--set-key-file", db.KeyFile)
	}
	runCLI(t, stdin, append(args, source, db.Path)...)
	return db
}

// ImportWithEmptyPassword creates the database with the empty string as its password,
// which is a different key from no password at all. Only the real keepassxc-cli opens
// such a file after this, with an empty line on stdin.
func ImportWithEmptyPassword(t testing.TB, path, xml string) DB {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source.xml")
	if err := os.WriteFile(source, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	// An empty line as the password, then "y" to the question whether that is meant.
	runCLI(t, "\ny\n", "import", "-q", "-t", "100", "-p", source, path)
	runCLI(t, "\n", "ls", "-q", path)
	return DB{Path: path}
}

// CLI unlocks the database and runs one keepassxc-cli command on it. The file path
// goes right after the options, then args. It returns stdout.
func (db DB) CLI(t testing.TB, command string, args ...string) string {
	t.Helper()
	full := []string{command, "-q"}
	stdin := ""
	if db.Password == "" {
		full = append(full, "--no-password")
	} else {
		stdin = db.Password + "\n"
	}
	if db.KeyFile != "" {
		full = append(full, "-k", db.KeyFile)
	}
	full = append(full, db.Path)
	return runCLI(t, stdin, append(full, args...)...)
}

// ResavedGroup is the group Resave adds. Expected group counts must include it.
const ResavedGroup = "/made-by-keepassxc"

// Resave makes the real keepassxc-cli write the file: it adds the group ResavedGroup.
// After this the file on disk is a true KeePassXC file, whoever made it before.
func (db DB) Resave(t testing.TB) DB {
	t.Helper()
	db.CLI(t, "mkdir", ResavedGroup)
	return db
}

// NewKeyFile writes a key file with synthetic bytes and returns its path.
func NewKeyFile(t testing.TB, path string, seed byte) string {
	t.Helper()
	content := make([]byte, 64)
	for i := range content {
		content[i] = seed + byte(i)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

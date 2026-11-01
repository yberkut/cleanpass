package testkit

import (
	_ "embed"
	"os"
	"path/filepath"
	"testing"
)

// Ready files from other KDBX implementations. Neither KeePassXC nor the library can
// make them, so they lie in the test data. They hold only synthetic data, and Password
// opens them. Both have 64 MiB of Argon2, so each open takes about half a second.
var (
	// Made by kdbxweb with Argon2id. Then pykeepass 4 added an lxml element with the text
	// UnknownFieldValue at four levels and saved it. See UnknownFieldPaths.
	//go:embed testdata/unknown-fields.kdbx
	unknownFields []byte

	// Made by kdbxweb with Argon2d and without Meta.keyChanged, which leaves
	// <MasterKeyChanged/> empty.
	//go:embed testdata/empty-time.kdbx
	emptyTime []byte
)

// UnknownFieldValue is the text of every unknown element in UnknownFields.
const UnknownFieldValue = "keep-me"

// UnknownFieldPaths are the places of the unknown elements in UnknownFields.
var UnknownFieldPaths = []string{
	"KeePassFile/Meta/FutureMeta",
	"KeePassFile/Root/FutureRoot",
	"KeePassFile/Root/Group/Entry/FutureEntry",
	"KeePassFile/Root/Group/FutureGroup",
}

// UnknownFields writes the database from an independent implementation that holds data
// in elements KeePass does not know.
func UnknownFields(t testing.TB, path string) DB {
	t.Helper()
	return writeFixture(t, path, unknownFields)
}

// EmptyTime writes the database from an independent implementation that holds an empty
// time element.
func EmptyTime(t testing.TB, path string) DB {
	t.Helper()
	return writeFixture(t, path, emptyTime)
}

func writeFixture(t testing.TB, path string, content []byte) DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return DB{Path: path, Password: Password}
}

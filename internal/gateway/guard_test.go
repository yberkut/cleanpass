package gateway_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/testkit"
)

// status and merge read the verdict of the guard from the Snapshot.
func TestOpenGivesTheVerdictOfTheFieldGuard(t *testing.T) {
	db := testkit.UnknownFields(t, filepath.Join(t.TempDir(), "unknown.kdbx"))
	snap, err := gateway.Open(db.Path, gateway.Credentials{Password: []byte(db.Password)})
	if err != nil {
		t.Fatal(err)
	}
	var want []snapshot.FieldLoss
	for _, path := range testkit.UnknownFieldPaths {
		want = append(want, snapshot.FieldLoss{Path: path, InFile: 1, Kept: 0})
	}
	if !slices.Equal(snap.FieldLosses, want) {
		t.Errorf("verdict %v, want %v", snap.FieldLosses, want)
	}
}

func TestOpenGivesNoVerdictForAFileOfKeePassXC(t *testing.T) {
	snap, _ := richSnapshot(t)
	if len(snap.FieldLosses) != 0 {
		t.Errorf("false alarm: %v", snap.FieldLosses)
	}
}

func TestOpenTellsAnEmptyTimeFromDamage(t *testing.T) {
	db := testkit.EmptyTime(t, filepath.Join(t.TempDir(), "kdbxweb.kdbx"))
	_, err := gateway.Open(db.Path, gateway.Credentials{Password: []byte(db.Password)})
	if !errors.Is(err, gateway.ErrEmptyTime) {
		t.Errorf("error %v, want an empty time", err)
	}
}

// The content of such a file passes the checks of its hash, so only its author can damage
// it. The library takes it on trust and can panic.
func TestOpenGivesAnErrorForADamagedInnerHeader(t *testing.T) {
	const terminator = "\x00\x00\x00\x00\x00"
	for _, tc := range []struct {
		name  string
		inner string
	}{
		{"stream ID too short", "\x01\x01\x00\x00\x00\x03" + terminator},
		{"unknown item", "\x09\x00\x00\x00\x00" + terminator},
		{"negative size", "\x01\xFF\xFF\xFF\xFF"},
		{"item longer than the data", "\x01\xFF\xFF\xFF\x7F"},
		{"no end", "\x02\x01\x00\x00\x00\x00"},
		{"nothing at all", ""},
		{"no stream key", terminator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := testkit.RawFile{Major: 4, Minor: 1, XML: testkit.PlainXML, InnerHeader: []byte(tc.inner)}
			db := file.Write(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.Password)

			_, err := gateway.Open(db.Path, gateway.Credentials{Password: []byte(db.Password)})
			if !errors.Is(err, gateway.ErrCorrupt) {
				t.Errorf("error %v, want damage", err)
			}
		})
	}
}

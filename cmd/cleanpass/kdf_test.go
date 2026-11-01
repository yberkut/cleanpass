package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

func TestCompareExplainsAKDFItCannotUse(t *testing.T) {
	const (
		unknown = "незнайомий KDF"
		version = "версію Argon2, відмінну від 0x13"
		keyed   = "заповнено K або A"
		limits  = "виходять за межі"
	)
	argon, aes := testkit.Argon2idParams(), testkit.AESKDFParams()
	for _, tc := range []struct {
		name string
		kdf  []testkit.KDFParam
		want string
	}{
		{"unknown ID", testkit.WithParam(argon, "$UUID", bytes.Repeat([]byte{0xAB}, 16)), unknown},
		{"ID of a wrong size", testkit.WithParam(aes, "$UUID", []byte{0xAB}), unknown},
		{"no ID", testkit.WithoutParam(aes, "$UUID"), unknown},

		{"Argon2 version 1.0", testkit.WithParam(argon, "V", testkit.Uint32Param(0x10)), version},
		{"Argon2 version of the future", testkit.WithParam(argon, "V", testkit.Uint32Param(0x14)), version},
		{"Argon2 without a version", testkit.WithoutParam(argon, "V"), version},

		{"Argon2 with K", testkit.WithParam(argon, "K", []byte{1}), keyed},
		{"Argon2 with A", testkit.WithParam(argon, "A", []byte{1}), keyed},
		{"AES-KDF with K", testkit.WithParam(aes, "K", []byte{1}), keyed},
		{"AES-KDF with A", testkit.WithParam(aes, "A", []byte{1}), keyed},

		// The library would cut these values to a smaller type and get a wrong key.
		{"iterations over 32 bits", testkit.WithParam(argon, "I", testkit.Uint64Param(1<<32)), limits},
		{"parallelism over 8 bits", testkit.WithParam(argon, "P", testkit.Uint32Param(256)), limits},
		{"memory over 32 bits", testkit.WithParam(argon, "M", testkit.Uint64Param(1<<32)), limits},
		{"zero iterations", testkit.WithParam(argon, "I", testkit.Uint64Param(0)), limits},
		{"zero parallelism", testkit.WithParam(argon, "P", testkit.Uint32Param(0)), limits},
		{"zero memory", testkit.WithParam(argon, "M", testkit.Uint64Param(0)), limits},
		{"memory under the Argon2 minimum", testkit.WithParam(argon, "M", testkit.Uint64Param(7*1024)), limits},
		{"zero rounds", testkit.WithParam(aes, "R", testkit.Uint64Param(0)), limits},
		{"rounds no machine can finish", testkit.WithParam(aes, "R", testkit.Uint64Param(1<<40)), limits},

		// The library would use another salt or a zero cost and get a wrong key.
		{"short salt", testkit.WithParam(argon, "S", make([]byte, 16)), limits},
		{"long salt", testkit.WithParam(argon, "S", make([]byte, 33)), limits},
		{"no salt", testkit.WithoutParam(argon, "S"), limits},
		{"no iterations", testkit.WithoutParam(argon, "I"), limits},
		{"no memory", testkit.WithoutParam(argon, "M"), limits},
		{"no parallelism", testkit.WithoutParam(argon, "P"), limits},
		{"no rounds", testkit.WithoutParam(aes, "R"), limits},
		{"iterations of a wrong size", testkit.WithParam(argon, "I", testkit.Uint32Param(2)), limits},

		{"unknown parameter", testkit.WithParam(aes, "Z", []byte{1}), limits},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := testkit.RawFile{Major: 4, Minor: 1, XML: testkit.PlainXML, KDF: tc.kdf}
			db := file.Write(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.Password)

			got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
			if got.Code != 3 {
				t.Errorf("exit code %d, want 3\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
			}
			if !strings.Contains(got.Stderr, db.Path) || !strings.Contains(got.Stderr, tc.want) {
				t.Errorf("stderr has no %q about %s:\n%s", tc.want, db.Path, got.Stderr)
			}
			if strings.Contains(got.Stderr, "Неправильний") {
				t.Errorf("false message about the password:\n%s", got.Stderr)
			}
			if strings.Contains(got.Stderr, "Пароль для") {
				t.Errorf("asks for a password it cannot use:\n%s", got.Stderr)
			}
			wantUkrainian(t, got.Stdout, got.Stderr, db.Path)
		})
	}
}

// No hash protects the header of KDBX 3, and the library runs the KDF at once. Without this
// check a flipped high byte of the rounds makes the tool hang for years.
func TestCompareDoesNotHangOnAbsurdRoundsInKDBX3(t *testing.T) {
	db := testkit.RawFile{Major: 3, Minor: 1, XML: testkit.PlainXML, Rounds: 1 << 56}.Write(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.Password)

	got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
	if got.Code != 3 {
		t.Errorf("exit code %d, want 3", got.Code)
	}
	if want := "виходять за межі"; !strings.Contains(got.Stderr, want) {
		t.Errorf("stderr has no %q:\n%s", want, got.Stderr)
	}
}

func TestCompareAcceptsEmptyKAndA(t *testing.T) {
	params := testkit.WithParam(testkit.WithParam(testkit.AESKDFParams(), "K", []byte{}), "A", []byte{})
	db := testkit.RawFile{Major: 4, Minor: 1, XML: testkit.PlainXML, KDF: params}.Write(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.Password)

	got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstderr:\n%s", got.Code, got.Stderr)
	}
	wantField(t, passports(t, got.Stdout, db.Path)[db.Path], "Записів", "1")
}

// A flipped bit in the KDF parameters must not send the person to KeePassXC to change a
// KDF that was never there. The hash of the header tells the two cases apart.
func TestCompareCallsADamagedHeaderDamagedNotAnUnknownKDF(t *testing.T) {
	dir := t.TempDir()
	good := testkit.DB{Path: filepath.Join(dir, "good.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	content, err := os.ReadFile(good.Path)
	if err != nil {
		t.Fatal(err)
	}
	const name = "$UUID"
	at := bytes.Index(content, []byte(name))
	if at < 0 {
		t.Fatalf("no %s in the header", name)
	}
	content[at+len(name)+4] ^= 0xFF // the first byte of the value, after its four bytes of length
	damaged := filepath.Join(dir, "damaged.kdbx")
	if err := os.WriteFile(damaged, content, 0o600); err != nil {
		t.Fatal(err)
	}

	got := testkit.Run(t, testkit.Password+"\n", "compare", damaged)
	if got.Code != 3 {
		t.Errorf("exit code %d, want 3", got.Code)
	}
	if want := "Не вдалося прочитати базу " + damaged; !strings.Contains(got.Stderr, want) {
		t.Errorf("stderr has no %q:\n%s", want, got.Stderr)
	}
	if strings.Contains(got.Stderr, "незнайомий KDF") {
		t.Errorf("a damaged header is reported as an unknown KDF:\n%s", got.Stderr)
	}
}

// KDBX 3 has fields with a length of two bytes. A file cut inside its header must get an
// answer before any question.
func TestCompareSeesAHeaderCutOffBeforeAskingForAPassword(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		db   testkit.DB
	}{
		{"KDBX 4", testkit.RawFile{Major: 4, Minor: 1, XML: testkit.PlainXML}.Write(t, filepath.Join(dir, "4.kdbx"), testkit.Password)},
		{"KDBX 3", testkit.RawFile{Major: 3, Minor: 1, XML: testkit.PlainXML}.Write(t, filepath.Join(dir, "3.kdbx"), testkit.Password)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := os.ReadFile(tc.db.Path)
			if err != nil {
				t.Fatal(err)
			}
			cut := filepath.Join(dir, "cut.kdbx")
			if err := os.WriteFile(cut, content[:20], 0o600); err != nil {
				t.Fatal(err)
			}
			got := testkit.Run(t, tc.db.Password+"\n", "compare", cut)
			if got.Code != 3 {
				t.Errorf("exit code %d, want 3", got.Code)
			}
			if want := "Не вдалося прочитати базу " + cut; !strings.Contains(got.Stderr, want) {
				t.Errorf("stderr has no %q:\n%s", want, got.Stderr)
			}
			if strings.Contains(got.Stderr, "Пароль для") {
				t.Errorf("asks for a password:\n%s", got.Stderr)
			}
		})
	}
}

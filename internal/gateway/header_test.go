package gateway_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/testkit"
)

func readFile(t testing.TB, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func FuzzReadHeader(f *testing.F) {
	dir := f.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }

	keepassxc := testkit.DB{Path: path("keepassxc.kdbx"), Password: testkit.Password}.Import(f, testkit.RichXML())
	f.Add(readFile(f, keepassxc.Path))
	f.Add(readFile(f, keepassxc.WithEncryption(f, path("argon2d.kdbx"), testkit.ChaCha20, testkit.KDFArgon2d).Path))
	f.Add(readFile(f, keepassxc.WithEncryption(f, path("argon2id.kdbx"), testkit.Twofish, testkit.KDFArgon2id).Path))
	f.Add(readFile(f, testkit.NewKDBX3(f, path("library3.kdbx"), testkit.Password).Path))
	f.Add(readFile(f, testkit.UnknownFields(f, path("pykeepass.kdbx")).Path))
	f.Add(readFile(f, testkit.EmptyTime(f, path("kdbxweb.kdbx")).Path))
	for name, file := range map[string]testkit.RawFile{
		"raw3":      {Major: 3, Minor: 1, XML: testkit.PlainXML},
		"raw4":      {Major: 4, Minor: 1, XML: testkit.PlainXML},
		"argon":     {Major: 4, Minor: 1, XML: testkit.PlainXML, KDF: testkit.Argon2idParams()},
		"version":   {Major: 4, Minor: 1, XML: testkit.PlainXML, KDF: testkit.WithParam(testkit.Argon2idParams(), "V", testkit.Uint32Param(0x10))},
		"keyed":     {Major: 4, Minor: 1, XML: testkit.PlainXML, KDF: testkit.WithParam(testkit.AESKDFParams(), "K", []byte{1})},
		"no params": {Major: 4, Minor: 1, XML: testkit.PlainXML, KDF: []testkit.KDFParam{}},
	} {
		f.Add(readFile(f, file.Write(f, path(name+".kdbx"), testkit.Password).Path))
	}
	f.Add([]byte{})
	f.Add([]byte{0x03, 0xd9, 0xa2, 0x9a, 0x67, 0xfb, 0x4b, 0xb5, 0x01, 0x00, 0x04, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		header, err := gateway.ReadHeader(data)
		if err != nil {
			return
		}
		switch header.Major {
		case 3:
			if header.Salt != nil {
				t.Errorf("KDBX 3 has a salt of %d bytes", len(header.Salt))
			}
		case 4:
			if len(header.Salt) != 32 {
				t.Errorf("KDBX 4 has a salt of %d bytes", len(header.Salt))
			}
		default:
			t.Errorf("accepted KDBX %d", header.Major)
		}
	})
}

func TestReadHeaderReadsTheVersionAndTheSalt(t *testing.T) {
	dir := t.TempDir()
	salt := func(params []testkit.KDFParam) []byte {
		for _, param := range params {
			if param.Name == "S" {
				return param.Value
			}
		}
		t.Fatal("no salt in the parameters")
		return nil
	}
	for _, tc := range []struct {
		name         string
		major, minor uint16
		salt         []byte
	}{
		{"3.1", 3, 1, nil},
		{"4.0", 4, 0, salt(testkit.AESKDFParams())},
		{"4.1", 4, 1, salt(testkit.AESKDFParams())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := testkit.RawFile{Major: tc.major, Minor: tc.minor, XML: testkit.PlainXML}
			db := file.Write(t, filepath.Join(dir, tc.name+".kdbx"), testkit.Password)
			header, err := gateway.ReadHeader(readFile(t, db.Path))
			if err != nil {
				t.Fatal(err)
			}
			if header.Major != tc.major || header.Minor != tc.minor {
				t.Errorf("version %d.%d, want %d.%d", header.Major, header.Minor, tc.major, tc.minor)
			}
			if !bytes.Equal(header.Salt, tc.salt) {
				t.Errorf("salt %x, want %x", header.Salt, tc.salt)
			}
		})
	}
}

// headerEnd is the shortest start of the file that ReadHeader accepts. It also checks that
// every cut of a header gives an error and no panic, and that more bytes change nothing.
func headerEnd(t *testing.T, content []byte) int {
	t.Helper()
	end := -1
	for size := range len(content) + 1 {
		_, err := gateway.ReadHeader(content[:size])
		switch {
		case err == nil && end < 0:
			end = size
		case err != nil && end >= 0:
			t.Fatalf("accepts %d bytes, refuses %d: %v", end, size, err)
		}
	}
	if end < 0 {
		t.Fatal("the whole file is refused")
	}
	return end
}

func TestReadHeaderRefusesEveryCutOfTheHeader(t *testing.T) {
	dir := t.TempDir()
	for name, db := range map[string]testkit.DB{
		"KeePassXC": testkit.DB{Path: filepath.Join(dir, "keepassxc.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML()),
		"library":   testkit.NewKDBX3(t, filepath.Join(dir, "library3.kdbx"), testkit.Password),
		"pykeepass": testkit.UnknownFields(t, filepath.Join(dir, "pykeepass.kdbx")),
	} {
		t.Run(name, func(t *testing.T) {
			// The smallest real header: two 16 and 32 byte values with their lengths at least.
			if end := headerEnd(t, readFile(t, db.Path)); end < 60 {
				t.Errorf("accepts a header of %d bytes", end)
			}
		})
	}
}

// A file of KDBX 4 proves its header with a hash, so damage to any byte is told from a
// wrong password and from a KDF the tool cannot use.
func TestOpenCallsDamageToAnyByteOfTheHeaderDamage(t *testing.T) {
	dir := t.TempDir()
	for name, db := range map[string]testkit.DB{
		"AES-KDF":   testkit.DB{Path: filepath.Join(dir, "aes.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML()),
		"pykeepass": testkit.UnknownFields(t, filepath.Join(dir, "argon.kdbx")),
	} {
		t.Run(name, func(t *testing.T) {
			content := readFile(t, db.Path)
			damaged := filepath.Join(t.TempDir(), "damaged.kdbx")
			for at := range headerEnd(t, content) {
				flipped := bytes.Clone(content)
				flipped[at] ^= 0xFF
				if err := os.WriteFile(damaged, flipped, 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := gateway.Open(damaged, gateway.Credentials{Password: []byte(db.Password)})
				if !errors.Is(err, gateway.ErrCorrupt) && !errors.Is(err, gateway.ErrNotKDBX) {
					t.Errorf("byte %d: error %v, want damage", at, err)
				}
			}
		})
	}
}

func TestOpenChecksTheKDFBeforeAnyKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		file testkit.RawFile
		want error
	}{
		{"unknown KDF", testkit.RawFile{Major: 4, Minor: 1, KDF: testkit.WithParam(testkit.AESKDFParams(), "$UUID", make([]byte, 16))}, gateway.ErrKDFUnknown},
		{"Argon2 version", testkit.RawFile{Major: 4, Minor: 1, KDF: testkit.WithParam(testkit.Argon2idParams(), "V", testkit.Uint32Param(0x10))}, gateway.ErrKDFVersion},
		{"K", testkit.RawFile{Major: 4, Minor: 1, KDF: testkit.WithParam(testkit.Argon2idParams(), "K", []byte{1})}, gateway.ErrKDFKeyed},
		{"A", testkit.RawFile{Major: 4, Minor: 1, KDF: testkit.WithParam(testkit.Argon2idParams(), "A", []byte{1})}, gateway.ErrKDFKeyed},
		{"cost", testkit.RawFile{Major: 4, Minor: 1, KDF: testkit.WithParam(testkit.Argon2idParams(), "I", testkit.Uint64Param(1<<32))}, gateway.ErrKDFLimits},
		{"no KDF at all", testkit.RawFile{Major: 4, Minor: 1, KDF: []testkit.KDFParam{}}, gateway.ErrKDFUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := tc.file
			file.XML = testkit.PlainXML
			db := file.Write(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.Password)

			if _, err := gateway.Open(db.Path, gateway.Credentials{Password: []byte(db.Password)}); !errors.Is(err, tc.want) {
				t.Errorf("Open: error %v, want %v", err, tc.want)
			}
			if err := gateway.Check(db.Path); !errors.Is(err, tc.want) {
				t.Errorf("Check: error %v, want %v", err, tc.want)
			}
		})
	}
}

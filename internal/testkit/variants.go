package testkit

import (
	"crypto/rand"
	"crypto/sha256"
	"os"
	"testing"

	"github.com/tobischo/gokeepasslib/v3"
	w "github.com/tobischo/gokeepasslib/v3/wrappers"
)

// Cipher names an outer cipher for WithEncryption.
type Cipher int

// The ciphers WithEncryption can set.
const (
	AES256 Cipher = iota
	ChaCha20
	Twofish
)

// KDF names a key derivation function and its cost for WithEncryption.
type KDF struct {
	kind        kdfKind
	Rounds      uint64
	MemoryBytes uint64
	Iterations  uint64
	Parallelism uint32
}

type kdfKind int

const (
	kindAES kdfKind = iota
	kindArgon2d
	kindArgon2id
)

const testMemory = 1024 * 1024

// The KDF variants, all at a low cost.
var (
	KDFAES      = KDF{kind: kindAES, Rounds: 6000}
	KDFArgon2d  = KDF{kind: kindArgon2d, MemoryBytes: testMemory, Iterations: 2, Parallelism: 1}
	KDFArgon2id = KDF{kind: kindArgon2id, MemoryBytes: testMemory, Iterations: 2, Parallelism: 1}
)

// WithEncryption writes a copy of the database to dst with this cipher and KDF. The
// test library makes the change, then the real keepassxc-cli saves the file again, so
// that a true KeePassXC file lies on the disk. See Resave for the extra group.
func (db DB) WithEncryption(t testing.TB, dst string, cipher Cipher, kdf KDF) DB {
	t.Helper()
	lib := db.openWithLibrary(t)
	headers := lib.Header.FileHeaders
	switch cipher {
	case AES256:
		headers.CipherID, headers.EncryptionIV = gokeepasslib.CipherAES, make([]byte, 16)
	case ChaCha20:
		headers.CipherID, headers.EncryptionIV = gokeepasslib.CipherChaCha20, make([]byte, 12)
	case Twofish:
		headers.CipherID, headers.EncryptionIV = gokeepasslib.CipherTwoFish, make([]byte, 16)
	}
	params := headers.KdfParameters
	params.Rounds, params.Memory, params.Iterations, params.Parallelism, params.Version = 0, 0, 0, 0, 0
	switch kdf.kind {
	case kindAES:
		params.UUID, params.Rounds = gokeepasslib.KdfAES4, kdf.Rounds
	case kindArgon2d, kindArgon2id:
		params.UUID = gokeepasslib.KdfArgon2
		if kdf.kind == kindArgon2id {
			params.UUID = gokeepasslib.KdfArgon2id
		}
		params.Memory, params.Iterations, params.Parallelism, params.Version = kdf.MemoryBytes, kdf.Iterations, kdf.Parallelism, 19
	}
	freshen(t, lib)
	writeWithLibrary(t, lib, dst)
	result := db
	result.Path = dst
	return result.Resave(t)
}

// NewKDBX3 creates a KDBX 3.1 database with the library, because KeePassXC no longer
// makes this format. It has one group and one entry with one attachment and one older
// version.
func NewKDBX3(t testing.TB, path, password string) DB {
	t.Helper()
	lib := gokeepasslib.NewDatabase(gokeepasslib.WithDatabaseKDBXVersion3())
	lib.Credentials = gokeepasslib.NewPasswordCredentials(password)
	attachment := lib.Content.Meta.Binaries.Add([]byte("synthetic kdbx3 attachment"))

	value := func(key, content string, protected bool) gokeepasslib.ValueData {
		data := gokeepasslib.ValueData{Key: key, Value: gokeepasslib.V{Content: content}}
		if protected {
			data.Value.Protected = w.NewBoolWrapper(true)
		}
		return data
	}
	old := gokeepasslib.NewEntry()
	old.Values = []gokeepasslib.ValueData{value("Title", "kdbx3", false), value("Password", "SECRET-MARKER-old", true)}
	entry := gokeepasslib.NewEntry()
	entry.UUID = old.UUID
	entry.Values = []gokeepasslib.ValueData{value("Title", "kdbx3", false), value("Password", "SECRET-MARKER-new", true)}
	entry.Binaries = []gokeepasslib.BinaryReference{attachment.CreateReference("kdbx3.bin")}
	entry.Histories = []gokeepasslib.History{{Entries: []gokeepasslib.Entry{old}}}
	root := gokeepasslib.NewGroup()
	root.Name = "kdbx3-root"
	root.Entries = []gokeepasslib.Entry{entry}
	lib.Content.Root = &gokeepasslib.RootData{Groups: []gokeepasslib.Group{root}}

	writeWithLibrary(t, lib, path)
	return DB{Path: path, Password: password}
}

func (db DB) openWithLibrary(t testing.TB) *gokeepasslib.Database {
	t.Helper()
	file, err := os.Open(db.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	creds := &gokeepasslib.DBCredentials{}
	if db.Password != "" {
		hash := sha256.Sum256([]byte(db.Password))
		creds.Passphrase = hash[:]
	}
	if db.KeyFile != "" {
		if creds.Key, err = gokeepasslib.ParseKeyFile(db.KeyFile); err != nil {
			t.Fatal(err)
		}
	}
	lib := gokeepasslib.NewDatabase()
	lib.Credentials = creds
	if err := gokeepasslib.NewDecoder(file).Decode(lib); err != nil {
		t.Fatal(err)
	}
	if err := lib.UnlockProtectedEntries(); err != nil {
		t.Fatal(err)
	}
	return lib
}

// freshen makes new random values, as KeePassXC does on every save.
func freshen(t testing.TB, lib *gokeepasslib.Database) {
	t.Helper()
	headers := lib.Header.FileHeaders
	for _, part := range [][]byte{
		headers.MasterSeed, headers.EncryptionIV, headers.KdfParameters.Salt[:],
		lib.Content.InnerHeader.InnerRandomStreamKey,
	} {
		if _, err := rand.Read(part); err != nil {
			t.Fatal(err)
		}
	}
}

// writeWithLibrary saves a database whose values are unlocked. The lock must come
// after any change of the stream key, because Encode needs values locked with it.
func writeWithLibrary(t testing.TB, lib *gokeepasslib.Database, path string) {
	t.Helper()
	if err := lib.LockProtectedEntries(); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := gokeepasslib.NewEncoder(file).Encode(lib); err != nil {
		t.Fatal(err)
	}
}

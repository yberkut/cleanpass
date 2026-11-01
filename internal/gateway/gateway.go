// Package gateway is the KDBX Gateway: the only module that knows gokeepasslib.
// It opens a copy of a database and gives a neutral Snapshot.
package gateway

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tobischo/gokeepasslib/v3"

	"github.com/yberkut/cleanpass/internal/snapshot"
)

// The errors of this package. Their text is for developers. The cli package turns
// them into text for the human.
var (
	// ErrWrongCredentials means the password or the key file does not fit.
	ErrWrongCredentials = errors.New("wrong password or key file")
	// ErrKeyFile means the key file could not be read.
	ErrKeyFile = errors.New("key file cannot be read")
	// ErrNotKDBX means the file is not a KeePass database of KDBX 3 or 4.
	ErrNotKDBX = errors.New("not a KDBX 3 or 4 file")
	// ErrCorrupt means the file looks like a database, but it cannot be read.
	ErrCorrupt = errors.New("database file is damaged or uses an unsupported format")
	// ErrEmptyTime means the file holds a time element without a value. A save in
	// KeePassXC fills it in.
	ErrEmptyTime = errors.New("a time element is empty")
)

// Credentials are the keys of one copy.
type Credentials struct {
	// Password is nil when the database has no password. An empty, non-nil value is
	// the empty password, which is a different key.
	Password []byte
	// KeyFile is the path of the key file, or empty when there is none.
	KeyFile string
}

const signatureSize = 12

// Check tells, without any key, whether the tool can read the file. Errors from the
// file system are returned as they are, so errors.Is works with fs.ErrNotExist.
func Check(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	signature := make([]byte, signatureSize)
	_, err = io.ReadFull(file, signature)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrNotKDBX
	}
	if err != nil {
		return err
	}
	// The rest gets read only for a file that promises to be a database.
	if !hasKDBXSignature(signature) {
		return ErrNotKDBX
	}
	rest, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	_, err = ReadHeader(append(signature, rest...))
	return err
}

func hasKDBXSignature(header []byte) bool {
	if len(header) < signatureSize ||
		!bytes.Equal(header[:4], gokeepasslib.BaseSignature[:]) ||
		!bytes.Equal(header[4:8], gokeepasslib.SecondarySignature[:]) {
		return false
	}
	major := uint16(header[10]) | uint16(header[11])<<8
	return major == 3 || major == 4
}

// CheckKeyFile tells whether the key file can be used, before anyone types a password.
func CheckKeyFile(path string) error {
	_, err := readKeyFile(path)
	return err
}

func readKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeyFile, err)
	}
	key, err := gokeepasslib.ParseKeyData(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeyFile, err)
	}
	return key, nil
}

// Open reads a copy for reading only and gives its Snapshot.
func Open(path string, creds Credentials) (snap *snapshot.Snapshot, err error) {
	// The library panics on some content that passed the hash checks. A damaged file
	// must end in an error. What the panic says stays out of it: it comes from the file.
	defer func() {
		if recover() != nil {
			snap, err = nil, fmt.Errorf("%w: the library panicked", ErrCorrupt)
		}
	}()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if _, err := ReadHeader(data); err != nil {
		return nil, err
	}
	libCreds, err := newLibraryCredentials(creds)
	if err != nil {
		return nil, err
	}

	db := gokeepasslib.NewDatabase()
	db.Credentials = libCreds
	if err := gokeepasslib.NewDecoder(bytes.NewReader(data)).Decode(db); err != nil {
		return nil, decodeError(err, db)
	}
	if db.Content == nil || db.Content.Meta == nil || db.Content.Root == nil {
		return nil, fmt.Errorf("%w: no Meta or Root element", ErrCorrupt)
	}
	losses, err := guardFields(db)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if err := db.UnlockProtectedEntries(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	snap, err = fromDatabase(db)
	if err != nil {
		return nil, err
	}
	snap.FieldLosses = losses
	return snap, nil
}

func newLibraryCredentials(creds Credentials) (*gokeepasslib.DBCredentials, error) {
	result := &gokeepasslib.DBCredentials{}
	if creds.Password != nil {
		hash := sha256.Sum256(creds.Password)
		result.Passphrase = hash[:]
	}
	if creds.KeyFile != "" {
		key, err := readKeyFile(creds.KeyFile)
		if err != nil {
			return nil, err
		}
		result.Key = key
	}
	return result, nil
}

// decodeError sorts the errors of the library. It marks a wrong password only by the
// text of the message, because the library keeps those two errors private.
func decodeError(err error, db *gokeepasslib.Database) error {
	if strings.HasPrefix(err.Error(), "Wrong password?") {
		return ErrWrongCredentials
	}
	if hasEmptyTime(db) {
		return ErrEmptyTime
	}
	return fmt.Errorf("%w: %w", ErrCorrupt, err)
}

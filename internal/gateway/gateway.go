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

// Check tells, without any key, whether the file can be a database. Errors from the
// file system are returned as they are, so errors.Is works with fs.ErrNotExist.
func Check(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, signatureSize)
	_, err = io.ReadFull(file, header)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrNotKDBX
	}
	if err != nil {
		return err
	}
	if !hasKDBXSignature(header) {
		return ErrNotKDBX
	}
	return nil
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
func Open(path string, creds Credentials) (*snapshot.Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !hasKDBXSignature(data) {
		return nil, ErrNotKDBX
	}
	libCreds, err := newLibraryCredentials(creds)
	if err != nil {
		return nil, err
	}

	db := gokeepasslib.NewDatabase()
	db.Credentials = libCreds
	if err := gokeepasslib.NewDecoder(bytes.NewReader(data)).Decode(db); err != nil {
		return nil, decodeError(err)
	}
	if db.Content == nil || db.Content.Meta == nil || db.Content.Root == nil {
		return nil, fmt.Errorf("%w: no Meta or Root element", ErrCorrupt)
	}
	if err := db.UnlockProtectedEntries(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return fromDatabase(db)
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
func decodeError(err error) error {
	if strings.HasPrefix(err.Error(), "Wrong password?") {
		return ErrWrongCredentials
	}
	return fmt.Errorf("%w: %w", ErrCorrupt, err)
}

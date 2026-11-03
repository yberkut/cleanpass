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
	// ErrYubiKeyKDBX3 means a YubiKey for a KDBX 3 file. That format uses the response in a way
	// the library gives no access to.
	ErrYubiKeyKDBX3 = errors.New("a YubiKey works only with KDBX 4")
)

// Credentials are the keys of one copy.
type Credentials struct {
	// Password is nil when the database has no password. An empty, non-nil value is
	// the empty password, which is a different key.
	Password []byte
	// KeyFile is the path of the key file, or empty when there is none.
	KeyFile string
	// YubiKey is the slot of a hardware key, or nil when there is none.
	YubiKey YubiKey
}

// YubiKey is a slot of a challenge-response key. Each file has its own salt, so the response is
// no fixed value: it has to come from the key for the file at hand.
type YubiKey interface {
	// Slot gives the number of the slot.
	Slot() int
	// Respond gives the response to the KDF salt of a file. It may wait for a touch.
	Respond(salt []byte) ([]byte, error)
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

// CheckYubiKey tells, before anyone types a password, whether a YubiKey can open the file.
func CheckYubiKey(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	header, err := ReadHeader(data)
	if err != nil {
		return err
	}
	if header.Major != outputMajor {
		return ErrYubiKeyKDBX3
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
	header, err := ReadHeader(data)
	if err != nil {
		return nil, err
	}
	if creds.YubiKey != nil && header.Major != outputMajor {
		return nil, ErrYubiKeyKDBX3
	}
	libCreds, err := newLibraryCredentials(creds)
	if err != nil {
		return nil, err
	}
	// The key is asked before Decode, because Decode runs the KDF at once.
	if err := addResponse(libCreds, creds.YubiKey, header.Salt); err != nil {
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
	snap.FileDigest = sha256.Sum256(data)
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

// addResponse puts the hash of the response of a key into the composite key. The library has no
// challenge-response, but its Windows field stands where KeePassXC adds the response, by the source
// of KeePassXC. kdbxweb confirms this order. Only a real key can confirm it for KeePassXC.
func addResponse(libCreds *gokeepasslib.DBCredentials, key YubiKey, salt []byte) error {
	if key == nil {
		return nil
	}
	response, err := key.Respond(salt)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(response)
	libCreds.Windows = hash[:]
	return nil
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

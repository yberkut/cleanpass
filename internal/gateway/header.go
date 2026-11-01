package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/tobischo/gokeepasslib/v3"
)

// The errors about KDF parameters the tool cannot use. The library would not refuse these
// files: it would run the wrong KDF and call the result a wrong password. Its Decode also
// runs the KDF before it returns, so ReadHeader has to look first.
var (
	// ErrKDFUnknown means the file names a KDF the library does not have.
	ErrKDFUnknown = errors.New("unknown KDF")
	// ErrKDFVersion means Argon2 of another version than 0x13, the only one the library runs.
	ErrKDFVersion = errors.New("Argon2 version is not 0x13")
	// ErrKDFKeyed means non-empty K or A. The library reads them but never gives them to
	// the KDF, and it would write A under the wrong name.
	ErrKDFKeyed = errors.New("KDF parameter K or A is not empty")
	// ErrKDFLimits means a parameter that is missing, unknown or outside what the library
	// handles. It casts the Argon2 costs to smaller types without a check, and keeps
	// exactly 32 bytes of salt.
	ErrKDFLimits = errors.New("KDF parameters are missing or out of range")
)

// Header is what the own parser reads from a file, before any key and any run of a KDF.
type Header struct {
	// Major and Minor are the KDBX version, for example 4 and 1.
	Major, Minor uint16
	// Salt is the KDF salt of KDBX 4, always 32 bytes. KDBX 3 has none here.
	Salt []byte
}

const (
	headerFieldEnd             = 0
	headerFieldTransformRounds = 6
	headerFieldKDF             = 11

	kdfSaltSize       = 32
	argon2Version     = 0x13
	argon2MinKiBPerP  = 8 // RFC 9106: the library raises a smaller memory without a word
	kibibyte          = 1024
	dictionaryMajor   = 1
	headerHashSize    = sha256.Size
	dictionaryVerSize = 2

	// maxAESRounds is far above any real cost. It takes minutes on a fast machine, and a
	// value above it can only come from damage.
	maxAESRounds = math.MaxInt32
)

// ReadHeader checks that the file starts as a database the tool can read. Damaged or
// hostile input gives an error and never a panic: a fuzz test holds it to that.
func ReadHeader(data []byte) (Header, error) {
	if !hasKDBXSignature(data) {
		return Header{}, ErrNotKDBX
	}
	header := Header{
		Minor: binary.LittleEndian.Uint16(data[8:]),
		Major: binary.LittleEndian.Uint16(data[10:]),
	}
	fields, end, err := walkHeaderFields(data, header.Major)
	if err != nil {
		return Header{}, err
	}
	if header.Major != 4 {
		// No hash protects this header, and the library runs the KDF at once. A flipped
		// high byte of the rounds would keep it busy for years.
		if rounds, ok := uint64Item(fields, headerFieldTransformRounds); !ok || !validAESRounds(rounds) {
			return Header{}, fmt.Errorf("%w: rounds", ErrKDFLimits)
		}
		return header, nil
	}
	dictionary, ok := fields[headerFieldKDF]
	if !ok {
		return Header{}, fmt.Errorf("%w: no KDF parameters", ErrCorrupt)
	}
	items, err := readDictionary(dictionary)
	if err != nil {
		return Header{}, err
	}
	header.Salt, err = checkKDF(items)
	if err != nil {
		// A flipped bit looks like a strange KDF. Only the hash of the header can tell.
		if !headerHashFits(data, end) {
			return Header{}, fmt.Errorf("%w: header hash does not fit", ErrCorrupt)
		}
		return Header{}, err
	}
	return header, nil
}

// walkHeaderFields goes over the fields of the outer header and says where the header
// ends. KDBX 3 has two bytes of length in a field, KDBX 4 has four. Of a repeated field
// the last value counts, as in the library, so the check looks at what the library uses.
func walkHeaderFields(data []byte, major uint16) (fields map[byte][]byte, end int, err error) {
	fields = map[byte][]byte{}
	rest := data[signatureSize:]
	for {
		if len(rest) < 1 {
			return nil, 0, fmt.Errorf("%w: header has no end", ErrCorrupt)
		}
		id := rest[0]
		var value []byte
		var ok bool
		if major == 4 {
			value, rest, ok = cutSized32(rest[1:])
		} else {
			value, rest, ok = cutSized16(rest[1:])
		}
		if !ok {
			return nil, 0, fmt.Errorf("%w: header field does not fit in the file", ErrCorrupt)
		}
		if id == headerFieldEnd {
			return fields, len(data) - len(rest), nil
		}
		fields[id] = value
	}
}

// readDictionary reads the KDF parameters: two bytes of version, then items of type, name
// and value until type 0.
func readDictionary(dictionary []byte) (map[string][]byte, error) {
	if len(dictionary) < dictionaryVerSize {
		return nil, fmt.Errorf("%w: KDF parameters too short", ErrCorrupt)
	}
	if binary.LittleEndian.Uint16(dictionary)>>8 != dictionaryMajor {
		return nil, fmt.Errorf("%w: KDF parameters of an unknown version", ErrCorrupt)
	}
	rest := dictionary[dictionaryVerSize:]
	items := map[string][]byte{}
	for {
		if len(rest) < 1 {
			return nil, fmt.Errorf("%w: KDF parameters have no end", ErrCorrupt)
		}
		kind := rest[0]
		rest = rest[1:]
		if kind == 0 {
			return items, nil
		}
		name, afterName, ok := cutSized32(rest)
		if !ok {
			return nil, fmt.Errorf("%w: KDF parameter name does not fit", ErrCorrupt)
		}
		value, afterValue, ok := cutSized32(afterName)
		if !ok {
			return nil, fmt.Errorf("%w: KDF parameter value does not fit", ErrCorrupt)
		}
		// Of a repeated item the last value counts, as in the library.
		items[string(name)] = value
		rest = afterValue
	}
}

// checkKDF refuses the parameters that would give a wrong key, and returns the salt.
func checkKDF(items map[string][]byte) ([]byte, error) {
	id := items["$UUID"]
	isArgon2 := bytes.Equal(id, gokeepasslib.KdfArgon2) || bytes.Equal(id, gokeepasslib.KdfArgon2id)
	isAES := bytes.Equal(id, gokeepasslib.KdfAES4) || bytes.Equal(id, gokeepasslib.KdfAES3)
	if !isArgon2 && !isAES {
		return nil, ErrKDFUnknown
	}
	for name := range items {
		switch name {
		case "$UUID", "S", "R", "V", "I", "M", "P", "K", "A":
		default:
			// The name is not in the error: it comes from the file.
			return nil, fmt.Errorf("%w: unknown parameter", ErrKDFLimits)
		}
	}
	if len(items["K"]) > 0 || len(items["A"]) > 0 {
		return nil, ErrKDFKeyed
	}
	salt := items["S"]
	if len(salt) != kdfSaltSize {
		return nil, fmt.Errorf("%w: salt of %d bytes", ErrKDFLimits, len(salt))
	}
	if isAES {
		if rounds, ok := uint64Item(items, "R"); !ok || !validAESRounds(rounds) {
			return nil, fmt.Errorf("%w: rounds", ErrKDFLimits)
		}
		return salt, nil
	}

	if version, ok := uint32Item(items, "V"); !ok || version != argon2Version {
		return nil, ErrKDFVersion
	}
	iterations, okIterations := uint64Item(items, "I")
	if !okIterations || iterations == 0 || iterations > math.MaxUint32 {
		return nil, fmt.Errorf("%w: iterations", ErrKDFLimits)
	}
	parallelism, okParallelism := uint32Item(items, "P")
	if !okParallelism || parallelism == 0 || parallelism > math.MaxUint8 {
		return nil, fmt.Errorf("%w: parallelism", ErrKDFLimits)
	}
	memory, okMemory := uint64Item(items, "M")
	if !okMemory || memory > math.MaxUint32 || memory/kibibyte < argon2MinKiBPerP*uint64(parallelism) {
		return nil, fmt.Errorf("%w: memory", ErrKDFLimits)
	}
	return salt, nil
}

func validAESRounds(rounds uint64) bool { return rounds > 0 && rounds <= maxAESRounds }

func uint32Item[K comparable](items map[K][]byte, name K) (uint32, bool) {
	value := items[name]
	if len(value) != 4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(value), true
}

func uint64Item[K comparable](items map[K][]byte, name K) (uint64, bool) {
	value := items[name]
	if len(value) != 8 {
		return 0, false
	}
	return binary.LittleEndian.Uint64(value), true
}

// headerHashFits checks the SHA-256 that follows the header of KDBX 4.
func headerHashFits(data []byte, end int) bool {
	if len(data)-end < headerHashSize {
		return false
	}
	hash := sha256.Sum256(data[:end])
	return bytes.Equal(hash[:], data[end:end+headerHashSize])
}

// cutSized32 cuts a value with four bytes of length in front from the data.
func cutSized32(data []byte) (value, rest []byte, ok bool) {
	if len(data) < 4 {
		return nil, nil, false
	}
	return cutValue(data[4:], uint64(binary.LittleEndian.Uint32(data)))
}

// cutSized16 cuts a value with two bytes of length in front from the data.
func cutSized16(data []byte) (value, rest []byte, ok bool) {
	if len(data) < 2 {
		return nil, nil, false
	}
	return cutValue(data[2:], uint64(binary.LittleEndian.Uint16(data)))
}

func cutValue(data []byte, size uint64) (value, rest []byte, ok bool) {
	if size > uint64(len(data)) {
		return nil, nil, false
	}
	return data[:size], data[size:], true
}

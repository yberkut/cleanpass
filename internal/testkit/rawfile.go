package testkit

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/tobischo/gokeepasslib/v3"
)

// PlainXML is a small valid database for RawFile. A test changes it with strings.Replace.
const PlainXML = `<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<KeePassFile>
	<Meta>
		<Generator>cleanpass-tests</Generator>
		<MasterKeyChanged>2026-02-03T10:00:00Z</MasterKeyChanged>
	</Meta>
	<Root>
		<Group>
			<UUID>AAAAAAAAAAAAAAAAAAAAEA==</UUID>
			<Name>Root</Name>
			<IsExpanded>True</IsExpanded>
			<Entry>
				<UUID>AAAAAAAAAAAAAAAAAAABAA==</UUID>
				<Times>
					<LastModificationTime>2026-03-15T10:00:00Z</LastModificationTime>
				</Times>
				<String><Key>Title</Key><Value>Запис</Value></String>
			</Entry>
		</Group>
		<DeletedObjects>
			<DeletedObject><UUID>AAAAAAAAAAAAAAAAAAAHdw==</UUID><DeletionTime>2026-02-10T10:00:00Z</DeletionTime></DeletedObject>
		</DeletedObjects>
	</Root>
</KeePassFile>
`

// RawFile is a database that the tests write byte by byte, with no KDBX library. It makes
// the files neither KeePassXC nor the library would write: any XML, attachments no entry
// refers to, any KDF parameters. The real keepassxc-cli opens what it writes.
//
// The file has AES-256 and is really encrypted with the key of AESKDFParams. Other KDF
// parameters only get declared in the header, with a true SHA-256 of that header. So a
// reader that believes them gets a wrong key, the same as for a file it cannot handle.
type RawFile struct {
	// Major and Minor are the KDBX version: 3.1 or 4.x.
	Major, Minor uint16
	// XML is the document. It may have no protected values.
	XML string
	// Attachments go to the inner header of KDBX 4, in order. XML decides who refers to them.
	Attachments [][]byte
	// InnerHeader replaces the inner header of KDBX 4 when it is not nil.
	InnerHeader []byte
	// KDF replaces AESKDFParams in the header of KDBX 4 when it is not nil.
	KDF []KDFParam
	// NoCompression leaves the content without gzip, so the padding follows the XML.
	NoCompression bool
	// Rounds replaces the rounds in the header of KDBX 3 when it is not zero. The file is
	// still encrypted with the real ones.
	Rounds uint64
	// Comment goes to the comment field of the outer header.
	Comment []byte
	// PublicCustomData goes to the outer header of KDBX 4 when it is not nil.
	PublicCustomData []KDFParam
}

// KDFParam is one item of the KDF parameters in the header of KDBX 4.
type KDFParam struct {
	Name  string
	Value []byte
}

const rawFileRounds = 1000

var rawFileSeed = bytes.Repeat([]byte{0x5A}, 32)

// AESKDFParams are the parameters a RawFile is really encrypted with.
func AESKDFParams() []KDFParam {
	return []KDFParam{
		{"$UUID", gokeepasslib.KdfAES4},
		{"R", Uint64Param(rawFileRounds)},
		{"S", rawFileSeed},
	}
}

// Argon2idParams are parameters KeePassXC could write. See RawFile about the key.
func Argon2idParams() []KDFParam {
	return []KDFParam{
		{"$UUID", gokeepasslib.KdfArgon2id},
		{"V", Uint32Param(0x13)},
		{"I", Uint64Param(2)},
		{"M", Uint64Param(1024 * 1024)},
		{"P", Uint32Param(1)},
		{"S", rawFileSeed},
	}
}

// Uint32Param encodes a value of a KDF parameter.
func Uint32Param(value uint32) []byte { return binary.LittleEndian.AppendUint32(nil, value) }

// Uint64Param encodes a value of a KDF parameter.
func Uint64Param(value uint64) []byte { return binary.LittleEndian.AppendUint64(nil, value) }

// WithParam returns the parameters with this one set: in place if the name is there,
// at the end otherwise.
func WithParam(params []KDFParam, name string, value []byte) []KDFParam {
	result := append([]KDFParam{}, params...)
	for i := range result {
		if result[i].Name == name {
			result[i].Value = value
			return result
		}
	}
	return append(result, KDFParam{name, value})
}

// WithoutParam returns the parameters without this one.
func WithoutParam(params []KDFParam, name string) []KDFParam {
	result := []KDFParam{}
	for _, param := range params {
		if param.Name != name {
			result = append(result, param)
		}
	}
	return result
}

// Write saves the file with this password.
func (f RawFile) Write(t testing.TB, path, password string) DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var content []byte
	if f.Major == 3 {
		content = f.bytes3(t, password)
	} else {
		content = f.bytes4(t, password)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return DB{Path: path, Password: password}
}

func (f RawFile) bytes4(t testing.TB, password string) []byte {
	t.Helper()
	masterSeed, iv := randomBytes(t, 32), randomBytes(t, 16)
	transformed := transformKey(t, password)

	header := rawSignature(f.Major, f.Minor)
	put := func(id byte, value []byte) {
		header = append(header, id)
		header = binary.LittleEndian.AppendUint32(header, uint32(len(value)))
		header = append(header, value...)
	}
	if f.Comment != nil {
		put(1, f.Comment)
	}
	put(2, gokeepasslib.CipherAES)
	put(3, f.compressionFlag())
	put(4, masterSeed)
	put(7, iv)
	params := f.KDF
	if params == nil {
		params = AESKDFParams()
	}
	put(11, rawDictionary(params))
	if f.PublicCustomData != nil {
		put(12, rawDictionary(f.PublicCustomData))
	}
	put(0, []byte("\r\n\r\n"))

	hmacBase := sha512.New()
	hmacBase.Write(masterSeed)
	hmacBase.Write(transformed)
	hmacBase.Write([]byte{1})
	baseKey := hmacBase.Sum(nil)

	inner := f.InnerHeader
	if inner == nil {
		inner = f.innerHeader(t)
	}
	plain := append(append([]byte{}, inner...), f.XML...)
	encrypted := encryptCBC(t, masterKey(masterSeed, transformed), iv, f.compress(t, plain))

	file := append([]byte{}, header...)
	headerHash := sha256.Sum256(header)
	file = append(file, headerHash[:]...)
	file = append(file, blockMAC(baseKey, 0xFFFFFFFFFFFFFFFF, header)...)
	for index, block := range [][]byte{encrypted, {}} {
		lengthAndData := binary.LittleEndian.AppendUint32(nil, uint32(len(block)))
		file = append(file, blockMAC(baseKey, uint64(index), lengthAndData, block)...)
		file = append(file, lengthAndData...)
		file = append(file, block...)
	}
	return file
}

func (f RawFile) innerHeader(t testing.TB) []byte {
	t.Helper()
	var inner []byte
	put := func(id byte, value []byte) {
		inner = append(inner, id)
		inner = binary.LittleEndian.AppendUint32(inner, uint32(len(value)))
		inner = append(inner, value...)
	}
	put(1, Uint32Param(gokeepasslib.ChaChaStreamID))
	put(2, randomBytes(t, 64))
	for _, attachment := range f.Attachments {
		put(3, append([]byte{0}, attachment...))
	}
	put(0, nil)
	return inner
}

func (f RawFile) bytes3(t testing.TB, password string) []byte {
	t.Helper()
	masterSeed, iv, startBytes := randomBytes(t, 32), randomBytes(t, 16), randomBytes(t, 32)
	transformed := transformKey(t, password)

	header := rawSignature(f.Major, f.Minor)
	put := func(id byte, value []byte) {
		header = append(header, id)
		header = binary.LittleEndian.AppendUint16(header, uint16(len(value)))
		header = append(header, value...)
	}
	if f.Comment != nil {
		put(1, f.Comment)
	}
	put(2, gokeepasslib.CipherAES)
	put(3, f.compressionFlag())
	put(4, masterSeed)
	put(5, rawFileSeed)
	rounds := f.Rounds
	if rounds == 0 {
		rounds = rawFileRounds
	}
	put(6, Uint64Param(rounds))
	put(7, iv)
	put(8, randomBytes(t, 32))
	put(9, startBytes)
	put(10, Uint32Param(gokeepasslib.SalsaStreamID))
	put(0, []byte("\r\n\r\n"))

	content := f.compress(t, []byte(f.XML))
	hash := sha256.Sum256(content)
	plain := append([]byte{}, startBytes...)
	plain = binary.LittleEndian.AppendUint32(plain, 0)
	plain = append(plain, hash[:]...)
	plain = binary.LittleEndian.AppendUint32(plain, uint32(len(content)))
	plain = append(plain, content...)
	plain = binary.LittleEndian.AppendUint32(plain, 1)
	plain = append(plain, make([]byte, 32)...)
	plain = binary.LittleEndian.AppendUint32(plain, 0)

	return append(header, encryptCBC(t, masterKey(masterSeed, transformed), iv, plain)...)
}

func rawSignature(major, minor uint16) []byte {
	signature := append([]byte{}, gokeepasslib.BaseSignature[:]...)
	signature = append(signature, gokeepasslib.SecondarySignature[:]...)
	signature = binary.LittleEndian.AppendUint16(signature, minor)
	return binary.LittleEndian.AppendUint16(signature, major)
}

func rawDictionary(params []KDFParam) []byte {
	dictionary := binary.LittleEndian.AppendUint16(nil, 0x0100)
	for _, param := range params {
		var kind byte = 0x42
		switch param.Name {
		case "R", "I", "M":
			kind = 0x05
		case "V", "P":
			kind = 0x04
		}
		dictionary = append(dictionary, kind)
		dictionary = binary.LittleEndian.AppendUint32(dictionary, uint32(len(param.Name)))
		dictionary = append(dictionary, param.Name...)
		dictionary = binary.LittleEndian.AppendUint32(dictionary, uint32(len(param.Value)))
		dictionary = append(dictionary, param.Value...)
	}
	return append(dictionary, 0)
}

func (f RawFile) compressionFlag() []byte {
	if f.NoCompression {
		return Uint32Param(gokeepasslib.NoCompressionFlag)
	}
	return Uint32Param(gokeepasslib.GzipCompressionFlag)
}

func (f RawFile) compress(t testing.TB, plain []byte) []byte {
	t.Helper()
	if f.NoCompression {
		return plain
	}
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// transformKey runs AES-KDF on the password, as the file with AESKDFParams says.
func transformKey(t testing.TB, password string) []byte {
	t.Helper()
	hashed := sha256.Sum256([]byte(password))
	composite := sha256.Sum256(hashed[:])
	block, err := aes.NewCipher(rawFileSeed)
	if err != nil {
		t.Fatal(err)
	}
	key := composite
	for range rawFileRounds {
		block.Encrypt(key[:16], key[:16])
		block.Encrypt(key[16:], key[16:])
	}
	transformed := sha256.Sum256(key[:])
	return transformed[:]
}

func masterKey(masterSeed, transformed []byte) []byte {
	hash := sha256.New()
	hash.Write(masterSeed)
	hash.Write(transformed)
	return hash.Sum(nil)
}

func encryptCBC(t testing.TB, key, iv, plain []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)
	return encrypted
}

// blockMAC is the HMAC of KDBX 4 for a block index: the header takes the highest index.
func blockMAC(baseKey []byte, index uint64, parts ...[]byte) []byte {
	indexBytes := binary.LittleEndian.AppendUint64(nil, index)
	keyHash := sha512.New()
	keyHash.Write(indexBytes)
	keyHash.Write(baseKey)
	mac := hmac.New(sha256.New, keyHash.Sum(nil))
	if index != 0xFFFFFFFFFFFFFFFF {
		mac.Write(indexBytes)
	}
	for _, part := range parts {
		mac.Write(part)
	}
	return mac.Sum(nil)
}

func randomBytes(t testing.TB, size int) []byte {
	t.Helper()
	result := make([]byte, size)
	if _, err := rand.Read(result); err != nil {
		t.Fatal(err)
	}
	return result
}

// WithoutCompression writes a copy of the database to dst with the content not compressed,
// then the real keepassxc-cli saves it again. See Resave for the extra group.
func (db DB) WithoutCompression(t testing.TB, dst string) DB {
	t.Helper()
	lib := db.openWithLibrary(t)
	lib.Header.FileHeaders.CompressionFlags = gokeepasslib.NoCompressionFlag
	freshen(t, lib)
	writeWithLibrary(t, lib, dst)
	result := db
	result.Path = dst
	return result.Resave(t)
}

package main_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

// The matrix of boundary 1 of the spec. All copies here have been saved by the real KeePassXC, the
// built tool merges them as a separate process, and the real KeePassXC reads the result. The paths
// hold Cyrillic letters and spaces in every case.

// A cipher and a KDF as a test sets them, and as the real KeePassXC names them in db-info. Those
// names do not depend on the language of the system.
type matrixCipher struct {
	id   testkit.Cipher
	name string
}

type matrixKDF struct {
	kdf  testkit.KDF
	name string
}

var (
	matrixCiphers = []matrixCipher{{testkit.AES256, "AES"}, {testkit.ChaCha20, "ChaCha20"}, {testkit.Twofish, "Twofish"}}
	matrixKDFs    = []matrixKDF{{testkit.KDFAES, "AES"}, {testkit.KDFArgon2d, "Argon2d"}, {testkit.KDFArgon2id, "Argon2id"}}
)

// matrixKeys says what opens each copy. A copy has its number as an index, and the last copy is the
// newest one, so its keys go to the result.
type matrixKeys func(t *testing.T, dir string, copies int) []testkit.DB

func onePassword(_ *testing.T, _ string, copies int) []testkit.DB {
	keys := make([]testkit.DB, copies)
	for i := range keys {
		keys[i].Password = "спільний пароль"
	}
	return keys
}

func differentPasswords(_ *testing.T, _ string, copies int) []testkit.DB {
	keys := make([]testkit.DB, copies)
	for i := range keys {
		keys[i].Password = fmt.Sprintf("пароль копії %d", i)
	}
	return keys
}

func passwordAndKeyFile(t *testing.T, dir string, copies int) []testkit.DB {
	t.Helper()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "мій ключ.bin"), 11)
	keys := make([]testkit.DB, copies)
	for i := range keys {
		keys[i].Password, keys[i].KeyFile = testkit.Password, key
	}
	return keys
}

func onlyKeyFile(t *testing.T, dir string, copies int) []testkit.DB {
	t.Helper()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "лише ключ.bin"), 12)
	keys := make([]testkit.DB, copies)
	for i := range keys {
		keys[i].KeyFile = key
	}
	return keys
}

func differentPasswordsAndKeyFiles(t *testing.T, dir string, copies int) []testkit.DB {
	t.Helper()
	keys := make([]testkit.DB, copies)
	for i := range keys {
		keys[i].Password = fmt.Sprintf("пароль копії %d", i)
		keys[i].KeyFile = testkit.NewKeyFile(t, filepath.Join(dir, fmt.Sprintf("ключ копії %d.bin", i)), byte(20+i))
	}
	return keys
}

// matrixCopies makes the copies of one database. Each has an entry of its own. The last copy is the
// newest one, with a time that lies after the present saves of the others. That time is the
// creation of an entry only this copy has, so it asks nothing.
func matrixCopies(t *testing.T, dir string, keys []testkit.DB, encryption func(i int) (matrixCipher, matrixKDF)) []testkit.DB {
	t.Helper()
	copies := make([]testkit.DB, len(keys))
	for i, key := range keys {
		xml := replaced(t, mergeableXML(), "<Group><UUID>AAAAAAAAAAAAAAAAAAAAIA==</UUID>", matrixEntry(i)+"<Group><UUID>AAAAAAAAAAAAAAAAAAAAIA==</UUID>")
		if i == len(keys)-1 {
			xml = replaced(t, xml,
				"SECRET-MARKER-друге", divergedSecret,
				"<LastModificationTime>2026-03-01T10:00:03Z<", "<LastModificationTime>2026-05-01T10:00:00Z<",
				"<CreationTime>2026-03-20T10:00:01Z<", "<CreationTime>2099-03-20T10:00:01Z<")
		}
		base := key
		base.Path = filepath.Join(dir, fmt.Sprintf("основа %d.kdbx", i))
		base = base.Import(t, xml)
		cipher, kdf := encryption(i)
		copies[i] = base.WithEncryption(t, filepath.Join(dir, fmt.Sprintf("копія номер %d.kdbx", i)), cipher.id, kdf.kdf)
	}
	return copies
}

// matrixEntry is an entry only one copy has.
func matrixEntry(i int) string {
	uuid := fmt.Sprintf("AAAAAAAAAAAAAAAAAAAC%cA==", 'A'+i)
	return strings.NewReplacer("AAAAAAAAAAAAAAAAAAABCQ==", uuid, "Додане", fmt.Sprintf("Запис копії %d", i), addedSecret, matrixSecret(i)).Replace(addedEntry)
}

func matrixSecret(i int) string { return fmt.Sprintf("%s-копія-%d", secretMarker, i) }

// mergeLine gives the arguments and the lines of stdin for a merge of the copies.
func mergeLine(copies []testkit.DB, output string) (args []string, stdin string) {
	args = []string{"merge"}
	for _, c := range copies {
		args = append(args, c.Path)
		if c.KeyFile != "" {
			args = append(args, "--key-file", c.KeyFile)
		}
		// No password is an empty line: Enter tries the password typed before, then no password.
		stdin += c.Password + "\n"
	}
	return append(args, "--output", output), stdin
}

// wantEveryKindOfData checks with the real KeePassXC that a result holds all kinds of data the spec
// names, also in the versions behind the main ones.
func wantEveryKindOfData(t *testing.T, result testkit.DB) {
	t.Helper()
	export := strings.Join(result.ExportLines(t), "\n")
	for name, want := range map[string]string{
		"main password":    "SECRET-MARKER-поточний пароль",
		"older password":   "SECRET-MARKER-старий пароль",
		"TOTP":             "otpauth://totp/Example:user?secret=JBSWY3DPEHPK3PXP",
		"passkey key":      "<Key>KPEX_PASSKEY_PRIVATE_KEY_PEM</Key>",
		"passkey value":    "SECRET-MARKER-passkey",
		"custom field":     "<Key>Власне поле</Key>",
		"its value":        "власне значення",
		"empty field":      "<Key>EmptyField</Key>",
		"Auto-Type":        "{USERNAME}{TAB}{PASSWORD}",
		"Auto-Type window": "*Firefox*",
		"tag":              "бета",
		"foreground":       "#112233",
		"background":       "#445566",
		"custom icon":      "<CustomIconUUID>AAAAAAAAAAAAAAAAAAAJAQ==</CustomIconUUID>",
		"browser data":     "<Key>KPXC_BROWSER_SETTINGS</Key>",
		"attachment":       "<Key>file.bin</Key>",
		"older attachment": "<Key>old.bin</Key>",
		"database name":    "Синтетична база",
		"database data":    "meta-custom-value",
		"group data":       "group-value",
		"deletion record":  "AAAAAAAAAAAAAAAAAAAHdw==",
		"the older main":   ">SECRET-MARKER-друге<",
		"the newer main":   divergedSecret,
	} {
		if !strings.Contains(export, want) {
			t.Errorf("KeePassXC does not see the %s: %q", name, want)
		}
	}
	for entry, name := range map[string]string{richMailEntry: "file.bin", richWorkEntry: "same-content.bin"} {
		if got := result.AttachmentBytes(t, entry, name); string(got) != richFileBytes {
			t.Errorf("KeePassXC reads %q in %s of %s", got, name, entry)
		}
	}
}

// wantEncryption checks the cipher and the KDF of a file as the real KeePassXC reads them.
func wantEncryption(t *testing.T, db testkit.DB, cipher matrixCipher, kdf matrixKDF) {
	t.Helper()
	info := db.CLI(t, "db-info")
	// The labels follow the language of the system. The order of the lines does not: UUID, name,
	// description, cipher, KDF. A name stays the same in every language.
	lines := strings.Split(strings.ReplaceAll(info, "\r", ""), "\n")
	if len(lines) < 5 {
		t.Fatalf("db-info printed:\n%s", info)
	}
	if !strings.Contains(lines[3], cipher.name) {
		t.Errorf("KeePassXC says the cipher is %q, want %s", lines[3], cipher.name)
	}
	if !strings.Contains(lines[4], kdf.name) {
		t.Errorf("KeePassXC says the KDF is %q, want %s", lines[4], kdf.name)
	}
}

func TestMatrixOfCiphersKDFsAndKeys(t *testing.T) {
	t.Parallel()
	keyKinds := []struct {
		name string
		keys matrixKeys
	}{
		{"a password", onePassword},
		{"a password and a key file", passwordAndKeyFile},
		{"different passwords", differentPasswords},
	}
	// Each cipher meets each KDF, and each of these two meets each kind of keys.
	for i, cipher := range matrixCiphers {
		for j, kdf := range matrixKDFs {
			kind := keyKinds[(i+j)%len(keyKinds)]
			t.Run(fmt.Sprintf("%s, %s, %s", cipher.name, kdf.name, kind.name), func(t *testing.T) {
				t.Parallel()
				// The older copy has another cipher and another KDF, so only the newest one can give
				// the result those it has.
				checkMatrixCase(t, kind.keys, 2, func(at int) (matrixCipher, matrixKDF) {
					if at == 1 {
						return cipher, kdf
					}
					return matrixCiphers[(i+1)%len(matrixCiphers)], matrixKDFs[(j+1)%len(matrixKDFs)]
				})
			})
		}
	}
}

func TestMatrixOfKeysAndCopies(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		keys   matrixKeys
		copies int
	}{
		"a key file and no password":           {onlyKeyFile, 2},
		"different passwords and key files":    {differentPasswordsAndKeyFiles, 2},
		"three copies with different keys":     {differentPasswordsAndKeyFiles, 3},
		"five copies with one password":        {onePassword, 5},
		"five copies with different passwords": {differentPasswords, 5},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkMatrixCase(t, tc.keys, tc.copies, func(at int) (matrixCipher, matrixKDF) {
				return matrixCiphers[at%len(matrixCiphers)], matrixKDFs[at%len(matrixKDFs)]
			})
		})
	}
}

// checkMatrixCase merges copies of one database and checks the result with the real KeePassXC.
func checkMatrixCase(t *testing.T, keys matrixKeys, count int, encryption func(at int) (matrixCipher, matrixKDF)) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "папка з пробілом")
	copies := matrixCopies(t, dir, keys(t, dir, count), encryption)
	digests := make([][32]byte, count)
	for i := range copies {
		digests[i] = digestOf(t, copies[i].Path)
	}
	newest := copies[count-1]
	result := testkit.DB{Path: filepath.Join(dir, "нова база.kdbx"), Password: newest.Password, KeyFile: newest.KeyFile}

	args, stdin := mergeLine(copies, result.Path)
	got := testkit.Run(t, stdin, args...)

	wantJudged(t, got)
	wantEveryKindOfData(t, result)
	keptAllLinesButTheResavedGroup(t, result, copies...)
	for i := range copies {
		if digestOf(t, copies[i].Path) != digests[i] {
			t.Errorf("copy %d changed", i)
		}
		if export := strings.Join(result.ExportLines(t), "\n"); !strings.Contains(export, matrixSecret(i)) {
			t.Errorf("KeePassXC does not see the entry of copy %d", i)
		}
	}
	cipher, kdf := encryption(count - 1)
	wantEncryption(t, result, cipher, kdf)
	wantNoTempFiles(t, dir)
}

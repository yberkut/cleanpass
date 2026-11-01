package gateway_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tobischo/gokeepasslib/v3"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/testkit"
)

// The bytes of the attachments in the rich fixture.
const (
	richAttachment    = "synthetic attachment bytes \x00\x01\x02 end"
	richOldAttachment = "synthetic old attachment"
)

// write saves the snapshot under the rules of the output format and gives the file with
// the same password.
func write(t *testing.T, snap *snapshot.Snapshot, password string) testkit.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "written.kdbx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	creds := gateway.Credentials{Password: []byte(password)}
	if err := gateway.Write(file, snap, gateway.OutputFormat(snap.Format), creds); err != nil {
		t.Fatal(err)
	}
	return testkit.DB{Path: path, Password: password}
}

func wantSameLines(t *testing.T, got, want testkit.DB) {
	t.Helper()
	gotLines, wantLines := got.ExportLines(t), want.ExportLines(t)
	// The lines hold secrets, but only synthetic ones, and a failure must say which differ.
	for _, line := range missingFrom(gotLines, wantLines) {
		t.Errorf("the export lost the line %q", line)
	}
	for _, line := range missingFrom(wantLines, gotLines) {
		t.Errorf("the export gained the line %q", line)
	}
}

// missingFrom gives the lines of want that have and has not enough of. Both are sorted.
func missingFrom(has, want []string) []string {
	var missing []string
	rest := slices.Clone(has)
	for _, line := range want {
		if i := slices.Index(rest, line); i >= 0 {
			rest = slices.Delete(rest, i, i+1)
		} else {
			missing = append(missing, line)
		}
	}
	return missing
}

func TestKeePassXCSeesInTheWrittenFileAllThatItSawInTheCopy(t *testing.T) {
	snap, source := richSnapshot(t)
	wantSameLines(t, write(t, snap, source.Password), source)
}

func TestKeePassXCReadsTheBytesOfAttachments(t *testing.T) {
	snap, source := richSnapshot(t)
	written := write(t, snap, source.Password)

	entry := `/Пошта <&> "quoted" 😀`
	if got := written.AttachmentBytes(t, entry, "file.bin"); string(got) != richAttachment {
		t.Errorf("file.bin holds %q", got)
	}
	if got := written.AttachmentBytes(t, "/Робота/Друге", "same-content.bin"); string(got) != richAttachment {
		t.Errorf("same-content.bin holds %q", got)
	}

	// keepassxc-cli reads attachments of main versions only. So it saves the file: it has read
	// the attachment of the older version and has written it out by itself.
	written.Resave(t)
	again, err := gateway.Open(written.Path, gateway.Credentials{Password: []byte(written.Password)})
	if err != nil {
		t.Fatal(err)
	}
	old := findEntry(t, &again.Root, "Пошта").History[0].Attachments[0]
	if old.Name != "old.bin" || string(again.Attachments[old.ID].Content) != richOldAttachment {
		t.Errorf("after a save by KeePassXC the older version has %q with %q", old.Name, again.Attachments[old.ID].Content)
	}
}

// findEntry finds the entry whose title starts with the prefix.
func findEntry(t *testing.T, root *snapshot.Group, titlePrefix string) *snapshot.Entry {
	t.Helper()
	var found *snapshot.Entry
	root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			for _, f := range g.Entries[i].Fields {
				if f.Key == "Title" && strings.HasPrefix(f.Value, titlePrefix) {
					found = &g.Entries[i]
				}
			}
		}
	})
	if found == nil {
		t.Fatalf("no entry %q", titlePrefix)
	}
	return found
}

// The bytes of these attachments are valid base64 text, and that is a trap: the library
// takes any content that decodes as base64 for the encoded form of KDBX 3.
func TestAttachmentsThatLookLikeBase64KeepTheirBytes(t *testing.T) {
	for name, content := range map[string]string{
		"abcd.txt":    "abcd",
		"padded.txt":  "dGVzdA==",
		"empty.bin":   "",
		"pem.txt":     strings.Repeat("QUJD", 200),
		"not-b64.bin": "\xff\xfe\x00 raw",
		// Larger than one block of the file format.
		"big.bin": strings.Repeat("\x00\x01 some bytes that do not end in padding ", 80_000),
	} {
		t.Run(name, func(t *testing.T) {
			db := testkit.DB{Path: filepath.Join(t.TempDir(), "a.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
			db.AttachFile(t, "/Робота/Друге", name, []byte(content))

			snap, err := gateway.Open(db.Path, gateway.Credentials{Password: []byte(db.Password)})
			if err != nil {
				t.Fatal(err)
			}
			written := write(t, snap, db.Password)
			if got := written.AttachmentBytes(t, "/Робота/Друге", name); !bytes.Equal(got, []byte(content)) {
				t.Errorf("KeePassXC reads other bytes from the written file: %d of them, want %d", len(got), len(content))
			}
		})
	}
}

func TestTheSameBytesAreStoredOnce(t *testing.T) {
	xml := strings.Replace(testkit.PlainXML, "<Times>",
		`<Binary><Key>a.bin</Key><Value Ref="0"/></Binary><Binary><Key>b.bin</Key><Value Ref="1"/></Binary><Binary><Key>c.bin</Key><Value Ref="2"/></Binary><Times>`, 1)
	source := testkit.RawFile{Major: 4, Minor: 1, XML: xml, Attachments: [][]byte{[]byte("same"), []byte("same"), []byte("other")}}.
		Write(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.Password)
	if stored := source.AttachmentsInFile(t); stored != 3 {
		t.Fatalf("the source stores %d attachments, want 3", stored)
	}
	snap, err := gateway.Open(source.Path, gateway.Credentials{Password: []byte(source.Password)})
	if err != nil {
		t.Fatal(err)
	}

	written := write(t, snap, source.Password)
	if stored := written.AttachmentsInFile(t); stored != 2 {
		t.Errorf("the file stores %d attachments, want 2", stored)
	}
	for name, want := range map[string]string{"a.bin": "same", "b.bin": "same", "c.bin": "other"} {
		if got := written.AttachmentBytes(t, "/Запис", name); string(got) != want {
			t.Errorf("%s holds %q, want %q", name, got, want)
		}
	}
}

func TestAWriteWithoutTheBytesOfAnAttachmentFails(t *testing.T) {
	snap, source := richSnapshot(t)
	clear(snap.Attachments)
	err := gateway.Write(io.Discard, snap, gateway.OutputFormat(snap.Format), gateway.Credentials{Password: []byte(source.Password)})
	if err == nil {
		t.Error("the write dropped the attachments without an error")
	}
}

func wantAllDifferent(t *testing.T, name string, got, other []byte, size int) {
	t.Helper()
	if len(got) != size {
		t.Errorf("%s has %d bytes, want %d", name, len(got), size)
	}
	if bytes.Equal(got, other) {
		t.Errorf("%s did not change", name)
	}
}

func TestEveryWriteMakesNewRandomValues(t *testing.T) {
	snap, source := richSnapshot(t)
	in := source.Randoms(t)
	first, second := write(t, snap, source.Password).Randoms(t), write(t, snap, source.Password).Randoms(t)

	for _, other := range []testkit.Randoms{in, second} {
		wantAllDifferent(t, "MasterSeed", first.MasterSeed, other.MasterSeed, 32)
		wantAllDifferent(t, "KDF salt", first.KDFSalt, other.KDFSalt, 32)
		wantAllDifferent(t, "EncryptionIV", first.EncryptionIV, other.EncryptionIV, 16)
		wantAllDifferent(t, "inner stream key", first.InnerStreamKey, other.InnerStreamKey, 64)
	}
}

func TestWriteKeepsTheCipherAndTheKDF(t *testing.T) {
	dir := t.TempDir()
	base := testkit.DB{Path: filepath.Join(dir, "base.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	cases := []struct {
		name   string
		cipher testkit.Cipher
		kdf    testkit.KDF
	}{
		{"AES and AES-KDF", testkit.AES256, testkit.KDFAES},
		{"ChaCha20 and Argon2d", testkit.ChaCha20, testkit.KDFArgon2d},
		{"Twofish and Argon2id", testkit.Twofish, testkit.KDFArgon2id},
		{"AES and Argon2id", testkit.AES256, testkit.KDFArgon2id},
		{"ChaCha20 and AES-KDF", testkit.ChaCha20, testkit.KDFAES},
		{"Twofish and Argon2d", testkit.Twofish, testkit.KDFArgon2d},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := base.WithEncryption(t, filepath.Join(t.TempDir(), "source.kdbx"), tc.cipher, tc.kdf)
			creds := gateway.Credentials{Password: []byte(source.Password)}
			snap, err := gateway.Open(source.Path, creds)
			if err != nil {
				t.Fatal(err)
			}

			written := write(t, snap, source.Password)
			wantSameLines(t, written, source)
			again, err := gateway.Open(written.Path, creds)
			if err != nil {
				t.Fatal(err)
			}
			if again.Format.Cipher != snap.Format.Cipher || again.Format.KDF != snap.Format.KDF {
				t.Errorf("the file has %+v, want the cipher and the KDF of %+v", again.Format, snap.Format)
			}
			if again.Format.Major != 4 {
				t.Errorf("the file is KDBX %d", again.Format.Major)
			}
		})
	}
}

func TestAKDBX3CopyBecomesKDBX4WithTheDefaultsOfKeePassXC(t *testing.T) {
	source := testkit.NewKDBX3(t, filepath.Join(t.TempDir(), "k3.kdbx"), testkit.Password)
	creds := gateway.Credentials{Password: []byte(source.Password)}
	snap, err := gateway.Open(source.Path, creds)
	if err != nil {
		t.Fatal(err)
	}

	written := write(t, snap, source.Password)
	again, err := gateway.Open(written.Path, creds)
	if err != nil {
		t.Fatal(err)
	}
	want := snapshot.Format{
		Major: 4, Minor: 1, Cipher: snapshot.CipherAES256,
		KDF: snapshot.KDF{Kind: snapshot.KDFArgon2id, MemoryBytes: 64 * 1024 * 1024, Iterations: 30, Parallelism: 2},
	}
	if again.Format != want {
		t.Errorf("the file has %+v, want %+v", again.Format, want)
	}
	if got := written.AttachmentBytes(t, "/kdbx3", "kdbx3.bin"); string(got) != "synthetic kdbx3 attachment" {
		t.Errorf("the attachment holds %q", got)
	}
	export := strings.Join(written.ExportLines(t), "\n")
	for _, secret := range []string{"SECRET-MARKER-new", "SECRET-MARKER-old"} {
		if !strings.Contains(export, secret) {
			t.Errorf("KeePassXC does not see %s", secret)
		}
	}
}

func TestEveryPartOfASnapshotComesBackFromTheFile(t *testing.T) {
	want := testkit.FullSnapshot()
	written := write(t, want, testkit.Password)

	got, err := gateway.Open(written.Path, gateway.Credentials{Password: []byte(written.Password)})
	if err != nil {
		t.Fatal(err)
	}
	if place := snapshot.Difference(want, got); place != "" {
		t.Errorf("the file holds something else at %s", place)
	}
	if len(got.FieldLosses) > 0 {
		t.Errorf("the guard finds losses in the file it wrote itself: %v", got.FieldLosses)
	}
	if listing := written.CLI(t, "ls"); !strings.Contains(listing, testkit.FullSnapshotEntryTitle) {
		t.Errorf("keepassxc-cli does not see the entry:\n%s", listing)
	}
}

// A Snapshot must have a place for all the library reads, or a write would lose it with no
// word from the field guard: the guard looks at what the library keeps. So a new field after an
// update of the library copy fails here, until it has its place or this list a good reason.
func TestEveryFieldOfTheLibraryHasAPlaceInASnapshot(t *testing.T) {
	for _, tc := range []struct {
		value  any
		fields string
	}{
		{gokeepasslib.MetaData{}, "Generator SettingsChanged HeaderHash DatabaseName DatabaseNameChanged DatabaseDescription DatabaseDescriptionChanged DefaultUserName DefaultUserNameChanged MaintenanceHistoryDays Color MasterKeyChanged MasterKeyChangeRec MasterKeyChangeForce MemoryProtection CustomIcons RecycleBinEnabled RecycleBinUUID RecycleBinChanged EntryTemplatesGroup EntryTemplatesGroupChanged HistoryMaxItems HistoryMaxSize LastSelectedGroup LastTopVisibleGroup Binaries CustomData"},
		{gokeepasslib.MemProtection{}, "ProtectTitle ProtectUserName ProtectPassword ProtectURL ProtectNotes"},
		{gokeepasslib.CustomIcon{}, "UUID Data Name LastModificationTime"},
		{gokeepasslib.CustomData{}, "XMLName Key Value LastModificationTime"},
		{gokeepasslib.RootData{}, "Groups DeletedObjects"},
		{gokeepasslib.DeletedObjectData{}, "XMLName UUID DeletionTime"},
		{gokeepasslib.Group{}, "UUID Name Notes IconID CustomIconUUID Times IsExpanded DefaultAutoTypeSequence EnableAutoType EnableSearching LastTopVisibleEntry PreviousParentGroup Tags CustomData Entries Groups childOrder"},
		{gokeepasslib.Entry{}, "UUID IconID CustomIconUUID ForegroundColor BackgroundColor OverrideURL QualityCheck Tags PreviousParentGroup Times Values Binaries AutoType CustomData Histories childOrder"},
		{gokeepasslib.TimeData{}, "CreationTime LastModificationTime LastAccessTime ExpiryTime Expires UsageCount LocationChanged"},
		{gokeepasslib.AutoTypeData{}, "Enabled DataTransferObfuscation DefaultSequence Associations"},
		{gokeepasslib.AutoTypeAssociation{}, "Window KeystrokeSequence"},
		{gokeepasslib.ValueData{}, "Key Value"},
		{gokeepasslib.V{}, "Content Protected"},
		{gokeepasslib.BinaryReference{}, "Name Value"},
		{gokeepasslib.FileHeaders{}, "Comment CipherID CompressionFlags MasterSeed TransformSeed TransformRounds EncryptionIV ProtectedStreamKey StreamStartBytes InnerRandomStreamID KdfParameters PublicCustomData"},
		{gokeepasslib.InnerHeader{}, "InnerRandomStreamID InnerRandomStreamKey Binaries"},
	} {
		kind := reflect.TypeOf(tc.value)
		var names []string
		for i := range kind.NumField() {
			names = append(names, kind.Field(i).Name)
		}
		if got := strings.Join(names, " "); got != tc.fields {
			t.Errorf("%s has the fields\n%s\nwant\n%s", kind.Name(), got, tc.fields)
		}
	}
}

func TestWriteWorksWithEveryKindOfKey(t *testing.T) {
	dir := t.TempDir()
	keyFile := testkit.NewKeyFile(t, filepath.Join(dir, "key.bin"), 7)
	snap, _ := richSnapshot(t)
	for _, tc := range []struct {
		name  string
		db    testkit.DB
		creds gateway.Credentials
	}{
		{"password", testkit.DB{Password: "пароль"}, gateway.Credentials{Password: []byte("пароль")}},
		{"key file", testkit.DB{KeyFile: keyFile}, gateway.Credentials{KeyFile: keyFile}},
		{"both", testkit.DB{Password: "пароль", KeyFile: keyFile}, gateway.Credentials{Password: []byte("пароль"), KeyFile: keyFile}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "written.kdbx")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := gateway.Write(file, snap, gateway.OutputFormat(snap.Format), tc.creds); err != nil {
				t.Fatal(err)
			}
			tc.db.Path = path
			if listing := tc.db.CLI(t, "ls"); !strings.Contains(listing, "Робота") {
				t.Errorf("keepassxc-cli does not see the groups:\n%s", listing)
			}
		})
	}
}

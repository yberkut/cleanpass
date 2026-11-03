package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

const fieldLossWarning = "Попередження"

func TestCompareNamesFieldsTheToolCannotKeep(t *testing.T) {
	t.Parallel()
	db := testkit.UnknownFields(t, filepath.Join(t.TempDir(), "unknown.kdbx"))

	got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
	if got.Code != 0 {
		t.Fatalf("exit code %d, a warning is not an error\nstderr:\n%s", got.Code, got.Stderr)
	}
	block := passports(t, got.Stdout, db.Path)[db.Path]
	if !strings.Contains(block, fieldLossWarning) {
		t.Errorf("no warning:\n%s", block)
	}
	for _, path := range testkit.UnknownFieldPaths {
		if !strings.Contains(block, path) {
			t.Errorf("the warning does not name %s:\n%s", path, block)
		}
	}
	if strings.Contains(got.Stdout+got.Stderr, testkit.UnknownFieldValue) {
		t.Errorf("the output holds a value of an unknown field:\n%s\n%s", got.Stdout, got.Stderr)
	}
	wantUkrainian(t, got.Stdout, got.Stderr, append([]string{db.Path, "Argon2id"}, testkit.UnknownFieldPaths...)...)
}

func wantNoFieldLossWarning(t *testing.T, got testkit.Result) {
	t.Helper()
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
	}
	if strings.Contains(got.Stdout, fieldLossWarning) {
		t.Errorf("false alarm:\n%s", got.Stdout)
	}
}

// The test writer stands behind many cases below, so the real KeePassXC judges it first.
func TestKeePassXCOpensTheFilesOfTheTestWriter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, file := range map[string]testkit.RawFile{
		"kdbx3.kdbx":             {Major: 3, Minor: 1, XML: testkit.PlainXML},
		"kdbx4.kdbx":             {Major: 4, Minor: 1, XML: testkit.PlainXML},
		"kdbx3-no-gzip.kdbx":     {Major: 3, Minor: 1, XML: testkit.PlainXML, NoCompression: true},
		"kdbx4-no-gzip.kdbx":     {Major: 4, Minor: 1, XML: testkit.PlainXML, NoCompression: true},
		"kdbx4-attachments.kdbx": {Major: 4, Minor: 0, XML: testkit.PlainXML, Attachments: [][]byte{[]byte("bytes")}},
	} {
		t.Run(name, func(t *testing.T) {
			db := file.Write(t, filepath.Join(dir, name), testkit.Password)
			if listing := db.CLI(t, "ls"); !strings.Contains(listing, "Запис") {
				t.Errorf("keepassxc-cli does not see the entry:\n%s", listing)
			}
		})
	}
}

func TestCompareRaisesNoFalseAlarmOnKeePassXCFiles(t *testing.T) {
	t.Parallel()
	// The values a library is most likely to leave out: zeros, False, null and empty text.
	sparse := testkit.RichXML()
	for _, change := range [][2]string{
		{"<MaintenanceHistoryDays>365<", "<MaintenanceHistoryDays>0<"},
		{"<Color>#FF0000<", "<Color><"},
		{"<MasterKeyChangeRec>90<", "<MasterKeyChangeRec>0<"},
		{"<MasterKeyChangeForce>-1<", "<MasterKeyChangeForce>0<"},
		{"<ProtectPassword>True<", "<ProtectPassword>False<"},
		{"<RecycleBinEnabled>True<", "<RecycleBinEnabled>False<"},
		{"<HistoryMaxItems>25<", "<HistoryMaxItems>0<"},
		{"<HistoryMaxSize>7340032<", "<HistoryMaxSize>0<"},
		{"<IconID>48<", "<IconID>0<"},
		{"<IconID>5<", "<IconID>0<"},
		{"<UsageCount>7<", "<UsageCount>0<"},
		{"<Expires>True<", "<Expires>False<"},
		{"<DataTransferObfuscation>1<", "<DataTransferObfuscation>0<"},
		{"<Enabled>True<", "<Enabled>False<"},
		{"<EnableAutoType>true<", "<EnableAutoType>null<"},
		{"<IsExpanded>True<", "<IsExpanded>False<"},
	} {
		if !strings.Contains(sparse, change[0]) {
			t.Fatalf("no %s in the fixture", change[0])
		}
		sparse = strings.ReplaceAll(sparse, change[0], change[1])
	}

	for name, xml := range map[string]string{"rich": testkit.RichXML(), "sparse": sparse} {
		t.Run(name, func(t *testing.T) {
			db := testkit.DB{Path: filepath.Join(t.TempDir(), "a.kdbx"), Password: testkit.Password}.Import(t, xml)
			wantNoFieldLossWarning(t, testkit.Run(t, db.Password+"\n", "compare", db.Path))

			db.Resave(t)
			wantNoFieldLossWarning(t, testkit.Run(t, db.Password+"\n", "compare", db.Path))
		})
	}
}

// KDBX 4 keeps attachments in front of the XML. An attached KeePass export must not be
// taken for the document, whole or cut off.
func TestCompareIsNotFooledByAnAttachmentThatLooksLikeTheDocument(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	for name, content := range map[string]string{
		"whole.xml":   "<KeePassFile><Root><FutureInAttachment>x</FutureInAttachment></Root></KeePassFile>",
		"cut-off.xml": "<KeePassFile><Root><FutureInAttachment>x",
	} {
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		db.CLI(t, "attachment-import", "/Робота/Друге", name, file)
	}

	got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
	wantNoFieldLossWarning(t, got)
	wantField(t, passports(t, got.Stdout, db.Path)[db.Path], "Вкладень", "4")
}

// Without compression the padding of the cipher follows the XML.
func TestCompareReadsAFileWithoutCompression(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := testkit.DB{Path: filepath.Join(dir, "base.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	for name, db := range map[string]testkit.DB{
		"KeePassXC":   base.WithoutCompression(t, filepath.Join(dir, "resaved.kdbx")),
		"test writer": testkit.RawFile{Major: 4, Minor: 1, XML: testkit.PlainXML, NoCompression: true}.Write(t, filepath.Join(dir, "raw4.kdbx"), testkit.Password),
		"KDBX 3":      testkit.RawFile{Major: 3, Minor: 1, XML: testkit.PlainXML, NoCompression: true}.Write(t, filepath.Join(dir, "raw3.kdbx"), testkit.Password),
	} {
		t.Run(name, func(t *testing.T) {
			wantNoFieldLossWarning(t, testkit.Run(t, db.Password+"\n", "compare", db.Path))
		})
	}
}

// fieldLossCases change PlainXML. Each one puts data where the library would lose it.
var fieldLossCases = []struct {
	name, from, to, wantLine string
}{
	{"element in Meta", "</Meta>", "<Future>x</Future></Meta>", "KeePassFile/Meta/Future (у файлі: 1, лишиться: 0)"},
	{"element in Root", "</Root>", "<Future>x</Future></Root>", "KeePassFile/Root/Future (у файлі: 1, лишиться: 0)"},
	{"element in a group", "<IsExpanded>", "<Future>x</Future><IsExpanded>", "KeePassFile/Root/Group/Future (у файлі: 1, лишиться: 0)"},
	{"element in an entry", "<Times>", "<Future>x</Future><Times>", "KeePassFile/Root/Group/Entry/Future (у файлі: 1, лишиться: 0)"},
	{"element beside Meta and Root", "<Root>", "<Future>x</Future><Root>", "KeePassFile/Future (у файлі: 1, лишиться: 0)"},
	{"element in an older version", "<Times>", "<History><Entry><Future>x</Future></Entry></History><Times>", "KeePassFile/Root/Group/Entry/History/Entry/Future (у файлі: 1, лишиться: 0)"},
	{"data deep inside an unknown element", "</Meta>", "<Future><Deep><Deeper>x</Deeper></Deep></Future></Meta>", "KeePassFile/Meta/Future/Deep/Deeper (у файлі: 1, лишиться: 0)"},
	{"attribute of a known element", "<Entry>", `<Entry Future="x">`, "KeePassFile/Root/Group/Entry@Future (у файлі: 1, лишиться: 0)"},
	{"attribute of an unknown element", "</Meta>", `<Future Inner="x"/></Meta>`, "KeePassFile/Meta/Future@Inner (у файлі: 1, лишиться: 0)"},
	{"text in a known container", "<Root>", "<Root>x", "KeePassFile/Root (у файлі: 1, лишиться: 0)"},
	{"known element twice", "<Name>Root</Name>", "<Name>Root</Name><Name>Second</Name>", "KeePassFile/Root/Group/Name (у файлі: 2, лишиться: 1)"},
}

func TestCompareCatchesLossInEveryPlaceAndFormat(t *testing.T) {
	t.Parallel()
	for _, format := range []struct {
		name         string
		major, minor uint16
	}{{"KDBX 3.1", 3, 1}, {"KDBX 4.1", 4, 1}} {
		for _, tc := range fieldLossCases {
			t.Run(format.name+" "+tc.name, func(t *testing.T) {
				xml := strings.Replace(testkit.PlainXML, tc.from, tc.to, 1)
				db := testkit.RawFile{Major: format.major, Minor: format.minor, XML: xml}.Write(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.Password)

				got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
				if got.Code != 0 {
					t.Fatalf("exit code %d\nstderr:\n%s", got.Code, got.Stderr)
				}
				if !strings.Contains(got.Stdout, fieldLossWarning) || !strings.Contains(got.Stdout, tc.wantLine) {
					t.Errorf("no warning with %q:\n%s", tc.wantLine, got.Stdout)
				}
			})
		}
	}
}

// An entry refers to attachment 0 and an older version of it to attachment 1.
var attachmentRefs = strings.NewReplacer(
	"<Times>", `<Binary><Key>a.bin</Key><Value Ref="0"/></Binary><History><Entry><Binary><Key>b.bin</Key><Value Ref="1"/></Binary></Entry></History><Times>`,
).Replace(testkit.PlainXML)

// On every save the library drops an attachment that no entry refers to. The XML of KDBX 4
// never shows attachments, so only a look at the references can see this.
func TestCompareNamesAttachmentsASaveWouldDrop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	metaBinaries := func(ids ...string) string {
		var binaries strings.Builder
		for _, id := range ids {
			binaries.WriteString(`<Binary ID="` + id + `" Compressed="False">Ynl0ZXM=</Binary>`)
		}
		return strings.Replace(attachmentRefs, "</Meta>", "<Binaries>"+binaries.String()+"</Binaries></Meta>", 1)
	}
	bytesOf := func(contents ...string) [][]byte {
		var result [][]byte
		for _, content := range contents {
			result = append(result, []byte(content))
		}
		return result
	}

	for _, tc := range []struct {
		name string
		file testkit.RawFile
		// want is empty when a save keeps every attachment.
		want string
	}{
		{
			"KDBX 4 with one unused",
			testkit.RawFile{Major: 4, Minor: 1, XML: attachmentRefs, Attachments: bytesOf("a", "b", "unused")},
			"InnerHeader/Binary (у файлі: 3, лишиться: 2)",
		},
		{
			"KDBX 3 with one unused",
			testkit.RawFile{Major: 3, Minor: 1, XML: metaBinaries("0", "1", "2")},
			"KeePassFile/Meta/Binaries/Binary (у файлі: 3, лишиться: 2)",
		},
		{
			"KDBX 4 where an older version is the only user",
			testkit.RawFile{Major: 4, Minor: 1, XML: attachmentRefs, Attachments: bytesOf("a", "b")},
			"",
		},
		{
			"KDBX 3 where an older version is the only user",
			testkit.RawFile{Major: 3, Minor: 1, XML: metaBinaries("0", "1")},
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := tc.file.Write(t, filepath.Join(dir, tc.name+".kdbx"), testkit.Password)
			got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
			if tc.want == "" {
				wantNoFieldLossWarning(t, got)
				return
			}
			if got.Code != 0 {
				t.Fatalf("exit code %d\nstderr:\n%s", got.Code, got.Stderr)
			}
			if !strings.Contains(got.Stdout, fieldLossWarning) || !strings.Contains(got.Stdout, tc.want) {
				t.Errorf("no warning with %q:\n%s", tc.want, got.Stdout)
			}
		})
	}
}

// The stop has no bypass, so docs/acceptance.md tells what clears it: KeePassXC drops such an
// attachment on a save. This is real KeePassXC behaviour, and the advice stands on it.
func TestASaveInKeePassXCClearsTheStopOnAnAttachmentNoEntryRefersTo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.RawFile{Major: 4, Minor: 1, XML: attachmentRefs, Attachments: [][]byte{[]byte("a"), []byte("b"), []byte("unused")}}.Write(t, filepath.Join(dir, "a.kdbx"), testkit.Password)
	output := filepath.Join(dir, "new.kdbx")

	wantStopped(t, testkit.Run(t, passwordLines(1), "merge", db.Path, "--output", output), output)

	saveAgain(t, db)

	wantJudged(t, testkit.Run(t, passwordLines(1), "merge", db.Path, "--output", output))
}

// The guard looks only at elements and attributes with data, by name and count.
func TestCompareIsSilentAboutWhatHoldsNoData(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, from, to string }{
		{"empty element", "</Meta>", "<Future/></Meta>"},
		{"element with white space only", "</Meta>", "<Future>\n\t\t</Future></Meta>"},
		{"attribute without a value", "<Entry>", `<Entry Future="">`},
		{"comment", "</Meta>", "<!-- x --></Meta>"},
		{"namespace declaration", "<KeePassFile>", `<KeePassFile xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`},
		{"a value spelled in another way", "<IsExpanded>True<", "<IsExpanded>yes<"},
		{"empty list the library leaves out", "<Name>Root</Name>", "<Name>Root</Name><Tags></Tags>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xml := strings.Replace(testkit.PlainXML, tc.from, tc.to, 1)
			db := testkit.RawFile{Major: 4, Minor: 1, XML: xml}.Write(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.Password)
			wantNoFieldLossWarning(t, testkit.Run(t, db.Password+"\n", "compare", db.Path))
		})
	}
}

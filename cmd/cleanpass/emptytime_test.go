package main_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

func wantEmptyTimeAdvice(t *testing.T, got testkit.Result, path string) {
	t.Helper()
	if got.Code != 3 {
		t.Errorf("exit code %d, want 3\nstdout:\n%s", got.Code, got.Stdout)
	}
	for _, want := range []string{path, "порожнє поле часу", "Відкрийте копію в KeePassXC і збережіть"} {
		if !strings.Contains(got.Stderr, want) {
			t.Errorf("stderr has no %q:\n%s", want, got.Stderr)
		}
	}
	if strings.Contains(got.Stderr, "Неправильний") || strings.Contains(got.Stderr, "пошкоджений") {
		t.Errorf("a wrong reason:\n%s", got.Stderr)
	}
	wantUkrainian(t, got.Stdout, got.Stderr, path)
}

// The real case: kdbxweb leaves <MasterKeyChanged/> empty.
func TestCompareExplainsAnEmptyTimeElement(t *testing.T) {
	t.Parallel()
	db := testkit.EmptyTime(t, filepath.Join(t.TempDir(), "kdbxweb.kdbx"))
	wantEmptyTimeAdvice(t, testkit.Run(t, db.Password+"\n", "compare", db.Path), db.Path)
}

// The message gives advice, so the advice has to work.
func TestTheAdviceAboutAnEmptyTimeElementWorks(t *testing.T) {
	t.Parallel()
	db := testkit.EmptyTime(t, filepath.Join(t.TempDir(), "kdbxweb.kdbx")).Resave(t)

	got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstderr:\n%s", got.Code, got.Stderr)
	}
	wantField(t, passports(t, got.Stdout, db.Path)[db.Path], "Формат", "KDBX 4.0")
}

func TestCompareExplainsAnEmptyTimeElementAnywhere(t *testing.T) {
	t.Parallel()
	const beforeMetaEnd, inTimes = "</Meta>", "<Times>"
	for _, format := range []struct {
		name         string
		major, minor uint16
	}{{"KDBX 3.1", 3, 1}, {"KDBX 4.1", 4, 1}} {
		for _, tc := range []struct{ name, from, to string }{
			{"SettingsChanged", beforeMetaEnd, "<SettingsChanged/>" + beforeMetaEnd},
			{"DatabaseNameChanged", beforeMetaEnd, "<DatabaseNameChanged/>" + beforeMetaEnd},
			{"DatabaseDescriptionChanged", beforeMetaEnd, "<DatabaseDescriptionChanged/>" + beforeMetaEnd},
			{"DefaultUserNameChanged", beforeMetaEnd, "<DefaultUserNameChanged/>" + beforeMetaEnd},
			{"MasterKeyChanged", "<MasterKeyChanged>2026-02-03T10:00:00Z</MasterKeyChanged>", "<MasterKeyChanged></MasterKeyChanged>"},
			{"RecycleBinChanged", beforeMetaEnd, "<RecycleBinChanged/>" + beforeMetaEnd},
			{"EntryTemplatesGroupChanged", beforeMetaEnd, "<EntryTemplatesGroupChanged/>" + beforeMetaEnd},
			{"CreationTime", inTimes, inTimes + "<CreationTime/>"},
			{"LastAccessTime", inTimes, inTimes + "<LastAccessTime/>"},
			{"ExpiryTime", inTimes, inTimes + "<ExpiryTime/>"},
			{"LocationChanged", inTimes, inTimes + "<LocationChanged/>"},
			{"LastModificationTime", "<LastModificationTime>2026-03-15T10:00:00Z</LastModificationTime>", "<LastModificationTime/>"},
			{"DeletionTime", "<DeletionTime>2026-02-10T10:00:00Z</DeletionTime>", "<DeletionTime/>"},
			{"time of a custom icon", beforeMetaEnd, "<CustomIcons><Icon><UUID>AAAAAAAAAAAAAAAAAAAJAQ==</UUID><Data>AA==</Data><LastModificationTime/></Icon></CustomIcons>" + beforeMetaEnd},
			{"time of custom data", beforeMetaEnd, "<CustomData><Item><Key>k</Key><Value>v</Value><LastModificationTime/></Item></CustomData>" + beforeMetaEnd},
			{"line break only", beforeMetaEnd, "<SettingsChanged>\n</SettingsChanged>" + beforeMetaEnd},
			{"a space only", beforeMetaEnd, "<SettingsChanged> </SettingsChanged>" + beforeMetaEnd},
		} {
			t.Run(format.name+" "+tc.name, func(t *testing.T) {
				if !strings.Contains(testkit.PlainXML, tc.from) {
					t.Fatalf("no %s in the fixture", tc.from)
				}
				xml := strings.Replace(testkit.PlainXML, tc.from, tc.to, 1)
				file := testkit.RawFile{Major: format.major, Minor: format.minor, XML: xml}
				db := file.Write(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.Password)
				wantEmptyTimeAdvice(t, testkit.Run(t, db.Password+"\n", "compare", db.Path), db.Path)
			})
		}
	}
}

// The advice helps only with an empty element. Other damage must not get it.
func TestCompareDoesNotCallOtherDamageAnEmptyTime(t *testing.T) {
	t.Parallel()
	const master = "<MasterKeyChanged>2026-02-03T10:00:00Z</MasterKeyChanged>"
	for _, tc := range []struct{ name, from, to string }{
		{"time that is no time", master, "<MasterKeyChanged>не час</MasterKeyChanged>"},
		{"time too short", master, "<MasterKeyChanged>AAAA</MasterKeyChanged>"},
		{"number that is no number", "<IsExpanded>", "<IconID>abc</IconID><IsExpanded>"},
		{"XML cut off", "</KeePassFile>", ""},
		{"another root element", "KeePassFile>", "OtherFile>"},
		{"empty element with a time-like name that is not a time", "<IsExpanded>", "<IconID>abc</IconID><FutureTime/><IsExpanded>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xml := strings.ReplaceAll(testkit.PlainXML, tc.from, tc.to)
			db := testkit.RawFile{Major: 4, Minor: 1, XML: xml}.Write(t, filepath.Join(t.TempDir(), "a.kdbx"), testkit.Password)

			got := testkit.Run(t, db.Password+"\n", "compare", db.Path)
			if got.Code != 3 {
				t.Errorf("exit code %d, want 3\nstdout:\n%s", got.Code, got.Stdout)
			}
			if want := "Не вдалося прочитати базу " + db.Path; !strings.Contains(got.Stderr, want) {
				t.Errorf("stderr has no %q:\n%s", want, got.Stderr)
			}
			if strings.Contains(got.Stderr, "порожнє поле часу") {
				t.Errorf("wrong advice:\n%s", got.Stderr)
			}
		})
	}
}

package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

const secretMarker = "SECRET-MARKER"

// markedXML gives the rich fixture with a marker in every kind of value the report must not show,
// beside the markers it already holds in passwords and passkeys.
func markedXML(t *testing.T, password string) string {
	t.Helper()
	xml := testkit.RichXML()
	for _, change := range [][2]string{
		{"SECRET-MARKER-друге", password},
		{"secret=JBSWY3DPEHPK3PXP", "secret=" + secretMarker + "-totp"},
		{"<Value>власне значення<", "<Value>" + secretMarker + "-власне поле<"},
		{"<String><Key>EmptyField</Key><Value></Value></String>", `<String><Key>Код</Key><Value ProtectInMemory="True">` + secretMarker + `-захищене поле</Value></String>`},
		{"<Value>рядок 1", "<Value>" + secretMarker + "-нотатки"},
		{"{PASSWORD}{TAB}", secretMarker + "-послідовність"},
		{"{USERNAME}{TAB}{PASSWORD}", secretMarker + "-вікно"},
		{`{"Allow":["example.com"]}`, secretMarker + "-розширення"},
		{"meta-custom-value", secretMarker + "-дані бази"},
		{"group-value", secretMarker + "-дані групи"},
		{"Кореневі нотатки", secretMarker + "-нотатки групи"},
		{"Опис бази", secretMarker + "-опис бази"},
		{"<Value>https://b.example<", "<Value>https://u2:" + secretMarker + "-адреса@b.example/?token=" + secretMarker + "-токен<"},
		{"<Tags>alpha;бета;gamma<", "<Tags>" + secretMarker + "-тег<"},
		{"cmd://calc", "cmd://" + secretMarker + "-команда"},
	} {
		if !strings.Contains(xml, change[0]) {
			t.Fatalf("no %s in the fixture", change[0])
		}
		xml = strings.Replace(xml, change[0], change[1], 1)
	}
	return xml
}

func wantNoSecret(t *testing.T, got testkit.Result) {
	t.Helper()
	for name, output := range map[string]string{"stdout": got.Stdout, "stderr": got.Stderr} {
		if strings.Contains(output, secretMarker) {
			t.Errorf("%s holds a secret:\n%s", name, output)
		}
	}
}

// One real run shows every section of the report, on copies that really hold the markers.
func TestCompareKeepsEverySecretOutOfTheWholeReport(t *testing.T) {
	master := secretMarker + "-майстер-пароль"
	dir := t.TempDir()
	base := testkit.DB{Path: filepath.Join(dir, "base.kdbx"), Password: master}.Import(t, markedXML(t, secretMarker+"-друге"))

	// The marked copy really holds its markers.
	for _, attribute := range []string{"Password", "Код", "Власне поле", "Notes"} {
		if value := base.CLI(t, "show", "-s", "-a", attribute, mailEntry); !strings.Contains(value, secretMarker) {
			t.Fatalf("the fixture has no marker in %s: %q", attribute, value)
		}
	}

	// A copy that lags: an edit of the real KeePassXC with a marker in the notes.
	ahead := copyOf(t, base, "ahead.kdbx")
	ahead.CLI(t, "edit", "--notes", secretMarker+"-нова нотатка", mailEntry)

	// A conflict about one password, a new entry and a pair under a new UUID. The second copy is an
	// import with another password in the same entry.
	other := testkit.DB{Path: filepath.Join(dir, "other.kdbx"), Password: master}.Import(t,
		strings.Replace(markedXML(t, secretMarker+"-інший пароль"), "2026-03-01T10:00:03Z", "2026-03-20T10:00:03Z", 1))
	other.CLI(t, "add", "--username", "u4", "/Новий запис")

	// A deletion: the real KeePassXC moves an entry to the bin of a third copy.
	deleted := copyOf(t, base, "deleted.kdbx")
	deleted.CLI(t, "rm", workEntry)

	// A wrong password first: the marker in it must stay out as well. An empty line answers the
	// question about a key file after it, and each other line repeats the password.
	stdin := "неправильний-" + secretMarker + "\n" + master + "\n" + strings.Repeat("\n", 4)
	got := testkit.Run(t, stdin, "compare", base.Path, ahead.Path, other.Path, deleted.Path)

	wantCode(t, got, 0)
	wantNoSecret(t, got)
	for _, header := range []string{onlyInSomeCopies, deletionSection, versionsDispute} {
		section(t, got.Stdout, header)
	}
	wantLine(t, strings.ReplaceAll(strings.Join(section(t, got.Stdout, versionsDispute), "\n"), "; ", "\n"), "відрізняється від версії, що стане головною: пароль різний")
	if strings.Contains(got.Stdout, "u2:") {
		t.Errorf("the output holds the part of a URL before the host:\n%s", got.Stdout)
	}
}

// KeePassXC marks these fields as protected, so the Label of the entry must leave them empty.
func TestCompareShowsNoProtectedTitleOrLogin(t *testing.T) {
	xml := testkit.RichXML()
	for _, change := range [][2]string{
		{"<Value>Друге</Value>", `<Value ProtectInMemory="True">` + secretMarker + `-назва</Value>`},
		{"<Value>u2</Value>", `<Value ProtectInMemory="True">` + secretMarker + `-логін</Value>`},
		{"<Value>https://b.example</Value>", `<Value ProtectInMemory="True">https://` + secretMarker + `-сайт.example</Value>`},
	} {
		if !strings.Contains(xml, change[0]) {
			t.Fatalf("no %s in the fixture", change[0])
		}
		xml = strings.Replace(xml, change[0], change[1], 1)
	}
	dir := t.TempDir()
	a := newCopy(t, dir, "a.kdbx", xml)
	b := newCopy(t, dir, "b.kdbx", strings.Replace(xml, "2026-03-01T10:00:03Z", "2026-03-20T10:00:03Z", 1))

	got := testkit.Run(t, passwords(2), "compare", a.Path, b.Path)

	wantCode(t, got, 0)
	wantNoSecret(t, got)
	wantLines(t, section(t, got.Stdout, versionsDispute), "  (без назви, UUID 00000000000000000000000000000101)")
}

// Bytes of an attachment and its file name stay out too, and so does the content of a key file.
func TestCompareKeepsAttachmentsAndKeyFilesOutOfTheReport(t *testing.T) {
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "a.key"), 1)
	content, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password, KeyFile: key}.Import(t, testkit.RichXML())
	b := testkit.DB{Path: filepath.Join(dir, "b.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	file := filepath.Join(dir, "marked.bin")
	if err := os.WriteFile(file, []byte(secretMarker+"-вкладення"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.CLI(t, "attachment-import", workEntry, secretMarker+"-назва.bin", file)

	got := testkit.Run(t, passwords(2), "compare", a.Path, "--key-file", key, b.Path)

	wantCode(t, got, 0)
	wantNoSecret(t, got)
	wantLines(t, strings.Split(strings.ReplaceAll(strings.Join(section(t, got.Stdout, copyLags), "\n"), "; ", "\n"), "\n"),
		"відрізняється від версії, що стане головною: вкладення різні")
	if strings.Contains(got.Stdout+got.Stderr, string(content)) {
		t.Errorf("the output holds the content of the key file:\n%s\n%s", got.Stdout, got.Stderr)
	}
}

package judge_test

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/judge"
	"github.com/yberkut/cleanpass/internal/merge"
	"github.com/yberkut/cleanpass/internal/merge/mergetest"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/testkit"
)

// cheap is a format that makes a test run quickly.
var cheap = snapshot.Format{
	Major: 4, Minor: 1, Cipher: snapshot.CipherAES256,
	KDF: snapshot.KDF{Kind: snapshot.KDFAES, Rounds: 1000},
}

var keys = gateway.Credentials{Password: []byte(testkit.Password)}

// writtenFile saves the snapshot with the Gateway.
func writtenFile(t *testing.T, snap *snapshot.Snapshot) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "written.kdbx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := gateway.Write(file, snap, cheap, keys); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// written saves the snapshot and gives what the real keepassxc-cli prints.
func written(t *testing.T, snap *snapshot.Snapshot) []byte {
	t.Helper()
	return []byte(testkit.DB{Path: writtenFile(t, snap), Password: testkit.Password}.CLI(t, "export"))
}

// richSnapshot is the rich fixture, read by the Gateway from a file of the real KeePassXC.
func richSnapshot(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	db := testkit.DB{Path: filepath.Join(t.TempDir(), "rich.kdbx"), Password: testkit.Password}.Import(t, testkit.RichXML())
	snap, err := gateway.Open(db.Path, gateway.Credentials{Password: []byte(db.Password)})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func wantAgrees(t *testing.T, export []byte, want *snapshot.Snapshot) {
	t.Helper()
	if err := judge.CompareExport(export, want); err != nil {
		t.Errorf("KeePassXC should agree: %v", err)
	}
}

func TestKeePassXCAgreesWithAResultThatHoldsEveryPartOfASnapshot(t *testing.T) {
	want := testkit.FullSnapshot()
	wantAgrees(t, written(t, want), want)
}

func TestKeePassXCAgreesWithTheRichFixture(t *testing.T) {
	want := richSnapshot(t)
	wantAgrees(t, written(t, want), want)
}

// The real KeePassXC changes a file as it reads it, in ways that lose nothing. Each snapshot here
// holds one such thing, so the Judge must take it as agreement.
func TestKeePassXCAgreesWhereItReadsAFileInItsOwnWay(t *testing.T) {
	missing := id(0x77)
	zero := snapshot.UUID{}
	yes, no := true, false
	for name, change := range map[string]func(*snapshot.Snapshot){
		"a line break of Windows and of old systems in every text": func(s *snapshot.Snapshot) {
			s.Meta.DatabaseDescription = "one\r\ntwo\rthree\nfour\r\r\nfive"
			s.Root.Notes = "one\r\ntwo\rthree"
			s.Root.Entries[0].Fields = append(s.Root.Entries[0].Fields,
				snapshot.Field{Key: "Multi", Value: "secret\r\nwith\rbreaks", Protected: true},
				snapshot.Field{Key: "Notes", Value: "\r\nfirst\r\n\r\nlast\r\n"})
		},
		"spaces around a value": func(s *snapshot.Snapshot) {
			s.Root.Entries[0].Fields = append(s.Root.Entries[0].Fields, snapshot.Field{Key: "Custom", Value: "  value  "})
		},
		"a group the settings point at is not there": func(s *snapshot.Snapshot) {
			s.Meta.RecycleBin, s.Meta.EntryTemplatesGroup, s.Meta.LastSelectedGroup, s.Meta.LastTopVisibleGroup = missing, missing, missing, missing
		},
		"an entry a group points at is not there": func(s *snapshot.Snapshot) {
			s.Root.LastTopVisibleEntry = missing
		},
		"places and icons point at what is not there": func(s *snapshot.Snapshot) {
			s.Root.PreviousParentGroup = &missing
			s.Root.Entries[0].PreviousParentGroup = &missing
			s.Root.Entries[0].CustomIcon = missing
			s.Root.CustomIcon = missing
		},
		"an old place of nothing": func(s *snapshot.Snapshot) {
			s.Root.PreviousParentGroup = &zero
			s.Root.Entries[0].PreviousParentGroup = &zero
		},
		"tags in any order and with any separator": func(s *snapshot.Snapshot) {
			s.Root.Entries[0].Tags = "b\tc;a ,a"
			s.Root.Tags = "b;a, c"
		},
		"a quality check that is on": func(s *snapshot.Snapshot) {
			s.Root.Entries[0].QualityCheck = &yes
		},
		"a quality check that is off, with no other new feature of the format": func(s *snapshot.Snapshot) {
			s.Root.Entries[0].QualityCheck = &no
		},
		"tags on a group, with no other new feature of the format": func(s *snapshot.Snapshot) {
			s.Root.Tags = "a"
		},
		"a named icon, with no other new feature of the format": func(s *snapshot.Snapshot) {
			s.Meta.CustomIcons = []snapshot.CustomIcon{{UUID: id(9), Data: "iVBORw0KGgo=", Name: "icon"}}
		},
		"a time on an icon, with no other new feature of the format": func(s *snapshot.Snapshot) {
			s.Meta.CustomIcons = []snapshot.CustomIcon{{UUID: id(9), Data: "iVBORw0KGgo=", LastModification: moment}}
		},
		"an icon with no name and no time": func(s *snapshot.Snapshot) {
			s.Meta.CustomIcons = []snapshot.CustomIcon{{UUID: id(9), Data: "iVBORw0KGgo="}}
		},
		"an icon with line breaks in its base64": func(s *snapshot.Snapshot) {
			s.Meta.CustomIcons = []snapshot.CustomIcon{{UUID: id(9), Data: "iVBOR\nw0KGgo=\n"}}
		},
		"an icon that is not base64": func(s *snapshot.Snapshot) {
			s.Meta.CustomIcons = []snapshot.CustomIcon{
				{UUID: id(9), Data: "Значок з літер"},
				{UUID: id(10), Data: "a"},
				{UUID: id(11), Data: "ab"},
				{UUID: id(12), Data: "base"},
			}
		},
		"two attachments under one name with the same bytes": func(s *snapshot.Snapshot) {
			same := snapshot.NewAttachmentID([]byte("same"))
			s.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{same: {Content: []byte("same")}}
			s.Root.Entries[0].Attachments = []snapshot.AttachmentRef{{Name: "a", ID: same}, {Name: "a", ID: same}}
		},
		"a time on custom data of the database, with no other new feature of the format": func(s *snapshot.Snapshot) {
			s.Meta.CustomData = []snapshot.CustomDataItem{{Key: "with-time", Value: "v", LastModification: moment}}
		},
		"an old place of a group, with no other new feature of the format": func(s *snapshot.Snapshot) {
			s.Root.Groups = []snapshot.Group{{UUID: id(3), Name: "g", PreviousParentGroup: &missing}}
		},
		"an old place of a version, with no other new feature of the format": func(s *snapshot.Snapshot) {
			s.Root.Entries[0].History = []snapshot.EntryVersion{{PreviousParentGroup: &missing}}
		},
		"an icon number beside a custom icon": func(s *snapshot.Snapshot) {
			s.Root.Entries[0].IconID, s.Root.Entries[0].CustomIcon = 7, id(9)
			s.Root.IconID, s.Root.CustomIcon = 8, id(9)
		},
		"standard fields a version does not have": func(s *snapshot.Snapshot) {
			s.Root.Entries[0].Fields = nil
		},
		"a standard field marked as protected against the settings": func(s *snapshot.Snapshot) {
			s.Meta.MemoryProtection.Password = false
			s.Meta.MemoryProtection.Notes = true
			s.Root.Entries[0].Fields = []snapshot.Field{{Key: "Password", Value: "p"}, {Key: "Title", Value: "t", Protected: true}}
		},
		"times the file does not have": func(s *snapshot.Snapshot) {
			s.Root.Times = snapshot.Times{}
			s.Root.Entries[0].Times = snapshot.Times{}
			s.Meta.DatabaseNameChanged = time.Time{}
			s.Meta.CustomData = []snapshot.CustomDataItem{{Key: "no-time", Value: "v"}}
		},
		"a record of a deletion with the zero time": func(s *snapshot.Snapshot) {
			s.Deletions = []snapshot.Deletion{{UUID: id(0x40)}, {UUID: id(0x41), Time: moment}}
		},
		"custom data with times KeePassXC keeps only for the database": func(s *snapshot.Snapshot) {
			s.Root.CustomData = []snapshot.CustomDataItem{{Key: "z", Value: "1", LastModification: moment}, {Key: "a", Value: "2", LastModification: moment}}
			s.Root.Entries[0].CustomData = []snapshot.CustomDataItem{{Key: "z", Value: "1", LastModification: moment}, {Key: "a", Value: "2"}}
		},
		"attachments with many names and the same bytes": func(s *snapshot.Snapshot) {
			one, two := snapshot.NewAttachmentID([]byte("one")), snapshot.NewAttachmentID([]byte("two"))
			s.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{one: {Content: []byte("one")}, two: {Content: []byte("two")}}
			s.Root.Entries[0].Attachments = []snapshot.AttachmentRef{{Name: "z", ID: one}, {Name: "a", ID: two}, {Name: "-m", ID: one}}
		},
		"history in an order that is not by time": func(s *snapshot.Snapshot) {
			s.Root.Entries[0].History = []snapshot.EntryVersion{
				{Times: snapshot.Times{LastModification: moment.Add(-time.Hour)}},
				{Times: snapshot.Times{LastModification: moment.Add(-3 * time.Hour)}},
				{Times: snapshot.Times{LastModification: moment.Add(-2 * time.Hour)}},
			}
		},
		"groups and entries in an order that is not by name": func(s *snapshot.Snapshot) {
			s.Root.Groups = []snapshot.Group{{UUID: id(3), Name: "z"}, {UUID: id(4), Name: "a"}}
			s.Root.Entries = append(s.Root.Entries, snapshot.Entry{UUID: id(0x0F)})
		},
	} {
		t.Run(name, func(t *testing.T) {
			want := smallSnapshot()
			change(want)
			wantAgrees(t, written(t, want), want)
		})
	}
}

func id(last byte) snapshot.UUID {
	var uuid snapshot.UUID
	uuid[15] = last
	return uuid
}

var moment = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

func smallSnapshot() *snapshot.Snapshot {
	return &snapshot.Snapshot{
		Meta: snapshot.Meta{DatabaseName: "small", MemoryProtection: snapshot.MemoryProtection{Password: true}},
		Root: snapshot.Group{
			UUID: id(1),
			Name: "Root",
			Entries: []snapshot.Entry{{
				UUID:   id(0x10),
				Times:  snapshot.Times{LastModification: moment},
				Fields: []snapshot.Field{{Key: "Title", Value: "Запис"}, {Key: "Password", Value: "SECRET-MARKER-small", Protected: true}},
			}},
		},
	}
}

// What the Merge Engine really gives, on any scenario the generator makes. The number is
// fixed and small: each one runs the real keepassxc-cli.
func TestKeePassXCAgreesWithWhatTheMergeEngineGives(t *testing.T) {
	const scenarios = 40
	generator := mergetest.Scenarios()
	for seed := range scenarios {
		scenario := generator.Example(seed)
		result, err := merge.Run(merge.Input{Copies: scenario.Copies, Now: scenario.Now, Key: merge.NewKey()})
		if err != nil {
			t.Fatal(err)
		}
		if holdsTwoContentsUnderOneName(result.Merged) {
			continue
		}
		if err := judge.CompareExport(written(t, result.Merged), result.Merged); err != nil {
			t.Errorf("scenario %d: %v", seed, err)
		}
	}
}

// All the steps of the Judge, the bytes of attachments too. The generator gives many entries on
// one path, and attachments in old versions.
func TestTheWholeJudgeAgreesWithWhatTheMergeEngineGives(t *testing.T) {
	const scenarios = 25
	program, err := judge.Find()
	if err != nil {
		t.Fatal(err)
	}
	generator := mergetest.Scenarios()
	checked := 0
	for seed := range scenarios {
		scenario := generator.Example(seed)
		result, err := merge.Run(merge.Input{Copies: scenario.Copies, Now: scenario.Now, Key: merge.NewKey()})
		if err != nil {
			t.Fatal(err)
		}
		if holdsTwoContentsUnderOneName(result.Merged) {
			continue
		}
		checked++
		if err := program.Check(writtenFile(t, result.Merged), result.Merged, keys); err != nil {
			t.Errorf("scenario %d: %v", seed, err)
		}
	}
	if checked < scenarios/2 {
		t.Errorf("only %d of %d scenarios got a check", checked, scenarios)
	}
}

// holdsTwoContentsUnderOneName finds what the generator can make and KeePassXC cannot hold: it has
// one attachment for a name. The Judge is right to stop there, and the test below proves that.
func holdsTwoContentsUnderOneName(s *snapshot.Snapshot) bool {
	found := false
	inVersion := func(v *snapshot.EntryVersion) {
		seen := map[string]snapshot.AttachmentID{}
		for _, ref := range v.Attachments {
			if other, known := seen[ref.Name]; known && other != ref.ID {
				found = true
			}
			seen[ref.Name] = ref.ID
		}
	}
	s.Root.Walk(func(g *snapshot.Group) {
		for i := range g.Entries {
			inVersion(&g.Entries[i].EntryVersion)
			for j := range g.Entries[i].History {
				inVersion(&g.Entries[i].History[j])
			}
		}
	})
	return found
}

// Which of the two KeePassXC keeps differs from run to run, so each call can see the one it is asked
// about, and a check of the bytes alone would pass now and then. The export must settle this, every
// time, for a main version and for an older one.
func TestTheJudgeStopsAtTwoContentsUnderOneName(t *testing.T) {
	program, err := judge.Find()
	if err != nil {
		t.Fatal(err)
	}
	one, two := snapshot.NewAttachmentID([]byte("one")), snapshot.NewAttachmentID([]byte("two"))
	refs := []snapshot.AttachmentRef{{Name: "a", ID: one}, {Name: "a", ID: two}}
	for name, place := range map[string]func(*snapshot.Entry){
		"a main version":   func(e *snapshot.Entry) { e.Attachments = refs },
		"an older version": func(e *snapshot.Entry) { e.History = []snapshot.EntryVersion{{Attachments: refs}} },
	} {
		t.Run(name, func(t *testing.T) {
			want := smallSnapshot()
			want.Attachments = map[snapshot.AttachmentID]snapshot.Attachment{one: {Content: []byte("one")}, two: {Content: []byte("two")}}
			place(&want.Root.Entries[0])
			file := writtenFile(t, want)
			export := []byte(testkit.DB{Path: file, Password: testkit.Password}.CLI(t, "export"))

			var mismatch *judge.MismatchError
			if err := judge.CompareExport(export, want); !errors.As(err, &mismatch) {
				t.Errorf("the export gave %v, want a mismatch", err)
			}
			if err := program.Check(file, want, keys); !errors.As(err, &mismatch) {
				t.Errorf("the whole check gave %v, want a mismatch", err)
			}
		})
	}
}

// node is one piece of data in an export, with its place in the bytes.
type node struct {
	start, end int64
	text       string
	name       string
}

// serviceKeys renew themselves on each export or save, so these are not data of a result.
var serviceKeys = map[string]bool{"_LAST_MODIFIED": true, "KPXC_RANDOM_SLUG": true}

// textNodes finds every text in an export, apart from the service items. The decoder gives the
// place of each one, and the tests below change them one by one.
func textNodes(t *testing.T, export []byte) []node {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(export))
	var nodes []node
	var stack []string
	inService := false
	for {
		start := dec.InputOffset()
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nodes
		}
		if err != nil {
			t.Fatal(err)
		}
		switch tok := token.(type) {
		case xml.StartElement:
			stack = append(stack, tok.Name.Local)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			if tok.Name.Local == "Item" {
				inService = false
			}
		case xml.CharData:
			text := string(tok)
			if strings.TrimSpace(text) == "" {
				continue
			}
			name := stack[len(stack)-1]
			if name == "Key" && serviceKeys[text] {
				inService = true
			}
			if !inService {
				nodes = append(nodes, node{start: start, end: dec.InputOffset(), text: text, name: name})
			}
		}
	}
}

// changed gives another valid value of the same kind.
func changed(n node) string {
	switch n.text {
	case "True":
		return "False"
	case "False":
		return "True"
	case "true":
		return "false"
	case "false", "null":
		return "true"
	}
	if number, err := strconv.ParseInt(n.text, 10, 64); err == nil {
		return strconv.FormatInt(number+1, 10)
	}
	if decoded, err := base64.StdEncoding.DecodeString(n.text); err == nil && (len(decoded) == 8 || len(decoded) == 16) {
		decoded[0] ^= 1
		return base64.StdEncoding.EncodeToString(decoded)
	}
	return n.text + "x"
}

// Every piece of data KeePassXC prints must count. A change of any one of them must stop the
// work, and what stops it must not hold a value.
func TestEveryValueInTheExportCounts(t *testing.T) {
	for name, want := range map[string]*snapshot.Snapshot{"every part": testkit.FullSnapshot(), "rich": richSnapshot(t)} {
		t.Run(name, func(t *testing.T) {
			export := written(t, want)
			nodes := textNodes(t, export)
			if len(nodes) < 100 {
				t.Fatalf("only %d values found", len(nodes))
			}
			for _, n := range nodes {
				var other bytes.Buffer
				other.Write(export[:n.start])
				other.WriteString(changed(n))
				other.Write(export[n.end:])
				err := judge.CompareExport(other.Bytes(), want)
				if err == nil {
					t.Errorf("a change of the value in <%s> did not count", n.name)
					continue
				}
				if strings.Contains(err.Error(), "SECRET-MARKER") {
					t.Errorf("the error holds a value: %v", err)
				}
			}
		})
	}
}

// block is one whole element in an export, with its place in the bytes.
type block struct {
	start, end int64
	name       string
}

// blocks finds each whole element that holds data of a result, the root group excepted.
func blocks(t *testing.T, export []byte) []block {
	t.Helper()
	removable := map[string]bool{
		"Entry": true, "Group": true, "String": true, "Binary": true, "Icon": true,
		"DeletedObject": true, "Association": true, "Item": true, "Times": true, "MemoryProtection": true,
	}
	dec := xml.NewDecoder(bytes.NewReader(export))
	type open struct {
		name  string
		start int64
	}
	var stack []open
	var found []block
	groupDepth := 0
	serviceItem := false
	for {
		start := dec.InputOffset()
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return found
		}
		if err != nil {
			t.Fatal(err)
		}
		switch tok := token.(type) {
		case xml.StartElement:
			stack = append(stack, open{tok.Name.Local, start})
			if tok.Name.Local == "Item" {
				serviceItem = false
			}
			if tok.Name.Local == "Group" {
				groupDepth++
			}
		case xml.CharData:
			if serviceKeys[string(tok)] {
				serviceItem = true
			}
		case xml.EndElement:
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			isRoot := last.name == "Group" && groupDepth == 1
			if last.name == "Group" {
				groupDepth--
			}
			if removable[last.name] && !isRoot && !(last.name == "Item" && serviceItem) {
				found = append(found, block{last.start, dec.InputOffset(), last.name})
			}
		}
	}
}

func TestEveryWholePieceInTheExportCounts(t *testing.T) {
	for name, want := range map[string]*snapshot.Snapshot{"every part": testkit.FullSnapshot(), "rich": richSnapshot(t)} {
		t.Run(name, func(t *testing.T) {
			export := written(t, want)
			found := blocks(t, export)
			if len(found) < 20 {
				t.Fatalf("only %d pieces found", len(found))
			}
			for _, b := range found {
				var other bytes.Buffer
				other.Write(export[:b.start])
				other.Write(export[b.end:])
				if judge.CompareExport(other.Bytes(), want) == nil {
					t.Errorf("a loss of a whole <%s> did not count", b.name)
				}
			}
		})
	}
}

// The export sets these by itself at the moment it runs, so they differ from run to run.
func TestTheServiceValuesOfKeePassXCDoNotCount(t *testing.T) {
	want := testkit.FullSnapshot()
	export := written(t, want)
	lastModified := regexp.MustCompile(`(?s)(<Key>_LAST_MODIFIED</Key>\s*<Value>)[^<]*(</Value>)`)
	if !lastModified.Match(export) {
		t.Fatal("no _LAST_MODIFIED in the export")
	}
	for name, other := range map[string][]byte{
		"another text":      lastModified.ReplaceAll(export, []byte("${1}Пн січ 1 00:00:00 2001 GMT${2}")),
		"another language":  lastModified.ReplaceAll(export, []byte("${1}Mon Jan 1 00:00:00 2001 GMT${2}")),
		"none at all":       regexp.MustCompile(`(?s)<Item>\s*<Key>_LAST_MODIFIED</Key>.*?</Item>`).ReplaceAll(export, nil),
		"an empty value":    lastModified.ReplaceAll(export, []byte("${1}${2}")),
		"with a random one": bytes.Replace(export, []byte("</Meta>"), []byte("<CustomData><Item><Key>KPXC_RANDOM_SLUG</Key><Value>abc</Value></Item></CustomData></Meta>"), 1),
	} {
		t.Run(name, func(t *testing.T) {
			wantAgrees(t, other, want)
		})
	}
}

// What KeePassXC prints is added to, too. What the result should not hold must count as well.
func TestSomethingMoreInTheExportCounts(t *testing.T) {
	want := smallSnapshot()
	export := string(written(t, want))
	for name, addition := range map[string][2]string{
		"a field":          {"<AutoType>", "<String><Key>More</Key><Value>v</Value></String><AutoType>"},
		"an attachment":    {"<AutoType>", `<Binary><Key>more.bin</Key><Value Ref="0"/></Binary><AutoType>`},
		"a record":         {"</Root>", "<DeletedObjects><DeletedObject><UUID>AAAAAAAAAAAAAAAAAAAAQA==</UUID><DeletionTime>AAAAAAAAAAA=</DeletionTime></DeletedObject></DeletedObjects></Root>"},
		"a custom data":    {"</Meta>", "<CustomData><Item><Key>more</Key><Value>v</Value></Item></CustomData></Meta>"},
		"a version":        {"<History/>", "<History><Entry><UUID>AAAAAAAAAAAAAAAAAAAAEA==</UUID></Entry></History>"},
		"a group":          {"</Group>", "<Group><UUID>AAAAAAAAAAAAAAAAAAAAQA==</UUID></Group></Group>"},
		"another root":     {"</Root>", "<Group><UUID>AAAAAAAAAAAAAAAAAAAAQA==</UUID></Group></Root>"},
		"a version's UUID": {"<History/>", "<History><Entry><UUID>AAAAAAAAAAAAAAAAAAAAQA==</UUID></Entry></History>"},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(export, addition[0]) {
				t.Fatalf("no %s in the export", addition[0])
			}
			if judge.CompareExport([]byte(strings.Replace(export, addition[0], addition[1], 1)), want) == nil {
				t.Error("it did not count")
			}
		})
	}
}

var (
	creationTime = regexp.MustCompile(`<CreationTime>[^<]*</CreationTime>`)
	anyUUID      = regexp.MustCompile(`<UUID>[^<]*</UUID>`)
)

// A value of the export must not get out through an error of a parser. These texts hold a
// marker in the places a parser would quote.
func TestAnExportThatCannotBeReadGivesAnErrorWithoutAValue(t *testing.T) {
	const marker = "SECRET-MARKER-in-error"
	want := smallSnapshot()
	export := string(written(t, want))
	for name, broken := range map[string]string{
		"no XML":         marker,
		"an empty one":   "",
		"a broken tag":   "<KeePassFile><" + marker,
		"a bad number":   strings.Replace(export, "<IconID>0</IconID>", "<IconID>"+marker+"</IconID>", 1),
		"a bad time":     creationTime.ReplaceAllLiteralString(export, "<CreationTime>"+marker+"</CreationTime>"),
		"a bad UUID":     anyUUID.ReplaceAllLiteralString(export, "<UUID>"+marker+"</UUID>"),
		"a bad entity":   strings.Replace(export, "<DatabaseName>", "<DatabaseName>&"+marker+";", 1),
		"another root":   strings.ReplaceAll(export, "KeePassFile>", marker+">"),
		"no root groups": strings.ReplaceAll(strings.ReplaceAll(export, "<Group>", "<Other>"), "</Group>", "</Other>"),
	} {
		t.Run(name, func(t *testing.T) {
			err := judge.CompareExport([]byte(broken), want)
			if err == nil {
				t.Fatal("it passed")
			}
			if strings.Contains(err.Error(), marker) {
				t.Errorf("the error holds a value: %v", err)
			}
		})
	}
}

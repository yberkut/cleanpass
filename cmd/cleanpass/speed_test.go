package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yberkut/cleanpass/internal/testkit"
)

// The speed targets of the spec. Their numbers depend on the machine and on what else runs on it,
// so these tests run only when asked, on a machine with nothing else to do:
//
//	CLEANPASS_SPEED=1 go test -count=1 -run TestSpeed -v ./cmd/cleanpass
//
// CLEANPASS_SPEED_HISTORY=0,1 asks for only these numbers of older versions. docs/acceptance.md holds
// the numbers. A missed target fails here: the target stays as the spec has it.

const (
	startTarget   = 100 * time.Millisecond
	compareTarget = time.Second
	speedEntries  = 5000
	speedRuns     = 7
)

func wantSpeedTests(t *testing.T) {
	t.Helper()
	if os.Getenv("CLEANPASS_SPEED") == "" {
		t.Skip("set CLEANPASS_SPEED=1 on a machine with nothing else to do")
	}
}

// timed gives the best and the middle time of a run. The best one says what the program itself
// needs. The middle one says what a person sees.
func timed(t *testing.T, run func() testkit.Result) (best, middle time.Duration) {
	t.Helper()
	times := make([]time.Duration, 0, speedRuns)
	for range speedRuns {
		start := time.Now()
		got := run()
		times = append(times, time.Since(start))
		if got.Code >= 3 {
			t.Fatalf("exit code %d\n%s", got.Code, got.Stderr)
		}
	}
	slices.Sort(times)
	return times[0], times[len(times)/2]
}

func TestSpeedOfTheStart(t *testing.T) {
	wantSpeedTests(t)
	best, middle := timed(t, func() testkit.Result { return testkit.Run(t, "", "--help") })
	t.Logf("start: best %v, middle %v, target %v", best, middle, startTarget)
	if middle > startTarget {
		t.Errorf("the start takes %v, the target is %v", middle, startTarget)
	}
}

// speedXML gives a database like a real one: entries in groups, each with a protected password and
// the given number of older versions. edits holds the entries with a newer main version in this copy.
func speedXML(history, edits int) string {
	const stamp = "2026-03-15T10:00:00Z"
	version := func(id string, i, age int, password string) string {
		modified := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC).Add(-time.Duration(age) * 24 * time.Hour).Format(time.RFC3339)
		return fmt.Sprintf(`<UUID>%s</UUID><Times><LastModificationTime>%s</LastModificationTime><CreationTime>2020-01-01T10:00:00Z</CreationTime><LastAccessTime>%s</LastAccessTime><ExpiryTime>2030-01-01T10:00:00Z</ExpiryTime><Expires>False</Expires><UsageCount>1</UsageCount><LocationChanged>2020-01-01T10:00:00Z</LocationChanged></Times><String><Key>Title</Key><Value>Запис номер %d</Value></String><String><Key>UserName</Key><Value>user%d@example.com</Value></String><String><Key>Password</Key><Value ProtectInMemory="True">%s</Value></String><String><Key>URL</Key><Value>https://site%d.example.com/login</Value></String><String><Key>Notes</Key><Value>Нотатка до запису номер %d з кількома словами.</Value></String><AutoType><Enabled>True</Enabled><DataTransferObfuscation>0</DataTransferObfuscation></AutoType>`,
			id, modified, modified, i, i, password, i, i)
	}
	var xml strings.Builder
	fmt.Fprintf(&xml, `<?xml version="1.0" encoding="UTF-8"?><KeePassFile><Meta><Generator>KeePassXC</Generator><DatabaseName>Швидкість</DatabaseName><DatabaseNameChanged>%s</DatabaseNameChanged><HistoryMaxItems>-1</HistoryMaxItems><HistoryMaxSize>-1</HistoryMaxSize><MasterKeyChanged>%s</MasterKeyChanged><SettingsChanged>%s</SettingsChanged></Meta><Root><Group><UUID>AAAAAAAAAAAAAAAAAAAAEA==</UUID><Name>Root</Name><Times><LastModificationTime>%s</LastModificationTime><LocationChanged>%s</LocationChanged></Times><IsExpanded>True</IsExpanded><CustomData><Item><Key>keeps-this-file-in-kdbx-4</Key><Value>speed</Value></Item></CustomData>`, stamp, stamp, stamp, stamp, stamp)
	for i := range speedEntries {
		if i%500 == 0 {
			if i > 0 {
				xml.WriteString(`</Group>`)
			}
			fmt.Fprintf(&xml, `<Group><UUID>AAAAAAAAAAAAAAAAAAAB%cA==</UUID><Name>Група %d</Name><Times><LastModificationTime>%s</LastModificationTime><LocationChanged>%s</LocationChanged></Times><IsExpanded>True</IsExpanded>`, 'A'+i/500, i/500, stamp, stamp)
		}
		id := uuidOf(i)
		password := fmt.Sprintf("password-%d-of-the-entry", i)
		older := ""
		for age := history; age > 0; age-- {
			older += `<Entry>` + version(id, i, age, fmt.Sprintf("old-%d-%d", i, age)) + `</Entry>`
		}
		if i < edits {
			older += `<Entry>` + version(id, i, 0, password) + `</Entry>`
			xml.WriteString(`<Entry>` + strings.Replace(version(id, i, -1, "edited-"+password), "Нотатка", "Нова нотатка", 1) + `<History>` + older + `</History></Entry>`)
			continue
		}
		xml.WriteString(`<Entry>` + version(id, i, 0, password) + `<History>` + older + `</History></Entry>`)
	}
	xml.WriteString(`</Group></Group></Root></KeePassFile>`)
	return xml.String()
}

// uuidOf gives a UUID of its own to each entry, in base64.
func uuidOf(i int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	return "AAAAAAAAAAAAAAAAAC" + string(alphabet[i>>12&63]) + string(alphabet[i>>6&63]) + string(alphabet[i&63]) + "A=="
}

// speedCopies gives two copies of a real KeePassXC save with next to no KDF: its cost is 6 000 rounds
// of AES, far below a millisecond.
func speedCopies(t *testing.T, history int) (a, b testkit.DB) {
	t.Helper()
	dir := t.TempDir()
	saved := func(name string, edits int) testkit.DB {
		base := testkit.DB{Path: filepath.Join(dir, name+"-base.kdbx"), Password: testkit.Password}.Import(t, speedXML(history, edits))
		return base.WithEncryption(t, filepath.Join(dir, name+".kdbx"), testkit.AES256, testkit.KDFAES)
	}
	return saved("a", 0), saved("b", 50)
}

func TestSpeedOfCompareOnFiveThousandEntries(t *testing.T) {
	wantSpeedTests(t)
	for _, history := range []int{0, 1, 2, 5} {
		t.Run(fmt.Sprintf("%d older versions", history), func(t *testing.T) {
			if sizes := os.Getenv("CLEANPASS_SPEED_HISTORY"); sizes != "" && !slices.Contains(strings.Split(sizes, ","), fmt.Sprint(history)) {
				t.Skip("other sizes asked for")
			}
			a, b := speedCopies(t, history)
			info, err := os.Stat(a.Path)
			if err != nil {
				t.Fatal(err)
			}
			best, middle := timed(t, func() testkit.Result { return testkit.Run(t, passwordLines(2), "compare", a.Path, b.Path) })
			t.Logf("compare: file %.1f MB, best %v, middle %v, target %v", float64(info.Size())/1e6, best, middle, compareTarget)
			statusBest, statusMiddle := timed(t, func() testkit.Result { return testkit.Run(t, passwordLines(2), "status", a.Path, b.Path) })
			t.Logf("status: best %v, middle %v", statusBest, statusMiddle)
			// The spec does not say "without history", and a database in real use has one.
			if middle > compareTarget {
				t.Errorf("compare takes %v, the target is %v", middle, compareTarget)
			}
		})
	}
}

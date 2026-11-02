package main_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

func TestMergeStopsOnCopiesThatLookLikeDifferentDatabases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	home := newCopy(t, dir, "home.kdbx", testkit.RichXML())
	work := newCopy(t, dir, "work.kdbx", anotherDatabase(0x10))
	output := filepath.Join(dir, "new.kdbx")

	got := testkit.Run(t, passwordLines(2), "merge", home.Path, work.Path, "--output", output)

	wantStopped(t, got, output)
	for _, want := range []string{"Група 1: " + home.Path, "Група 2: " + work.Path, "--allow-unrelated"} {
		if !strings.Contains(got.Stderr, want) {
			t.Errorf("no %q in the message:\n%s", want, got.Stderr)
		}
	}
	wantUkrainian(t, got.Stdout, got.Stderr, home.Path, work.Path, "--allow-unrelated")
}

func TestMergeOfDifferentDatabasesGoesOnWithTheOption(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	home := newCopy(t, dir, "home.kdbx", mergeableXML())
	work := newCopy(t, dir, "work.kdbx", anotherDatabase(0x10))
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

	for name, args := range map[string][]string{
		"at the end":       {home.Path, work.Path, "--output", result.Path, "--allow-unrelated"},
		"before the files": {"--allow-unrelated", home.Path, work.Path, "--output", result.Path},
		"before the mark":  {"--allow-unrelated", "--output", result.Path, "--", home.Path, work.Path},
	} {
		t.Run(name, func(t *testing.T) {
			got := testkit.Run(t, passwordLines(2), append([]string{"merge"}, args...)...)
			wantMerged(t, got)
			wantInOutput(t, got.Stdout, "копії схожі на різні бази, але опція дозволила", "Група 1: "+home.Path, "Група 2: "+work.Path)
			export := strings.Join(result.ExportLines(t), "\n")
			for _, want := range []string{"Друге", "Банк"} {
				if !strings.Contains(export, want) {
					t.Errorf("KeePassXC does not see %s in the result", want)
				}
			}
			removeFile(t, result.Path)
		})
	}
}

// After -- every word is a file, also this one. So a file with such a name stays a file, and the
// stop stays on.
func TestAFileNamedLikeTheOptionDoesNotTakeOffTheStop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	home := newCopy(t, dir, "home.kdbx", testkit.RichXML())
	work := newCopy(t, dir, "--allow-unrelated", anotherDatabase(0x10))
	output := filepath.Join(dir, "new.kdbx")

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Dir: dir}, "merge", "--output", output, "--", home.Path, "--allow-unrelated")

	wantStopped(t, got, output)
	if !strings.Contains(got.Stderr, "Група 2: "+filepath.Base(work.Path)) {
		t.Errorf("no word about the second copy:\n%s", got.Stderr)
	}
}

// Related copies must not meet the stop: the protection is only as good as its silence.
func TestMergeOfAnImportUnderNewUUIDsDoesNotStop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := newCopy(t, dir, "base.kdbx", mergeableXML())
	imported := newCopy(t, dir, "imported.kdbx", withNewUUIDs(mergeableXML(), 0x20))
	output := filepath.Join(dir, "new.kdbx")

	wantMerged(t, testkit.Run(t, passwordLines(2), "merge", base.Path, imported.Path, "--output", output))
}

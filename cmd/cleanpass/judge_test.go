package main_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/judge"
	"github.com/yberkut/cleanpass/internal/safewriter"
	"github.com/yberkut/cleanpass/internal/testkit"
)

const (
	judgeAgreed = "Справжній KeePassXC перевірив результат"
	// judgeMissing starts each warning that no one outside looked at the result.
	judgeMissing = "Попередження: справжній KeePassXC цей результат не перевіряв"
	// The UUIDs of the rich fixture in the form of a message.
	mailEntryID = "00000000000000000000000000000100"
	// oldAttachmentBase64 is how the fixture holds the bytes of old.bin.
	oldAttachmentBase64 = "c3ludGhldGljIG9sZCBhdHRhY2htZW50"
)

func wantJudged(t *testing.T, got testkit.Result) {
	t.Helper()
	wantMerged(t, got)
	if !strings.Contains(got.Stdout, judgeAgreed) {
		t.Errorf("no word that KeePassXC checked the result:\n%s", got.Stdout)
	}
	if strings.Contains(got.Stdout+got.Stderr, judgeMissing) {
		t.Errorf("a warning about the check came:\n%s\n%s", got.Stdout, got.Stderr)
	}
}

// wantWarnedAndMerged checks a merge with no outside check: the work goes on, and the person knows.
func wantWarnedAndMerged(t *testing.T, got testkit.Result, result testkit.DB) {
	t.Helper()
	wantMerged(t, got)
	if !strings.Contains(got.Stdout, judgeMissing) {
		t.Errorf("no warning:\n%s", got.Stdout)
	}
	if strings.Contains(got.Stdout, judgeAgreed) {
		t.Errorf("it says that KeePassXC checked the result:\n%s", got.Stdout)
	}
	if listing := result.CLI(t, "ls"); !strings.Contains(listing, "Робота") {
		t.Errorf("the real KeePassXC does not read the result:\n%s", listing)
	}
}

// wantJudgeStopped checks a merge the Judge stopped, with the fake that gave the reason.
func wantJudgeStopped(t *testing.T, got testkit.Result, output string, fake *testkit.FakeCLI) {
	t.Helper()
	wantStopped(t, got, output)
	fake.WantLied(t)
	if !strings.Contains(got.Stderr, "Справжній KeePassXC") {
		t.Errorf("the stop does not come from the Judge:\n%s", got.Stderr)
	}
	if strings.Contains(got.Stdout+got.Stderr, secretMarker) {
		t.Errorf("the output holds a secret:\n%s\n%s", got.Stdout, got.Stderr)
	}
	if strings.Contains(got.Stdout, judgeAgreed) {
		t.Errorf("it says that KeePassXC agreed:\n%s", got.Stdout)
	}
	// The place and the paths in a message are not text in a language.
	strip := regexp.MustCompile(`місце: [^)]*|\S*\.kdbx`)
	wantUkrainian(t, "", strip.ReplaceAllString(got.Stderr, ""))
}

func commandsOf(t *testing.T, fake *testkit.FakeCLI) map[string]int {
	t.Helper()
	commands := map[string]int{}
	for _, call := range fake.Calls(t) {
		commands[call.Args[0]]++
	}
	return commands
}

func TestMergeLetsTheRealKeePassXCCheckTheResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{})
	result := filepath.Join(dir, "new.kdbx")

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "merge", a.Path, b.Path, "--output", result)

	wantJudged(t, got)
	wantNoTempFiles(t, dir)

	commands := commandsOf(t, fake)
	if commands["export"] != 1 {
		t.Errorf("export ran %d times", commands["export"])
	}
	// file.bin and same-content.bin on the two main versions, each read on its own. The older
	// version with old.bin needs a save, since keepassxc-cli reads attachments of main versions only.
	if commands["attachment-export"] != 2 {
		t.Errorf("attachment-export ran %d times", commands["attachment-export"])
	}
	if commands["mkdir"] != 1 {
		t.Errorf("a save by KeePassXC ran %d times", commands["mkdir"])
	}
}

func TestTheJudgeStaysOutOfTheFilesOfAPerson(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	beforeA, beforeB := digestOf(t, a.Path), digestOf(t, b.Path)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

	wantJudged(t, testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", result.Path))

	if digestOf(t, a.Path) != beforeA || digestOf(t, b.Path) != beforeB {
		t.Error("an input changed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("the directory holds %d files, want the two inputs and the result: %v", len(entries), entries)
	}
	// The group the Judge makes in its copy for a save must not reach the result.
	if listing := result.CLI(t, "ls"); strings.Contains(listing, "judge") {
		t.Errorf("the result holds what the Judge made:\n%s", listing)
	}
}

// Each change here is one thing the real program would then see in the result. A check of counts
// would miss most of them.
func TestTheJudgeStopsAtEveryDifferenceInWhatKeePassXCSees(t *testing.T) {
	t.Parallel()
	a, b := divergedPair(t, t.TempDir())
	beforeA, beforeB := digestOf(t, a.Path), digestOf(t, b.Path)
	for name, change := range map[string][2]string{
		"a password of a main version":  {divergedSecret, secretMarker + "-інший"},
		"a password of an old version":  {"SECRET-MARKER-старий пароль", "SECRET-MARKER-інший"},
		"the name of a custom field":    {"<Key>Власне поле</Key>", "<Key>Інше поле</Key>"},
		"the value of a custom field":   {"власне значення", "інше значення"},
		"the title of an entry":         {">Друге<", ">Третє<"},
		"the name of a group":           {"<Name>Робота</Name>", "<Name>Відпочинок</Name>"},
		"the name of the database":      {"Синтетична база", "Інша база"},
		"custom data of the database":   {"meta-custom-value", "other-value"},
		"a record about a deletion":     {"AAAAAAAAAAAAAAAAAAAHdw==", "AAAAAAAAAAAAAAAAAAAHeA=="},
		"the name of an attachment":     {"<Key>old.bin</Key>", "<Key>older.bin</Key>"},
		"a time":                        {"<UsageCount>7<", "<UsageCount>8<"},
		"a mark of a protected field":   {`ProtectInMemory="True"`, `ProtectInMemory="False"`},
		"a sequence of Auto-Type":       {"{USERNAME}{TAB}{PASSWORD}", "{PASSWORD}"},
		"an element in place of a time": {"<Times>", "<Other>"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeCLI(t, testkit.FakePlan{ExportOld: change[0], ExportNew: change[1]})
			output := filepath.Join(t.TempDir(), "new.kdbx")

			got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "merge", a.Path, b.Path, "--output", output)

			wantJudgeStopped(t, got, output, fake)
			if digestOf(t, a.Path) != beforeA || digestOf(t, b.Path) != beforeB {
				t.Error("an input changed")
			}
		})
	}
}

func TestTheJudgeStopsAtOtherBytesOfAnAttachmentOfAMainVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	const other = "other bytes that the file does not hold"
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{AttachmentName: "file.bin", AttachmentBytes: other})
	output := filepath.Join(dir, "new.kdbx")

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "merge", a.Path, b.Path, "--output", output)

	wantJudgeStopped(t, got, output, fake)
	if !strings.Contains(got.Stderr, mailEntryID) {
		t.Errorf("the message does not name the entry:\n%s", got.Stderr)
	}
	if strings.Contains(got.Stdout+got.Stderr, other) || strings.Contains(got.Stdout+got.Stderr, richFileBytes) {
		t.Errorf("the output holds the bytes:\n%s\n%s", got.Stdout, got.Stderr)
	}
}

// keepassxc-cli cannot read an attachment of an older version. It can only save it. So the save
// here gives a file with other bytes under the same name.
func TestTheJudgeStopsAtOtherBytesOfAnAttachmentOfAnOlderVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	swapped := testkit.DB{Path: filepath.Join(t.TempDir(), "swapped.kdbx"), Password: testkit.Password}.Import(t,
		replaced(t, mergeableXML(), oldAttachmentBase64, base64.StdEncoding.EncodeToString([]byte("other old bytes"))))
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{SwapAfterMkdir: swapped.Path})
	output := filepath.Join(dir, "new.kdbx")

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(1), Env: fake.Env()}, "merge", a.Path, "--output", output)

	wantJudgeStopped(t, got, output, fake)
	if !strings.Contains(got.Stderr, mailEntryID) {
		t.Errorf("the message does not name the entry:\n%s", got.Stderr)
	}
}

// sameTitleXML holds two entries on one path, each with its own attachment under one name. The
// program finds an entry by its path, so it can read only one of them directly.
func sameTitleXML(t *testing.T, secondBytes string) string {
	t.Helper()
	const second = `<Entry><UUID>AAAAAAAAAAAAAAAAAAABCg==</UUID><IconID>0</IconID><ForegroundColor/><BackgroundColor/><OverrideURL/><Tags/><Times>
  <LastModificationTime>2026-03-20T10:00:00Z</LastModificationTime><CreationTime>2026-03-20T10:00:01Z</CreationTime>
  <LastAccessTime>2026-03-20T10:00:02Z</LastAccessTime><ExpiryTime>2030-01-01T10:00:08Z</ExpiryTime>
  <Expires>False</Expires><UsageCount>0</UsageCount><LocationChanged>2026-03-20T10:00:03Z</LocationChanged>
</Times><String><Key>Title</Key><Value>Друге</Value></String><Binary><Key>same-content.bin</Key><Value Ref="2"/></Binary><AutoType><Enabled>True</Enabled><DataTransferObfuscation>0</DataTransferObfuscation></AutoType><History/></Entry>`
	return replaced(t, mergeableXML(),
		"</Binaries>", `<Binary ID="2" Compressed="False">`+base64.StdEncoding.EncodeToString([]byte(secondBytes))+`</Binary></Binaries>`,
		"<Entry><UUID>AAAAAAAAAAAAAAAAAAABAQ==</UUID>", second+"<Entry><UUID>AAAAAAAAAAAAAAAAAAABAQ==</UUID>")
}

func TestTheJudgeTakesTwoEntriesOnOnePathForWhatTheyAre(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, sameTitleXML(t, "bytes of the second"))
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{})

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(1), Env: fake.Env()}, "merge", a.Path, "--output", filepath.Join(dir, "new.kdbx"))

	wantJudged(t, got)
	// One for each path. The second entry on a path goes through the save.
	commands := commandsOf(t, fake)
	if commands["attachment-export"] != 2 || commands["mkdir"] != 1 {
		t.Errorf("the calls were %v", commands)
	}
}

func TestTheJudgeStopsAtOtherBytesOfTheSecondEntryOnOnePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, sameTitleXML(t, "bytes of the second"))
	swapped := testkit.DB{Path: filepath.Join(t.TempDir(), "swapped.kdbx"), Password: testkit.Password}.Import(t, sameTitleXML(t, "other bytes"))
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{SwapAfterMkdir: swapped.Path})
	output := filepath.Join(dir, "new.kdbx")

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(1), Env: fake.Env()}, "merge", a.Path, "--output", output)

	wantJudgeStopped(t, got, output, fake)
}

func TestTheJudgeStopsWhenKeePassXCFails(t *testing.T) {
	t.Parallel()
	a, b := divergedPair(t, t.TempDir())
	for _, command := range []string{"export", "attachment-export", "mkdir"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeCLI(t, testkit.FakePlan{FailCommand: command})
			output := filepath.Join(t.TempDir(), "new.kdbx")

			got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "merge", a.Path, b.Path, "--output", output)

			wantJudgeStopped(t, got, output, fake)
		})
	}
}

func TestTheJudgeStopsAtAnExportThatMakesNoSense(t *testing.T) {
	t.Parallel()
	a, b := divergedPair(t, t.TempDir())
	for name, text := range map[string]string{
		"nothing":     "",
		"not XML":     secretMarker + " is not XML",
		"a broken":    "<KeePassFile><Meta><Generator>" + secretMarker,
		"another":     "<Other>" + secretMarker + "</Other>",
		"a bad value": "<KeePassFile><Meta><HistoryMaxItems>" + secretMarker + "</HistoryMaxItems></Meta></KeePassFile>",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeCLI(t, testkit.FakePlan{ExportText: &text})
			output := filepath.Join(t.TempDir(), "new.kdbx")

			got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "merge", a.Path, b.Path, "--output", output)

			wantJudgeStopped(t, got, output, fake)
		})
	}
}

func TestThePasswordGoesToKeePassXCOnlyThroughStdin(t *testing.T) {
	t.Parallel()
	const password = "пароль-для-судді " + testkit.Password
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: password}.Import(t, mergeableXML())
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{})

	got := testkit.RunWith(t, testkit.Options{Stdin: password + "\n", Env: fake.Env()}, "merge", a.Path, "--output", filepath.Join(dir, "new.kdbx"))

	wantJudged(t, got)
	calls := fake.Calls(t)
	if len(calls) < 3 {
		t.Fatalf("only %d calls", len(calls))
	}
	for _, call := range calls {
		for _, argument := range call.Args {
			if strings.Contains(argument, testkit.Password) {
				t.Errorf("%s has the password in an argument", call.Args[0])
			}
		}
		for _, variable := range call.Env {
			if strings.Contains(variable, testkit.Password) {
				t.Errorf("%s has the password in the environment", call.Args[0])
			}
		}
		if call.Args[0] == "--version" {
			if call.Stdin != "" {
				t.Error("the question about the version got the password")
			}
			continue
		}
		if call.Stdin != password+"\n" {
			t.Errorf("%s did not get one line with the password on stdin", call.Args[0])
		}
	}
}

// A person can protect a title, and then it is a secret. Other programs on the system can read the
// arguments of a process, so the Judge reads the attachments of such an entry through a save.
func TestTheJudgeNeverPutsAProtectedTitleIntoAnArgument(t *testing.T) {
	t.Parallel()
	const marker = "PROTECTED-TITLE-MARKER"
	dir := t.TempDir()
	xml := replaced(t, mergeableXML(), "<Value>Друге</Value>", `<Value ProtectInMemory="True">Друге `+marker+`</Value>`)
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, xml)
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{})

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(1), Env: fake.Env()}, "merge", a.Path, "--output", filepath.Join(dir, "new.kdbx"))

	wantJudged(t, got)
	for _, call := range fake.Calls(t) {
		for _, argument := range call.Args {
			if strings.Contains(argument, marker) {
				t.Errorf("%s has the protected title in an argument", call.Args[0])
			}
		}
		for _, variable := range call.Env {
			if strings.Contains(variable, marker) {
				t.Errorf("%s has the protected title in the environment", call.Args[0])
			}
		}
	}
	if commandsOf(t, fake)["mkdir"] == 0 {
		t.Error("no save read the attachments of the entry with the protected title")
	}
}

// The keys of a result differ from copy to copy, and the real program must open each kind.
func TestTheJudgeOpensAResultWithEveryKindOfKeys(t *testing.T) {
	t.Parallel()
	t.Run("a password and a key file", func(t *testing.T) {
		dir := t.TempDir()
		key := testkit.NewKeyFile(t, filepath.Join(dir, "мій ключ.bin"), 3)
		a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password, KeyFile: key}.Import(t, mergeableXML())
		fake := testkit.NewFakeCLI(t, testkit.FakePlan{})

		got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(1), Env: fake.Env()}, "merge", a.Path, "--key-file", key, "--output", filepath.Join(dir, "new.kdbx"))

		wantJudged(t, got)
		for _, call := range fake.Calls(t) {
			if call.Args[0] != "--version" && !slices.Contains(call.Args, key) {
				t.Errorf("%s did not get the key file", call.Args[0])
			}
		}
	})
	t.Run("only a key file", func(t *testing.T) {
		dir := t.TempDir()
		key := testkit.NewKeyFile(t, filepath.Join(dir, "key.bin"), 4)
		a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), KeyFile: key}.Import(t, mergeableXML())
		fake := testkit.NewFakeCLI(t, testkit.FakePlan{})

		got := testkit.RunWith(t, testkit.Options{Stdin: "\n", Env: fake.Env()}, "merge", a.Path, "--key-file", key, "--output", filepath.Join(dir, "new.kdbx"))

		wantJudged(t, got)
		for _, call := range fake.Calls(t) {
			if call.Args[0] != "--version" && (!slices.Contains(call.Args, "--no-password") || call.Stdin != "") {
				t.Errorf("%s: args %q, stdin %q", call.Args[0], call.Args, call.Stdin)
			}
		}
	})
	t.Run("the empty password", func(t *testing.T) {
		dir := t.TempDir()
		a := testkit.ImportWithEmptyPassword(t, filepath.Join(dir, "a.kdbx"), mergeableXML())
		wantJudged(t, testkit.Run(t, "\n", "merge", a.Path, "--output", filepath.Join(dir, "new.kdbx")))
	})
}

func TestTheJudgeReadsAttachmentsUnderAnyNames(t *testing.T) {
	t.Parallel()
	for name, change := range map[string][]string{
		"names that look like options":  {"<Key>file.bin</Key>", "<Key>--force</Key>", "<Key>same-content.bin</Key>", "<Key>-k</Key>"},
		"names with spaces and letters": {"<Key>file.bin</Key>", `<Key>файл "з" лапками &amp; пробілом.bin</Key>`},
		"a slash in a title":            {"<Value>Друге</Value>", "<Value>Друге / з похилою</Value>"},
		"an empty title":                {"<Value>Друге</Value>", "<Value></Value>"},
		"a line break in a title":       {"<Value>Друге</Value>", "<Value>Друге\nз новим рядком</Value>"},
		"a slash in a group":            {"<Name>Робота</Name>", "<Name>Робота/Вдома</Name>"},
		"a group with no name":          {"<Name>Робота</Name>", "<Name></Name>"},
		"a name with an empty end":      {"<Name>Робота</Name>", "<Name>  Робота  </Name>"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "папка з пробілом")
			a := testkit.DB{Path: filepath.Join(dir, "моя копія.kdbx"), Password: testkit.Password}.Import(t, replaced(t, mergeableXML(), change...))
			fake := testkit.NewFakeCLI(t, testkit.FakePlan{})

			got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(1), Env: fake.Env()}, "merge", a.Path, "--output", filepath.Join(dir, "нова база.kdbx"))

			wantJudged(t, got)
			if count := commandsOf(t, fake)["attachment-export"]; count != 2 {
				t.Errorf("attachment-export ran %d times", count)
			}
			wantNoTempFiles(t, dir)
		})
	}
}

// Bytes reach the Judge whole only through a real stream of bytes. A text stream would change
// line ends and what is not valid UTF-8, and a false stop would leave the person without a result.
func TestTheJudgeReadsEveryKindOfBytes(t *testing.T) {
	t.Parallel()
	allBytes := make([]byte, 0, 256*4096)
	for range 4096 {
		for value := range 256 {
			allBytes = append(allBytes, byte(value))
		}
	}
	dir := t.TempDir()
	a := testkit.DB{Path: filepath.Join(dir, "a.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	for name, content := range map[string]string{
		"empty.bin":      "",
		"lines.txt":      "first\nsecond\r\nthird\rlast\n",
		"not-utf8.bin":   "\xff\xfe\x00 raw \x80",
		"looks-b64.txt":  "abcdQUJD",
		"zero-bytes.bin": "\x00\x00\x00",
		"big.bin":        string(allBytes),
	} {
		a.AttachFile(t, richWorkEntry, name, []byte(content))
	}
	wantJudged(t, testkit.Run(t, passwordLines(1), "merge", a.Path, "--output", filepath.Join(dir, "new.kdbx")))
}

// A file that holds secrets in clear text could stay behind on a flash drive even when it is
// removed. So the fake looks at the directories when each call of the real program has ended, and
// at that moment the Judge has not yet had a chance to clean up.
func TestTheJudgeLeavesNoClearTextInAnyFile(t *testing.T) {
	t.Parallel()
	const canary = "a text only the canary file holds"
	dir, temp := t.TempDir(), t.TempDir()
	a, b := divergedPair(t, dir)
	canaryFile := filepath.Join(temp, "canary.txt")
	if err := os.WriteFile(canaryFile, []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{
		WatchDirs: []string{dir, temp},
		WatchFor:  []string{canary, secretMarker, testkit.Password, "synthetic attachment bytes", "synthetic old attachment", "власне значення"},
	})

	got := testkit.RunWith(t, testkit.Options{
		Stdin: passwordLines(2),
		Env:   append(fake.Env(), "TMP="+temp, "TEMP="+temp, "TMPDIR="+temp),
	}, "merge", a.Path, b.Path, "--output", filepath.Join(dir, "new.kdbx"))

	wantJudged(t, got)
	calls := fake.Calls(t)
	if len(calls) < 4 {
		t.Fatalf("only %d calls", len(calls))
	}
	for _, call := range calls {
		// The canary shows that the look works.
		if !slices.Contains(call.ClearText, canaryFile) {
			t.Errorf("after %s the fake did not see the canary file", call.Args[0])
		}
		for _, file := range call.ClearText {
			if file != canaryFile {
				t.Errorf("after %s the file %s holds clear text", call.Args[0], file)
			}
		}
	}
	wantNoSecretInFiles(t, dir)
}

// The Judge reads no one's secrets into its messages, whatever the real program sees.
func TestTheJudgeLeavesNoSecretInTheOutputOfAGoodRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	got := testkit.Run(t, passwordLines(2), "merge", a.Path, b.Path, "--output", filepath.Join(dir, "new.kdbx"))
	wantJudged(t, got)
	for _, secret := range []string{secretMarker, testkit.Password, richFileBytes} {
		if strings.Contains(got.Stdout+got.Stderr, secret) {
			t.Errorf("the output holds %q:\n%s\n%s", secret, got.Stdout, got.Stderr)
		}
	}
}

// The tool runs from a flash drive, in a shell whose PATH may not know KeePassXC. The test is about
// the place the installers use, so it has nothing to say on a system without such an install.
func TestMergeFindsKeePassXCInItsInstallPlaceWithoutPath(t *testing.T) {
	t.Parallel()
	place := map[string]string{
		"windows": `C:\Program Files\KeePassXC\keepassxc-cli.exe`,
		"darwin":  "/Applications/KeePassXC.app/Contents/MacOS/keepassxc-cli",
		"linux":   "/usr/bin/keepassxc-cli",
	}[runtime.GOOS]
	if _, err := os.Stat(place); err != nil {
		t.Skipf("no install at %s", place)
	}
	dir := t.TempDir()
	a, b := divergedPair(t, dir)

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: []string{"PATH=" + t.TempDir()}}, "merge", a.Path, b.Path, "--output", filepath.Join(dir, "new.kdbx"))

	wantJudged(t, got)
}

func TestMergeWarnsAndGoesOnWithoutKeePassXC(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}
	opts := testkit.Options{
		Stdin: passwordLines(2),
		Env:   []string{"PATH=" + t.TempDir(), safewriter.FaultVariable + "=" + judge.FaultNoInstallPlaces},
	}

	got := testkit.RunWith(t, opts, "merge", a.Path, b.Path, "--output", result.Path)

	wantWarnedAndMerged(t, got, result)
	if !strings.Contains(got.Stdout, "keepassxc-cli") {
		t.Errorf("the warning does not name the program:\n%s", got.Stdout)
	}
	wantUkrainian(t, got.Stdout, got.Stderr, a.Path, b.Path, result.Path, "keepassxc-cli", "PATH", "b.example", "mail.example.com", "user@example.com", "quoted")
	keptAllLines(t, result, a, b)
}

// The program gets the password. A file that only lies in the current directory must not get it,
// also when PATH leads there. Windows looks in the current directory even without that.
func TestTheJudgeRunsNoProgramFromTheCurrentDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{})
	work := t.TempDir()
	program, err := os.ReadFile(fake.Program())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, filepath.Base(fake.Program())), program, 0o755); err != nil {
		t.Fatal(err)
	}
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}
	opts := testkit.Options{
		Stdin: passwordLines(2),
		Dir:   work,
		Env:   append(fake.PlanEnv(), "PATH=.", safewriter.FaultVariable+"="+judge.FaultNoInstallPlaces),
	}

	got := testkit.RunWith(t, opts, "merge", a.Path, b.Path, "--output", result.Path)

	wantWarnedAndMerged(t, got, result)
	if calls := fake.Calls(t); len(calls) != 0 {
		t.Errorf("the program from the current directory ran %d times", len(calls))
	}
}

// Windows runs a .cmd or a .bat file through cmd.exe, and that reads its arguments as a command. The
// title of an entry in a database would then run as a part of a command.
func TestTheJudgeRunsNoScriptOnWindows(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("a script takes its arguments as they are on this system")
	}
	a, b := divergedPair(t, t.TempDir())
	for _, name := range []string{"keepassxc-cli.cmd", "keepassxc-cli.bat"} {
		t.Run(name, func(t *testing.T) {
			programs := t.TempDir()
			ran := filepath.Join(t.TempDir(), "ran.txt")
			script := "@echo off\r\necho ran> \"" + ran + "\"\r\necho 2.7.12\r\n"
			if err := os.WriteFile(filepath.Join(programs, name), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			result := testkit.DB{Path: filepath.Join(t.TempDir(), "new.kdbx"), Password: testkit.Password}
			opts := testkit.Options{
				Stdin: passwordLines(2),
				Env:   []string{"PATH=" + programs, safewriter.FaultVariable + "=" + judge.FaultNoInstallPlaces},
			}

			got := testkit.RunWith(t, opts, "merge", a.Path, b.Path, "--output", result.Path)

			wantWarnedAndMerged(t, got, result)
			if _, err := os.Stat(ran); err == nil {
				t.Error("the script ran")
			}
		})
	}
}

// A program from before KDBX 4.1 could not read any result, and that says nothing about the result.
func TestAProgramThatCannotJudgeGivesAWarning(t *testing.T) {
	t.Parallel()
	a, b := divergedPair(t, t.TempDir())
	for name, tc := range map[string]struct {
		plan testkit.FakePlan
		want string
	}{
		"an old version":   {testkit.FakePlan{Version: "2.6.6"}, "2.6.6"},
		"one that fails":   {testkit.FakePlan{VersionFails: true}, "не запускається"},
		"the oldest major": {testkit.FakePlan{Version: "1.9.0"}, "1.9.0"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeCLI(t, tc.plan)
			result := testkit.DB{Path: filepath.Join(t.TempDir(), "new.kdbx"), Password: testkit.Password}

			got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "merge", a.Path, b.Path, "--output", result.Path)

			wantWarnedAndMerged(t, got, result)
			fake.WantLied(t)
			for _, want := range []string{tc.want, fake.Program()} {
				if !strings.Contains(got.Stdout, want) {
					t.Errorf("the warning does not hold %q:\n%s", want, got.Stdout)
				}
			}
			if commands := commandsOf(t, fake); commands["export"] != 0 {
				t.Errorf("the calls were %v", commands)
			}
		})
	}
}

func TestAVersionThatIsNewEnoughGetsTheCheck(t *testing.T) {
	t.Parallel()
	a, b := divergedPair(t, t.TempDir())
	for _, version := range []string{"2.7.0", "2.8.1", "3.0.0", "a version of an unknown form"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeCLI(t, testkit.FakePlan{Version: version})

			got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "merge", a.Path, b.Path, "--output", filepath.Join(t.TempDir(), "new.kdbx"))

			wantJudged(t, got)
		})
	}
}

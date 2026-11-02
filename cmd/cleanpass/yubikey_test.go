package main_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/judge"
	"github.com/yberkut/cleanpass/internal/safewriter"
	"github.com/yberkut/cleanpass/internal/testkit"
	"github.com/yberkut/cleanpass/internal/testkit/softkey"
)

// wantChallenges checks each call of the fake: the form Yubico documents, and these challenges in
// this order.
func wantChallenges(t *testing.T, fake *testkit.FakeYkman, slot string, challenges ...string) {
	t.Helper()
	calls := fake.Calls(t)
	if len(calls) != len(challenges) {
		t.Fatalf("ykman ran %d times, want %d: %v", len(calls), len(challenges), calls)
	}
	for i, call := range calls {
		if want := []string{"otp", "calculate", slot, challenges[i]}; !slices.Equal(call.Args, want) {
			t.Errorf("call %d: %q, want %q", i+1, call.Args, want)
		}
	}
}

// challengeOf gives the challenge the spec wants for a file: its KDF salt and 32 bytes of the
// value 0x20, as hex. The salt comes from the own reader of the header, and all else is literal.
func challengeOf(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	header, err := gateway.ReadHeader(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(header.Salt) != 32 {
		t.Fatalf("salt of %d bytes", len(header.Salt))
	}
	return hex.EncodeToString(header.Salt) + strings.Repeat("20", 32)
}

func TestCompareOpensADatabaseOfKdbxwebWithAYubiKey(t *testing.T) {
	t.Parallel()
	for name, arguments := range map[string][]string{
		"long option":  {"--yubikey", "2"},
		"short option": {"-y", "2"},
		"equals sign":  {"--yubikey=2"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "база.kdbx"))
			fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

			got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: fake.Env()}, append([]string{"compare", db.Path}, arguments...)...)

			if got.Code != 0 {
				t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
			}
			wantField(t, passports(t, got.Stdout, db.Path)[db.Path], "Записів", "1")
			wantChallenges(t, fake, "2", challengeOf(t, db.Path))
		})
	}
}

// noJudge makes a run find the fake key and no keepassxc-cli at all. The real program could not meet
// a key in software, and it would stop the merge after a wait for a touch.
func noJudge(fake *testkit.FakeYkman) []string {
	return append(fake.Env(), safewriter.FaultVariable+"="+judge.FaultNoInstallPlaces)
}

func TestAResultWithAYubiKeyHasANewSaltAndKdbxwebReadsIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.SoftHMAC(t, filepath.Join(dir, "a.kdbx"))
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: db.Password}

	got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: noJudge(fake)}, "merge", db.Path, "--yubikey", "2", "--output", result.Path)

	wantMerged(t, got)
	if challengeOf(t, result.Path) == challengeOf(t, db.Path) {
		t.Error("the result has the salt of the copy")
	}
	// One response opens the copy. The new salt needs its own, and the read back of the temporary
	// file has the same salt, so it needs none.
	wantChallenges(t, fake, "2", challengeOf(t, db.Path), challengeOf(t, result.Path))

	if !strings.Contains(got.Stdout, "YubiKey, слот 2") {
		t.Errorf("it does not say that the new file needs the key:\n%s", got.Stdout)
	}
	wantUkrainian(t, got.Stdout, "", db.Path, result.Path, "YubiKey", "keepassxc-cli", "PATH", "Argon2id")

	if titles := result.KdbxwebTitles(t, softkey.FixtureSecret); !slices.Equal(titles, []string{testkit.SoftHMACTitle}) {
		t.Errorf("kdbxweb sees %q", titles)
	}
}

// softKeyCopy is a rich copy with attachments, made by the real KeePassXC, that then needs the key
// with the secret of the fixture.
func softKeyCopy(t *testing.T, dir, name, xml string) testkit.DB {
	t.Helper()
	source := testkit.DB{Path: filepath.Join(t.TempDir(), "source.kdbx"), Password: testkit.Password}.Import(t, xml)
	return source.WithSoftKey(t, filepath.Join(dir, name), softkey.FixtureSecret)
}

// withJudgeOfSoftKey makes a run find the fake key and a fake keepassxc-cli that can meet that key.
func withJudgeOfSoftKey(t *testing.T, ykman *testkit.FakeYkman, plan testkit.FakePlan) (*testkit.FakeCLI, []string) {
	t.Helper()
	plan.YubiKeySecret = softkey.FixtureSecret
	keepassxc := testkit.NewFakeCLI(t, plan)
	return keepassxc, slices.Concat(ykman.PlanEnv(), keepassxc.PlanEnv(), []string{testkit.PathTo(ykman.Dir, keepassxc.Dir)})
}

// The real program cannot meet a key in software, so this says nothing about KeePassXC and a real
// key. It says what the Judge asks for, and how much of that the human has to be told.
func TestTheJudgeGivesTheSlotToEveryCallAndSaysHowManyTouchesItTakes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := softKeyCopy(t, dir, "a.kdbx", mergeableXML())
	ykman := testkit.NewFakeYkman(t, testkit.YkmanPlan{})
	keepassxc, env := withJudgeOfSoftKey(t, ykman, testkit.FakePlan{})
	result := filepath.Join(dir, "new.kdbx")

	got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: env}, "merge", db.Path, "--yubikey", "2", "--output", result)

	wantJudged(t, got)
	wantNoTempFiles(t, dir)
	commands := commandsOf(t, keepassxc)
	if commands["export"] != 1 || commands["attachment-export"] != 2 || commands["mkdir"] != 1 {
		t.Fatalf("the calls were %v", commands)
	}
	for _, call := range keepassxc.Calls(t) {
		if call.Args[0] == "--version" {
			continue
		}
		at := slices.Index(call.Args, "--yubikey")
		end := slices.Index(call.Args, "--")
		if at < 0 || at+1 >= len(call.Args) || call.Args[at+1] != "2" || at > end {
			t.Errorf("%s did not get the slot as an option: %q", call.Args[0], call.Args)
		}
		if call.Stdin != db.Password+"\n" {
			t.Errorf("%s did not get the password on stdin", call.Args[0])
		}
	}
	// Each call opens the file, and a save locks it again with a new salt. The saved copy has a
	// salt the tool has not seen, so its read back goes to the key through the tool too.
	touchesOfKeePassXC := commands["export"] + commands["attachment-export"] + 2*commands["mkdir"]
	readsOfTheSavedCopy := len(ykman.Calls(t)) - 2 // the copy and the result come before the Judge
	if readsOfTheSavedCopy != 1 {
		t.Errorf("ykman ran %d times for the saved copy", readsOfTheSavedCopy)
	}
	want := touchesOfKeePassXC + readsOfTheSavedCopy
	if !strings.Contains(got.Stderr, "Дотиків буде до "+strconv.Itoa(want)) {
		t.Errorf("no word about %d touches:\n%s", want, got.Stderr)
	}
}

func TestACopyWithNoAttachmentTakesOneTouchInTheJudge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.SoftHMAC(t, filepath.Join(dir, "a.kdbx"))
	ykman := testkit.NewFakeYkman(t, testkit.YkmanPlan{})
	keepassxc, env := withJudgeOfSoftKey(t, ykman, testkit.FakePlan{})

	got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: env}, "merge", db.Path, "-y", "2", "--output", filepath.Join(dir, "new.kdbx"))

	wantJudged(t, got)
	if commands := commandsOf(t, keepassxc); commands["export"] != 1 || commands["attachment-export"] != 0 || commands["mkdir"] != 0 {
		t.Errorf("the calls were %v", commands)
	}
	if !strings.Contains(got.Stderr, "Дотиків буде до 1:") {
		t.Errorf("no word about one touch:\n%s", got.Stderr)
	}
}

func TestTheJudgeAsksForNoTouchWithoutAYubiKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := divergedPair(t, dir)
	fake := testkit.NewFakeCLI(t, testkit.FakePlan{})

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "merge", a.Path, b.Path, "--output", filepath.Join(dir, "new.kdbx"))

	wantJudged(t, got)
	if strings.Contains(got.Stdout+got.Stderr, "Дотиків") || strings.Contains(got.Stdout+got.Stderr, "Торкніться") {
		t.Errorf("a word about a key came:\n%s\n%s", got.Stdout, got.Stderr)
	}
	for _, call := range fake.Calls(t) {
		if slices.Contains(call.Args, "--yubikey") {
			t.Errorf("%s got a slot", call.Args[0])
		}
	}
}

func TestAResultNeedsItsOwnResponseOrItIsNotCreated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.SoftHMAC(t, filepath.Join(dir, "a.kdbx"))
	before := digestOf(t, db.Path)
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{FailCalls: []int{2}})
	output := filepath.Join(dir, "new.kdbx")

	got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: noJudge(fake)}, "merge", db.Path, "-y", "2", "--output", output)

	wantStopped(t, got, output)
	if !strings.Contains(got.Stderr, "для нового файла") {
		t.Errorf("no clear error:\n%s", got.Stderr)
	}
	if digestOf(t, db.Path) != before {
		t.Error("the input changed")
	}
}

func TestAKeyThatGivesNoResponseGivesAClearErrorAndANewTry(t *testing.T) {
	t.Parallel()
	notHex, tooShort := secretMarker+" is not hex\n", "abcd\n"
	for name, plan := range map[string]testkit.YkmanPlan{
		"ykman fails":                   {FailCalls: []int{1}},
		"an answer that is not hex":     {Output: &notHex},
		"an answer of the wrong length": {Output: &tooShort},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "a.kdbx"))
			fake := testkit.NewFakeYkman(t, plan)

			// Then Enter repeats the password and keeps the key file and the slot.
			got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n\n\n\n", Env: fake.Env()}, "compare", db.Path, "-y", "2")

			if !strings.Contains(got.Stderr, "YubiKey не дав відповіді (слот 2)") {
				t.Errorf("no clear error:\n%s", got.Stderr)
			}
			if strings.Contains(got.Stdout+got.Stderr, secretMarker) {
				t.Errorf("the output holds what ykman printed:\n%s\n%s", got.Stdout, got.Stderr)
			}
			wantUkrainian(t, got.Stdout, got.Stderr, db.Path, "YubiKey", "challenge-response", "Argon2id")
			// A failure stays out of the memory: the new try asks the key again.
			challenge := challengeOf(t, db.Path)
			if plan.FailCalls != nil {
				if got.Code != 0 {
					t.Errorf("exit code %d after the new try:\n%s", got.Code, got.Stderr)
				}
				wantChallenges(t, fake, "2", challenge, challenge)
			} else if got.Code != 3 {
				t.Errorf("exit code %d, want 3", got.Code)
			}
		})
	}
}

func TestAnEmptySlotGivesAClearError(t *testing.T) {
	t.Parallel()
	db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "a.kdbx"))
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

	got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: fake.Env()}, "compare", db.Path, "-y", "1")

	if got.Code != 3 || !strings.Contains(got.Stderr, "YubiKey не дав відповіді (слот 1)") {
		t.Errorf("exit code %d:\n%s", got.Code, got.Stderr)
	}
}

func TestSlotCanBeGivenByAnswerToAQuestion(t *testing.T) {
	t.Parallel()
	db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "a.kdbx"))
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

	// The first try has no key and fails. The retry asks for the password, the key file and the slot.
	got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n\n\n2\n", Env: fake.Env()}, "compare", db.Path)

	wantOpenedWithKey(t, got, db.Path)
	if !strings.Contains(got.Stderr, "Слот YubiKey для "+db.Path) {
		t.Errorf("no question about the slot:\n%s", got.Stderr)
	}
	wantChallenges(t, fake, "2", challengeOf(t, db.Path))
}

func wantOpenedWithKey(t *testing.T, got testkit.Result, paths ...string) {
	t.Helper()
	if got.Code != 0 {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", got.Code, got.Stdout, got.Stderr)
	}
	passports(t, got.Stdout, paths...)
}

// A person who lost count of the questions may type a password here.
func TestABadSlotInAnAnswerIsNotRepeatedAndAsksAgain(t *testing.T) {
	t.Parallel()
	db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "a.kdbx"))
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})
	stdin := db.Password + "\n\n\n" + secretMarker + "\n3\n2\n"

	got := testkit.RunWith(t, testkit.Options{Stdin: stdin, Env: fake.Env()}, "compare", db.Path)

	wantOpenedWithKey(t, got, db.Path)
	if strings.Contains(got.Stdout+got.Stderr, secretMarker) {
		t.Errorf("the answer is in the output:\n%s", got.Stderr)
	}
	if count := strings.Count(got.Stderr, "це 1 або 2"); count != 2 {
		t.Errorf("%d messages about a bad slot:\n%s", count, got.Stderr)
	}
}

func TestAWrongSlotOptionCanBeCorrectedByAnAnswer(t *testing.T) {
	t.Parallel()
	db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "a.kdbx"))
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{Slots: map[int]string{1: strings.Repeat("ab", 20), 2: softkey.FixtureSecret}})

	got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n\n\n2\n", Env: fake.Env()}, "compare", db.Path, "-y", "1")

	wantOpenedWithKey(t, got, db.Path)
	if !strings.Contains(got.Stderr, "слот YubiKey (1) для "+db.Path) {
		t.Errorf("no clear error:\n%s", got.Stderr)
	}
	if !strings.Contains(got.Stderr, "Enter — лишити слот 1") {
		t.Errorf("the question does not name the slot of the option:\n%s", got.Stderr)
	}
}

func TestTheSameChallengeNeedsOneTouch(t *testing.T) {
	t.Parallel()
	t.Run("a new try after a wrong password", func(t *testing.T) {
		t.Parallel()
		db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "a.kdbx"))
		fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

		got := testkit.RunWith(t, testkit.Options{Stdin: "хибний\n" + db.Password + "\n\n\n", Env: fake.Env()}, "compare", db.Path, "-y", "2")

		wantOpenedWithKey(t, got, db.Path)
		wantChallenges(t, fake, "2", challengeOf(t, db.Path))
		if count := strings.Count(got.Stderr, "Торкніться"); count != 1 {
			t.Errorf("%d hints to touch for one touch:\n%s", count, got.Stderr)
		}
	})
	t.Run("two copies with one salt", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a := testkit.SoftHMAC(t, filepath.Join(dir, "a.kdbx"))
		b := testkit.SoftHMAC(t, filepath.Join(dir, "b.kdbx"))
		fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

		got := testkit.RunWith(t, testkit.Options{Stdin: a.Password + "\n\n", Env: fake.Env()}, "compare", a.Path, "-y", "2", b.Path, "-y", "2")

		wantOpenedWithKey(t, got, a.Path, b.Path)
		wantChallenges(t, fake, "2", challengeOf(t, a.Path))
	})
}

func TestTheSlotIsSetForEachCopySeparately(t *testing.T) {
	t.Parallel()
	const otherSecret = "ffeeddccbbaa99887766554433221100ffeeddcc"
	dir := t.TempDir()
	source := testkit.DB{Path: filepath.Join(t.TempDir(), "source.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	a := source.WithSoftKey(t, filepath.Join(dir, "a.kdbx"), otherSecret)
	b := source.WithSoftKey(t, filepath.Join(dir, "b.kdbx"), softkey.FixtureSecret)
	c := testkit.DB{Path: filepath.Join(dir, "c.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{Slots: map[int]string{1: otherSecret, 2: softkey.FixtureSecret}})

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(3), Env: fake.Env()}, "compare", a.Path, "--yubikey", "1", b.Path, "-y=2", c.Path)

	wantOpenedWithKey(t, got, a.Path, b.Path, c.Path)
	calls := fake.Calls(t)
	if len(calls) != 2 {
		t.Fatalf("ykman ran %d times: %v", len(calls), calls)
	}
	if want := []string{"otp", "calculate", "1", challengeOf(t, a.Path)}; !slices.Equal(calls[0].Args, want) {
		t.Errorf("first call %q, want %q", calls[0].Args, want)
	}
	if want := []string{"otp", "calculate", "2", challengeOf(t, b.Path)}; !slices.Equal(calls[1].Args, want) {
		t.Errorf("second call %q, want %q", calls[1].Args, want)
	}
}

// If the hint came after the call, the person would look at a program that seems to hang. The
// fake holds its answer until the file exists, the way a key waits for a touch.
func TestTheHintToTouchComesBeforeTheKeyWaits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.SoftHMAC(t, filepath.Join(dir, "a.kdbx"))
	touched := filepath.Join(t.TempDir(), "touched")
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{WaitFor: touched})

	process := testkit.StartWithEnv(t, fake.Env(), "compare", db.Path, "-y", "2")
	process.Send(t, db.Password)
	process.WaitForStderr(t, "Торкніться YubiKey")
	if err := os.WriteFile(touched, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got := process.Wait(t)

	wantOpenedWithKey(t, got, db.Path)
	// ykman says the same in English. Only the text of the tool reaches the person.
	if strings.Contains(got.Stderr, "Touch your YubiKey") {
		t.Errorf("the text of ykman came through:\n%s", got.Stderr)
	}
	wantUkrainian(t, got.Stdout, got.Stderr, db.Path, "YubiKey", "Argon2id")
}

const ykmanHint = "потрібна програма ykman від Yubico"

func TestWithoutYkmanThereIsAHintBeforeAnyPassword(t *testing.T) {
	t.Parallel()
	db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "a.kdbx"))
	other := testkit.DB{Path: filepath.Join(t.TempDir(), "b.kdbx"), Password: db.Password}.Import(t, mergeableXML())
	output := filepath.Join(t.TempDir(), "new.kdbx")
	for name, args := range map[string][]string{
		"compare": {"compare", db.Path, "-y", "2"},
		"status":  {"status", db.Path, "-y", "2", other.Path},
		"merge":   {"merge", db.Path, "-y", "2", "--output", output},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: []string{testkit.PathTo(t.TempDir())}}, args...)

			if got.Code != 3 {
				t.Errorf("exit code %d, want 3", got.Code)
			}
			if !strings.Contains(got.Stderr, ykmanHint) || !strings.Contains(got.Stderr, "Встановіть") {
				t.Errorf("no hint:\n%s", got.Stderr)
			}
			if strings.Contains(got.Stderr, "Пароль для") {
				t.Errorf("it asked for a password first:\n%s", got.Stderr)
			}
			wantUkrainian(t, got.Stdout, got.Stderr, db.Path, output, ykmanURL, "ykman", "YubiKey", "PATH", "Yubico", "Manager")
		})
	}
}

const ykmanURL = "https://docs.yubico.com/software/yubikey/tools/ykman/Install_ykman.html"

func TestWithoutYkmanAnAnswerAboutTheSlotGetsTheHintToo(t *testing.T) {
	t.Parallel()
	db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "a.kdbx"))

	got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n\n\n2\n", Env: []string{testkit.PathTo(t.TempDir())}}, "compare", db.Path)

	if got.Code != 3 || !strings.Contains(got.Stderr, ykmanHint) {
		t.Errorf("exit code %d:\n%s", got.Code, got.Stderr)
	}
}

// Byte-identical files give an answer with no password, so they need no key and no ykman.
func TestStatusOfTheSameBytesNeedsNoYkman(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := testkit.SoftHMAC(t, filepath.Join(dir, "a.kdbx"))
	b := testkit.SoftHMAC(t, filepath.Join(dir, "b.kdbx"))

	got := testkit.RunWith(t, testkit.Options{Env: []string{testkit.PathTo(t.TempDir())}}, "status", a.Path, "-y", "2", b.Path, "-y", "2")

	if got.Code != 0 {
		t.Errorf("exit code %d:\n%s", got.Code, got.Stderr)
	}
}

// The directory of the tool can be a flash drive anyone has written to. Windows looks in the
// current directory even without a word from PATH.
func TestNoYkmanRunsFromTheCurrentDirectory(t *testing.T) {
	t.Parallel()
	db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "a.kdbx"))
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})
	work := t.TempDir()
	program, err := os.ReadFile(fake.Program())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, filepath.Base(fake.Program())), program, 0o755); err != nil {
		t.Fatal(err)
	}

	got := testkit.RunWith(t, testkit.Options{
		Stdin: db.Password + "\n",
		Dir:   work,
		Env:   append(fake.PlanEnv(), "PATH=."),
	}, "compare", db.Path, "-y", "2")

	if got.Code != 3 || !strings.Contains(got.Stderr, ykmanHint) {
		t.Errorf("exit code %d:\n%s", got.Code, got.Stderr)
	}
	if calls := fake.Calls(t); len(calls) != 0 {
		t.Errorf("the program from the current directory ran %d times", len(calls))
	}
}

func TestAKDBX3CopyWithAYubiKeyIsRefused(t *testing.T) {
	t.Parallel()
	db := testkit.NewKDBX3(t, filepath.Join(t.TempDir(), "k3.kdbx"), testkit.Password)
	output := filepath.Join(t.TempDir(), "new.kdbx")
	for name, tc := range map[string]struct {
		stdin string
		args  []string
	}{
		"compare":              {db.Password + "\n", []string{"compare", db.Path, "-y", "2"}},
		"status":               {db.Password + "\n", []string{"status", db.Path, "-y", "2", db.Path}},
		"merge":                {db.Password + "\n", []string{"merge", db.Path, "-y", "2", "--output", output}},
		"an answer to a retry": {"хибний\n" + db.Password + "\n\n2\n", []string{"compare", db.Path}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

			got := testkit.RunWith(t, testkit.Options{Stdin: tc.stdin, Env: fake.Env()}, tc.args...)

			if got.Code != 3 {
				t.Errorf("exit code %d, want 3\n%s", got.Code, got.Stderr)
			}
			if !strings.Contains(got.Stderr, "це формат KDBX 3") {
				t.Errorf("no clear error:\n%s", got.Stderr)
			}
			if calls := fake.Calls(t); len(calls) != 0 {
				t.Errorf("ykman ran %d times", len(calls))
			}
			if name != "an answer to a retry" && strings.Contains(got.Stderr, "Пароль для") {
				t.Errorf("it asked for a password first:\n%s", got.Stderr)
			}
			wantUkrainian(t, got.Stdout, got.Stderr, db.Path, output, "YubiKey")
		})
	}
}

func TestAKDBX3CopyWithoutAKeyStillOpensBesideACopyWithAKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	k3 := testkit.NewKDBX3(t, filepath.Join(dir, "k3.kdbx"), testkit.Password)
	db := testkit.SoftHMAC(t, filepath.Join(dir, "a.kdbx"))
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "compare", k3.Path, db.Path, "-y", "2")

	wantOpenedWithKey(t, got, k3.Path, db.Path)
}

func TestABadSlotOptionStopsBeforeAnyPassword(t *testing.T) {
	t.Parallel()
	db := testkit.SoftHMAC(t, filepath.Join(t.TempDir(), "a.kdbx"))
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a slot that is not there": {[]string{"compare", db.Path, "-y", "3"}, "1 або 2"},
		"zero":                     {[]string{"compare", db.Path, "-y", "0"}, "1 або 2"},
		"a word":                   {[]string{"compare", db.Path, "--yubikey", "другий"}, "1 або 2"},
		"a serial as in keepassxc": {[]string{"compare", db.Path, "--yubikey=2:7370001"}, "1 або 2"},
		"an empty value":           {[]string{"compare", db.Path, "--yubikey="}, "1 або 2"},
		"no value":                 {[]string{"compare", db.Path, "-y"}, "потребує значення"},
		"no file before it":        {[]string{"compare", "-y", "2", db.Path}, "файла перед нею немає"},
		"merge takes the value":    {[]string{"merge", db.Path, "-y", "--output", "new.kdbx"}, "1 або 2"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

			got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: fake.Env()}, tc.args...)

			if got.Code != 3 || !strings.Contains(got.Stderr, tc.want) {
				t.Errorf("exit code %d, want 3 and %q:\n%s", got.Code, tc.want, got.Stderr)
			}
			if strings.Contains(got.Stderr, "другий") || strings.Contains(got.Stderr, "7370001") {
				t.Errorf("the value is in the output:\n%s", got.Stderr)
			}
			if calls := fake.Calls(t); len(calls) != 0 {
				t.Errorf("ykman ran %d times", len(calls))
			}
		})
	}
}

func TestMergeTakesTheSlotAnywhereBesideItsFile(t *testing.T) {
	t.Parallel()
	for name, form := range map[string]func(path, output string) []string{
		"before --output": func(path, output string) []string { return []string{"merge", path, "-y", "2", "--output", output} },
		"after --output":  func(path, output string) []string { return []string{"merge", "--output", output, path, "--yubikey=2"} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			db := testkit.SoftHMAC(t, filepath.Join(dir, "a.kdbx"))
			fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

			wantMerged(t, testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: noJudge(fake)}, form(db.Path, filepath.Join(dir, "new.kdbx"))...))
		})
	}
}

func TestStatusAndItsNextCommandKeepTheSlots(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plainA, plainB := divergedPair(t, t.TempDir())
	a := plainA.WithSoftKey(t, filepath.Join(dir, "a.kdbx"), softkey.FixtureSecret)
	b := plainB.WithSoftKey(t, filepath.Join(dir, "b.kdbx"), softkey.FixtureSecret)
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "status", a.Path, "-y", "2", b.Path, "-y", "2")

	if got.Code != 2 {
		t.Fatalf("exit code %d, want 2\n%s\n%s", got.Code, got.Stdout, got.Stderr)
	}
	unquoted := strings.NewReplacer(`"`, "", "'", "").Replace(filepath.ToSlash(got.Stdout))
	for _, path := range []string{a.Path, b.Path} {
		if !strings.Contains(unquoted, filepath.ToSlash(path)+" -y 2 ") {
			t.Errorf("the slot does not stand beside %s:\n%s", path, got.Stdout)
		}
	}
	wantChallenges(t, fake, "2", challengeOf(t, a.Path), challengeOf(t, b.Path))
}

func TestStatusOpensBytesOnceWithASlotGivenBesideAnyOfThem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := testkit.DB{Path: filepath.Join(t.TempDir(), "source.kdbx"), Password: testkit.Password}.Import(t, mergeableXML())
	a := source.WithSoftKey(t, filepath.Join(dir, "a.kdbx"), softkey.FixtureSecret)
	same := filepath.Join(dir, "same.kdbx")
	copyFile(t, a.Path, same)
	b := source.WithSoftKey(t, filepath.Join(dir, "b.kdbx"), softkey.FixtureSecret)
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: fake.Env()}, "status", a.Path, same, "-y", "2", b.Path, "-y", "2")

	if got.Code != 0 {
		t.Fatalf("exit code %d\n%s\n%s", got.Code, got.Stdout, got.Stderr)
	}
	wantChallenges(t, fake, "2", challengeOf(t, a.Path), challengeOf(t, b.Path))
}

// The newest copy gives the keys of a result. A key belongs to those keys, like a password.
func TestTheKeysOfAResultFollowTheNewestCopy(t *testing.T) {
	t.Parallel()
	t.Run("the newest copy has a key", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		plainA, plainB := divergedPair(t, t.TempDir())
		// a holds the newest change.
		a := withArgon2(t, plainA).WithSoftKey(t, filepath.Join(dir, "a.kdbx"), softkey.FixtureSecret)
		fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})
		output := filepath.Join(dir, "new.kdbx")

		got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(2), Env: noJudge(fake)}, "merge", a.Path, "-y", "2", plainB.Path, "--output", output)

		wantMerged(t, got)
		wantChallenges(t, fake, "2", challengeOf(t, a.Path), challengeOf(t, output))
		if titles := (testkit.DB{Path: output, Password: testkit.Password}).KdbxwebTitles(t, softkey.FixtureSecret); !slices.Contains(titles, "Додане") {
			t.Errorf("kdbxweb sees %q", titles)
		}
	})
	t.Run("the newest copy has none", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		plainA, plainB := divergedPair(t, t.TempDir())
		b := plainB.WithSoftKey(t, filepath.Join(dir, "b.kdbx"), softkey.FixtureSecret)
		fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})
		result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password}

		// The real KeePassXC is the Judge here: this result has no key.
		got := testkit.RunWith(t, testkit.Options{
			Stdin: passwordLines(2),
			Env:   slices.Concat(fake.PlanEnv(), []string{testkit.PathTo(fake.Dir, filepath.Dir(realCLI(t)))}),
		}, "merge", plainA.Path, b.Path, "-y", "2", "--output", result.Path)

		wantJudged(t, got)
		if strings.Contains(got.Stdout, "слот") {
			t.Errorf("it says that the new file needs a key:\n%s", got.Stdout)
		}
		result.CLI(t, "ls")
		wantChallenges(t, fake, "2", challengeOf(t, b.Path))
	})
}

func realCLI(t *testing.T) string {
	t.Helper()
	path, err := testkit.CLIPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// withArgon2 gives a copy kdbxweb can read: of the AES-KDF it knows only the UUID of KDBX 3, and a
// result takes its KDF from the copy.
func withArgon2(t *testing.T, db testkit.DB) testkit.DB {
	t.Helper()
	return db.WithEncryption(t, filepath.Join(t.TempDir(), "argon2.kdbx"), testkit.AES256, testkit.KDFArgon2id)
}

// The composite key has an order: password, key file, response. kdbxweb has its own code for that.
func TestAKeyFileAndAYubiKeyWorkTogether(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "ключ файл.bin"), 5)
	source := testkit.DB{Path: filepath.Join(t.TempDir(), "source.kdbx"), Password: testkit.Password, KeyFile: key}.Import(t, mergeableXML())
	db := withArgon2(t, source).WithSoftKey(t, filepath.Join(dir, "a.kdbx"), softkey.FixtureSecret)
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})
	result := testkit.DB{Path: filepath.Join(dir, "new.kdbx"), Password: testkit.Password, KeyFile: key}

	got := testkit.RunWith(t, testkit.Options{Stdin: passwordLines(1), Env: noJudge(fake)}, "merge", db.Path, "-k", key, "-y", "2", "--output", result.Path)

	wantMerged(t, got)
	if titles := result.KdbxwebTitles(t, softkey.FixtureSecret); !slices.Contains(titles, "Друге") {
		t.Errorf("kdbxweb sees %q", titles)
	}
}

func TestOnlyTheKeyWithNoPasswordWorks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key := testkit.NewKeyFile(t, filepath.Join(dir, "k.bin"), 6)
	source := testkit.DB{Path: filepath.Join(t.TempDir(), "source.kdbx"), KeyFile: key}.Import(t, mergeableXML())
	db := source.WithSoftKey(t, filepath.Join(dir, "a.kdbx"), softkey.FixtureSecret)
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})

	wantOpenedWithKey(t, testkit.RunWith(t, testkit.Options{Stdin: "\n", Env: fake.Env()}, "compare", db.Path, "-k", key, "-y", "2"), db.Path)
}

func TestNoSecretReachesYkmanOrTheOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.SoftHMAC(t, filepath.Join(dir, "a.kdbx"))
	fake := testkit.NewFakeYkman(t, testkit.YkmanPlan{})
	output := filepath.Join(dir, "new.kdbx")

	got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: noJudge(fake)}, "merge", db.Path, "-y", "2", "--output", output)

	wantMerged(t, got)
	calls := fake.Calls(t)
	if len(calls) != 2 {
		t.Fatalf("ykman ran %d times", len(calls))
	}
	for _, call := range calls {
		for _, word := range slices.Concat(call.Args, call.Env) {
			if strings.Contains(word, db.Password) {
				t.Errorf("ykman got the password in %q", word)
			}
		}
		challenge, err := hex.DecodeString(call.Args[3])
		if err != nil {
			t.Fatal(err)
		}
		response := softkey.Respond(softkey.FixtureSecret, challenge)
		if strings.Contains(strings.ToLower(got.Stdout+got.Stderr), hex.EncodeToString(response)) {
			t.Error("the output holds a response of the key")
		}
		wantNoFileHolds(t, dir, hex.EncodeToString(response))
		wantNoFileHolds(t, dir, string(response))
	}
}

func wantNoFileHolds(t *testing.T, dir, text string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), text) {
			t.Errorf("%s holds a response of the key", entry.Name())
		}
	}
}

func TestTheRepositoryHoldsNoCodeInPython(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".claude") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".py") {
			t.Errorf("code in Python: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A person who misses a touch sees a failure of KeePassXC, and that alone would point the wrong way.
func TestAStopOfTheJudgeWithAYubiKeySpeaksOfTheTouch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := softKeyCopy(t, dir, "a.kdbx", mergeableXML())
	for name, tc := range map[string]struct {
		keepassxc testkit.FakePlan
		ykman     testkit.YkmanPlan
	}{
		"no touch for keepassxc-cli": {keepassxc: testkit.FakePlan{FailCommand: "attachment-export"}},
		// The third call asks about the copy that KeePassXC saved.
		"no touch for the saved copy": {ykman: testkit.YkmanPlan{FailCalls: []int{3}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ykman := testkit.NewFakeYkman(t, tc.ykman)
			_, env := withJudgeOfSoftKey(t, ykman, tc.keepassxc)
			output := filepath.Join(t.TempDir(), "new.kdbx")

			got := testkit.RunWith(t, testkit.Options{Stdin: db.Password + "\n", Env: env}, "merge", db.Path, "-y", "2", "--output", output)

			wantStopped(t, got, output)
			for _, want := range []string{"не отримав дотику вчасно", "Справжній KeePassXC не зміг прочитати результат"} {
				if !strings.Contains(got.Stderr, want) {
					t.Errorf("no %q in:\n%s", want, got.Stderr)
				}
			}
			wantUkrainian(t, got.Stdout, got.Stderr, db.Path, output, "YubiKey", "PATH")
		})
	}
}

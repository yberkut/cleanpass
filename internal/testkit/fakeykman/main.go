// Command fakeykman stands in for ykman of Yubico in the tests. It takes only the form Yubico
// documents, `otp calculate {1|2} <hex>`, and computes a real HMAC-SHA1 with a known secret.
//
// What it prints follows the source of ykman, not a run: ykman was never run for this project.
// The response is one line of hex on stdout, the prompt to touch goes to stderr, and a failure
// is exit code 1 with a message on stderr. A challenge shorter than 64 bytes is padded the way
// yubikit does it.
//
// Only tests run it, and only with synthetic secrets.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"time"

	"github.com/yberkut/cleanpass/internal/testkit/softkey"
)

// Plan is YkmanPlan of the test kit.
type Plan struct {
	Slots     map[string]string
	Output    *string
	FailCalls []int
	WaitFor   string
}

// Call is one line of the log.
type Call struct {
	Args []string
	Env  []string
}

const (
	challengeSize = 64
	exitFailure   = 1
	exitUsage     = 2
)

func main() {
	var plan Plan
	if err := json.Unmarshal([]byte(os.Getenv("FAKE_YKMAN_PLAN")), &plan); err != nil {
		fatal(err)
	}
	number := bytes.Count(earlierCalls(), []byte{'\n'}) + 1
	if err := appendToLog(&Call{Args: os.Args[1:], Env: os.Environ()}); err != nil {
		fatal(err)
	}
	os.Exit(run(&plan, number))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fake ykman:", err)
	os.Exit(90)
}

func run(plan *Plan, number int) int {
	args := os.Args[1:]
	if len(args) != 4 || args[0] != "otp" || args[1] != "calculate" || (args[2] != "1" && args[2] != "2") {
		fmt.Fprintln(os.Stderr, "Usage: ykman otp calculate [OPTIONS] {1|2} [CHALLENGE]")
		return exitUsage
	}
	secret, configured := plan.Slots[args[2]]
	if !configured {
		fmt.Fprintln(os.Stderr, "ERROR: Cannot perform challenge-response on an empty slot.")
		return exitFailure
	}
	challenge, err := hex.DecodeString(args[3])
	if err != nil || len(challenge) > challengeSize {
		fmt.Fprintln(os.Stderr, "ERROR: bad challenge")
		return exitFailure
	}
	fmt.Fprintln(os.Stderr, "Touch your YubiKey...")
	waitFor(plan.WaitFor)
	if slices.Contains(plan.FailCalls, number) {
		fmt.Fprintln(os.Stderr, "ERROR: Failed to write to the YubiKey. Make sure the device does not have restricted access.")
		return exitFailure
	}
	if plan.Output != nil {
		fmt.Print(*plan.Output)
		return 0
	}
	fmt.Print(hex.EncodeToString(softkey.Respond(secret, padded(challenge))), lineEnd())
	return 0
}

// padded does what yubikit does with a short challenge: a byte other than the last one fills it up.
func padded(challenge []byte) []byte {
	filler := byte(0)
	if bytes.HasSuffix(challenge, []byte{0}) {
		filler = 1
	}
	return append(challenge, bytes.Repeat([]byte{filler}, challengeSize-len(challenge))...)
}

// lineEnd is what the text stream of Python writes.
func lineEnd() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

func waitFor(path string) {
	if path == "" {
		return
	}
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
	fatal(errors.New("nothing came to wait for"))
}

func earlierCalls() []byte {
	content, err := os.ReadFile(os.Getenv("FAKE_YKMAN_LOG"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fatal(err)
	}
	return content
}

func appendToLog(call *Call) error {
	line, err := json.Marshal(call)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(os.Getenv("FAKE_YKMAN_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(line, '\n'))
	return err
}

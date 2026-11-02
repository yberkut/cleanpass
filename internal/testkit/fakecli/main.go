// Command fakecli stands in for keepassxc-cli in the tests of the KeePassXC Judge. By default it
// passes each call on to the real program. A plan makes it lie in one chosen way, so a test can
// see the Judge catch that lie. It also writes down how it was called.
//
// Only tests run it, and only with synthetic databases.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/testkit/softkey"
)

// Plan says how to lie. The test kit has the same type and passes it in PlanVariable.
type Plan struct {
	Version         string
	VersionFails    bool
	FailCommand     string
	ExportOld       string
	ExportNew       string
	ExportText      *string
	AttachmentName  string
	AttachmentBytes string
	SwapAfterMkdir  string
	WatchDirs       []string
	WatchFor        []string
	YubiKeySecret   string
}

// Call is one line of the log.
type Call struct {
	Args  []string
	Env   []string
	Stdin string
	// Lied says that the plan changed what this call gave.
	Lied bool
	// ClearText names the watched files that hold a watched text when the call ends.
	ClearText []string
	// forReal is what the real program gets. It differs from Args when it works on a twin.
	forReal []string
}

func main() {
	var plan Plan
	if err := json.Unmarshal([]byte(os.Getenv("FAKE_CLI_PLAN")), &plan); err != nil {
		fatal(err)
	}
	call := Call{Args: os.Args[1:], Env: os.Environ()}
	code := run(&plan, &call)
	for _, dir := range plan.WatchDirs {
		call.ClearText = append(call.ClearText, holding(dir, plan.WatchFor)...)
	}
	if err := appendToLog(&call); err != nil {
		fatal(err)
	}
	os.Exit(code)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fake keepassxc-cli:", err)
	os.Exit(90)
}

func run(plan *Plan, call *Call) int {
	if len(call.Args) == 0 {
		return 91
	}
	call.forReal = call.Args
	command := call.Args[0]
	if command == "--version" {
		if plan.VersionFails {
			call.Lied = true
			return 1
		}
		if plan.Version != "" {
			call.Lied = true
			fmt.Println(plan.Version)
			return 0
		}
		return passOn(call, os.Stdout)
	}
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		fatal(err)
	}
	call.Stdin = string(stdin)
	if command == plan.FailCommand {
		call.Lied = true
		return 1
	}
	if plan.YubiKeySecret != "" && slices.Contains(call.Args, slotOption) {
		return onTwin(plan, call)
	}
	return act(plan, call)
}

func act(plan *Plan, call *Call) int {
	switch call.Args[0] {
	case "export":
		return export(plan, call)
	case "attachment-export":
		if plan.AttachmentBytes != "" && slices.Contains(call.Args, plan.AttachmentName) {
			call.Lied = true
			return writeTo(call.Args[len(call.Args)-1], plan.AttachmentBytes)
		}
	case "mkdir":
		code := passOn(call, os.Stdout)
		if code == 0 && plan.SwapAfterMkdir != "" {
			call.Lied = true
			swap(plan.SwapAfterMkdir, databaseOf(call.Args))
		}
		return code
	}
	return passOn(call, os.Stdout)
}

// slotOption is the option of the slot, in the form the Judge writes it.
const slotOption = "--yubikey"

// onTwin runs a call on a copy of the file that opens without the key.
func onTwin(plan *Plan, call *Call) int {
	at := slices.Index(call.Args, slotOption)
	slot, err := strconv.Atoi(call.Args[at+1])
	if err != nil {
		fatal(err)
	}
	withoutKey := gateway.Credentials{Password: []byte(strings.TrimSuffix(call.Stdin, "\n"))}
	withKey := withoutKey
	withKey.YubiKey = softkey.Key{Number: slot, Secret: plan.YubiKeySecret}
	database := databaseOf(call.Args)

	dir, err := os.MkdirTemp("", "fake-keepassxc-twin-")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(dir)
	twin := filepath.Join(dir, "twin.kdbx")
	relock(database, withKey, twin, withoutKey)

	call.forReal = slices.Delete(slices.Clone(call.Args), at, at+2)
	call.forReal[slices.Index(call.forReal, "--")+1] = twin
	code := act(plan, call)
	if code == 0 && call.Args[0] == "mkdir" {
		relock(twin, withoutKey, database, withKey)
	}
	return code
}

// relock reads a file with its keys and writes what it holds with other keys and a new salt.
func relock(from string, fromKeys gateway.Credentials, to string, toKeys gateway.Credentials) {
	snap, err := gateway.Open(from, fromKeys)
	if err != nil {
		fatal(err)
	}
	file, err := os.Create(to)
	if err != nil {
		fatal(err)
	}
	defer file.Close()
	if err := gateway.Write(file, snap, snap.Format, toKeys); err != nil {
		fatal(err)
	}
}

func export(plan *Plan, call *Call) int {
	if plan.ExportText != nil {
		call.Lied = true
		fmt.Print(*plan.ExportText)
		return 0
	}
	var output bytes.Buffer
	code := passOn(call, &output)
	text := output.String()
	if plan.ExportOld != "" && strings.Contains(text, plan.ExportOld) {
		call.Lied = true
		text = strings.Replace(text, plan.ExportOld, plan.ExportNew, 1)
	}
	if _, err := io.WriteString(os.Stdout, text); err != nil {
		fatal(err)
	}
	return code
}

// passOn runs the real program with the same arguments and the same stdin.
func passOn(call *Call, stdout io.Writer) int {
	program := exec.Command(os.Getenv("FAKE_CLI_REAL"), call.forReal...)
	program.Stdin = strings.NewReader(call.Stdin)
	// On systems where an attachment comes through the standard output, it must be this stream.
	program.Stdout = stdout
	program.Stderr = os.Stderr
	err := program.Run()
	if exit, isExit := err.(*exec.ExitError); isExit {
		return exit.ExitCode()
	}
	if err != nil {
		fatal(err)
	}
	return 0
}

func writeTo(path, content string) int {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		fatal(err)
	}
	defer file.Close()
	if _, err := io.WriteString(file, content); err != nil {
		fatal(err)
	}
	return 0
}

func swap(from, to string) {
	content, err := os.ReadFile(from)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(to, content, 0o600); err != nil {
		fatal(err)
	}
}

// databaseOf finds the path of the database: the first word after --.
func databaseOf(args []string) string {
	at := slices.Index(args, "--")
	if at < 0 || at+1 == len(args) {
		fatal(fmt.Errorf("no database in %q", args))
	}
	return args[at+1]
}

// holding names the files below dir that hold one of the texts.
func holding(dir string, texts []string) []string {
	var found []string
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, text := range texts {
			if bytes.Contains(content, []byte(text)) {
				found = append(found, path)
				break
			}
		}
		return nil
	})
	return found
}

func appendToLog(call *Call) error {
	line, err := json.Marshal(call)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(os.Getenv("FAKE_CLI_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(line, '\n'))
	return err
}

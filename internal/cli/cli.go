// Package cli parses the command line and runs the commands. Each command lives in
// its own file and registers itself in init.
package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/yberkut/cleanpass/internal/challenge"
	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/interrupt"
	"github.com/yberkut/cleanpass/internal/session"
	"github.com/yberkut/cleanpass/internal/text"
)

// Exit codes. The status command adds its own in the range below exitError.
const (
	exitOK    = 0
	exitError = 3
)

// Env is what a command gets from the process.
type Env struct {
	Stdin  *os.File
	Stdout io.Writer
	Stderr io.Writer
}

// command runs with the arguments after its name and returns the exit code.
type command func(env Env, args []string) int

var commands = map[string]command{}

func register(name string, run command) { commands[name] = run }

// Run runs the tool and returns the exit code of the process.
func Run(args []string, env Env) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, text.Usage)
		return exitError
	}
	if name := args[0]; name == "-h" || name == "--help" || name == "help" {
		fmt.Fprint(env.Stdout, text.Usage)
		return exitOK
	}
	run, ok := commands[args[0]]
	if !ok {
		return fail(env, text.UnknownCommand(args[0]))
	}
	// The steps go from the last one added, so these words come after all the others.
	interrupt.Guard(func() {
		fmt.Fprintln(env.Stderr)
		fmt.Fprintln(env.Stderr, text.Interrupted)
	})
	code := run(env, args[1:])
	interrupt.Finish()
	return code
}

func fail(env Env, message string) int {
	fmt.Fprintln(env.Stderr, message)
	return exitError
}

// parseCopies reads the input files of a command. A per-copy option applies to the
// file before it: a.kdbx --key-file a.key b.kdbx. After -- everything is a file.
func parseCopies(args []string) ([]session.Copy, string) {
	var copies []session.Copy
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			for _, path := range args[i+1:] {
				copies = append(copies, session.Copy{Path: path})
			}
			break
		}
		if !strings.HasPrefix(arg, "-") {
			copies = append(copies, session.Copy{Path: arg})
			continue
		}
		// Only the name goes into messages: the value could be anything.
		name, value, hasValue := strings.Cut(arg, "=")
		if !takesAValue(name) {
			return nil, text.UnknownOption(name)
		}
		if len(copies) == 0 {
			return nil, text.OptionNeedsFile(name)
		}
		if !hasValue {
			if i+1 == len(args) {
				return nil, text.OptionNeedsValue(name)
			}
			i++
			value = args[i]
		}
		current := &copies[len(copies)-1]
		if isKeyFileOption(name) {
			current.KeyFile = value
			continue
		}
		slot, ok := challenge.ParseSlot(value)
		if !ok {
			return nil, text.YubiKeySlotInvalid(name)
		}
		current.Slot = slot
	}
	if len(copies) == 0 {
		return nil, text.NoFiles
	}
	return copies, ""
}

func isKeyFileOption(name string) bool { return name == "-k" || name == "--key-file" }

// takesAValue names the options of a copy. Each one takes a value, and the word after it is that
// value, whatever it looks like.
func takesAValue(name string) bool {
	return isKeyFileOption(name) || name == "-y" || name == "--yubikey"
}

// checkCopies finds what is wrong with the files before anyone types a password.
func checkCopies(copies []session.Copy) string {
	for _, c := range copies {
		if err := gateway.Check(c.Path); err != nil {
			return describeFileError(c.Path, err)
		}
		if c.Slot != 0 {
			if err := gateway.CheckYubiKey(c.Path); err != nil {
				return describeFileError(c.Path, err)
			}
		}
		if c.KeyFile != "" {
			if err := gateway.CheckKeyFile(c.KeyFile); err != nil {
				return text.KeyFileUnreadable(c.KeyFile)
			}
		}
	}
	return ""
}

// describeFileError gives the text for the human about an error with a copy.
func describeFileError(path string, err error) string {
	switch {
	case errors.Is(err, session.ErrEchoingTerminal):
		return text.TerminalShowsPasswords
	case errors.Is(err, session.ErrNoInput):
		return text.NoPasswordGiven(path)
	case errors.Is(err, gateway.ErrNotKDBX):
		return text.NotADatabase(path)
	case errors.Is(err, gateway.ErrKDFUnknown):
		return text.KDFUnknown(path)
	case errors.Is(err, gateway.ErrKDFVersion):
		return text.KDFVersion(path)
	case errors.Is(err, gateway.ErrKDFKeyed):
		return text.KDFKeyed(path)
	case errors.Is(err, gateway.ErrKDFLimits):
		return text.KDFLimits(path)
	case errors.Is(err, gateway.ErrEmptyTime):
		return text.EmptyTime(path)
	case errors.Is(err, gateway.ErrYubiKeyKDBX3):
		return text.YubiKeyNeedsKDBX4(path)
	case errors.Is(err, challenge.ErrNoProgram):
		return text.YkmanMissing
	case errors.Is(err, gateway.ErrCorrupt):
		return text.DatabaseDamaged(path)
	case errors.Is(err, fs.ErrNotExist):
		return text.FileNotFound(path)
	}
	return text.FileUnreadable(path)
}

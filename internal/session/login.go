// Package session is the Session module: it holds the dialog with the person.
// Everything the tool reads from stdin goes through one Session.
package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"golang.org/x/term"

	"github.com/yberkut/cleanpass/internal/challenge"
	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/snapshot"
	"github.com/yberkut/cleanpass/internal/text"
)

// ErrNoInput means stdin ended before the person gave an answer.
var ErrNoInput = errors.New("stdin ended")

// exitInterrupted is the exit code after Ctrl+C during hidden input.
const exitInterrupted = 130

// Copy is one input file with what the command line says about it.
type Copy struct {
	Path    string
	KeyFile string
	// Slot is the slot of a YubiKey, or 0 when the copy has none.
	Slot int
}

// Session reads from stdin and writes questions to prompts.
type Session struct {
	stdin   *os.File
	prompts io.Writer
	// typed is the last password the person typed in this run.
	typed []byte
	// responder keeps the responses of a key for the whole run, so that a retry needs no new touch.
	responder *challenge.Responder
}

// New starts a dialog. Questions go to prompts, so that stdout holds only the result.
func New(stdin *os.File, prompts io.Writer) *Session {
	s := &Session{stdin: stdin, prompts: prompts}
	s.responder = challenge.New(func(slot int) { s.say(text.TouchYubiKey(slot)) })
	return s
}

// Open asks for the keys of a copy until they fit, and gives its Snapshot.
func (s *Session) Open(c Copy) (*snapshot.Snapshot, error) {
	snap, _, err := s.OpenWithKeys(c)
	return snap, err
}

// OpenWithKeys is Open that also gives the keys that fit, so that a result can have them.
//
// The first try asks only for the password. A retry also asks for the key file and the slot of a
// YubiKey, so that a database with those needs no restart. An answer counts for that try only:
// Enter goes back to what the command line said.
func (s *Session) OpenWithKeys(c Copy) (*snapshot.Snapshot, gateway.Credentials, error) {
	if c.Slot != 0 {
		if err := s.responder.Ready(); err != nil {
			return nil, gateway.Credentials{}, err
		}
	}
	for retry := false; ; retry = true {
		passwords, err := s.askPassword(c.Path)
		if err != nil {
			return nil, gateway.Credentials{}, err
		}
		keyFile, slot := c.KeyFile, c.Slot
		if retry {
			if keyFile, err = s.askKeyFile(c.Path, c.KeyFile); err != nil {
				return nil, gateway.Credentials{}, err
			}
			if slot, err = s.askSlot(c.Path, c.Slot); err != nil {
				return nil, gateway.Credentials{}, err
			}
		}
		snap, keys, err := s.tryPasswords(c.Path, passwords, keyFile, slot)
		switch {
		case errors.Is(err, gateway.ErrWrongCredentials):
			s.say(wrongCredentials(c.Path, slot))
		case errors.Is(err, gateway.ErrKeyFile):
			s.say(text.KeyFileUnreadable(keyFile))
		case errors.Is(err, challenge.ErrNoResponse):
			s.say(text.YubiKeyNoResponse(slot))
		default:
			return snap, keys, err
		}
	}
}

func wrongCredentials(path string, slot int) string {
	if slot != 0 {
		return text.WrongCredentialsWithYubiKey(path, slot)
	}
	return text.WrongCredentials(path)
}

// tryPasswords stops at the first password that fits.
func (s *Session) tryPasswords(path string, passwords [][]byte, keyFile string, slot int) (*snapshot.Snapshot, gateway.Credentials, error) {
	var err error
	for _, password := range passwords {
		keys := gateway.Credentials{Password: password, KeyFile: keyFile}
		if slot != 0 {
			keys.YubiKey = s.responder.Key(slot)
		}
		var snap *snapshot.Snapshot
		snap, err = gateway.Open(path, keys)
		if !errors.Is(err, gateway.ErrWrongCredentials) {
			return snap, keys, err
		}
	}
	return nil, gateway.Credentials{}, err
}

func (s *Session) say(line string) {
	fmt.Fprintln(s.prompts, line)
}

// askPassword returns the passwords to try. A typed line is the only one. Enter stands
// for three things the person cannot tell apart at a prompt: the password typed before,
// no password at all, and the empty password, which is a different key.
func (s *Session) askPassword(path string) ([][]byte, error) {
	fmt.Fprint(s.prompts, text.PasswordPrompt(path, s.typed != nil))
	line, err := s.readSecret()
	if err != nil {
		return nil, err
	}
	if len(line) > 0 {
		s.typed = line
		return [][]byte{line}, nil
	}
	if s.typed != nil {
		return [][]byte{s.typed, nil, {}}, nil
	}
	return [][]byte{nil, {}}, nil
}

func (s *Session) askKeyFile(path, current string) (string, error) {
	fmt.Fprint(s.prompts, text.KeyFilePrompt(path, current))
	line, err := readLine(s.stdin)
	if !s.isTerminal() {
		fmt.Fprintln(s.prompts) // no Enter from a person ended the line of the prompt
	}
	if errors.Is(err, ErrNoInput) {
		return current, nil
	}
	if err != nil {
		return "", err
	}
	if len(line) == 0 {
		return current, nil
	}
	return string(line), nil
}

// askSlot asks until the answer is a slot. The message about a bad answer does not repeat it: a
// person who lost count of the questions may have typed a password here.
func (s *Session) askSlot(path string, current int) (int, error) {
	for {
		fmt.Fprint(s.prompts, text.YubiKeySlotPrompt(path, current))
		line, err := readLine(s.stdin)
		if !s.isTerminal() {
			fmt.Fprintln(s.prompts) // no Enter from a person ended the line of the prompt
		}
		if errors.Is(err, ErrNoInput) {
			return current, nil
		}
		if err != nil {
			return 0, err
		}
		if len(line) == 0 {
			return current, nil
		}
		if slot, ok := challenge.ParseSlot(string(line)); ok {
			return slot, nil
		}
		s.say(text.YubiKeySlotAnswerInvalid)
	}
}

func (s *Session) isTerminal() bool { return term.IsTerminal(int(s.stdin.Fd())) }

// readSecret reads a line that must not show on the screen. It ends the line of the
// prompt itself, because neither hidden input nor a pipe does that.
func (s *Session) readSecret() ([]byte, error) {
	defer fmt.Fprintln(s.prompts)
	if !s.isTerminal() {
		return readLine(s.stdin)
	}
	line, err := readHidden(int(s.stdin.Fd()))
	if errors.Is(err, io.EOF) {
		return nil, ErrNoInput
	}
	return line, err
}

// readHidden reads a line without echo. It gives the terminal its echo back on Ctrl+C,
// because otherwise the shell would stay blind after the process ends.
func readHidden(fd int) ([]byte, error) {
	state, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}
	interrupt := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)
	defer close(done)
	go func() {
		select {
		case <-interrupt:
			_ = term.Restore(fd, state)
			os.Exit(exitInterrupted)
		case <-done:
		}
	}()
	return term.ReadPassword(fd)
}

// readLine reads one line, one byte at a time, so that nothing stays in a buffer
// when the next read goes straight to the terminal. It drops the line end. The last
// line may come without one.
func readLine(r io.Reader) ([]byte, error) {
	line := []byte{}
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				return trimCR(line), nil
			}
			line = append(line, one[0])
		}
		if errors.Is(err, io.EOF) {
			if len(line) == 0 {
				return nil, ErrNoInput
			}
			return trimCR(line), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func trimCR(line []byte) []byte {
	if len(line) > 0 && line[len(line)-1] == '\r' {
		return line[:len(line)-1]
	}
	return line
}

package session

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/yberkut/cleanpass/internal/challenge"
	"github.com/yberkut/cleanpass/internal/gateway"
	"github.com/yberkut/cleanpass/internal/report"
	"github.com/yberkut/cleanpass/internal/text"
)

// KeysChoice says what opens the result.
type KeysChoice struct {
	// FromCopy is the position of the copy that gives the keys, the cipher and the KDF.
	FromCopy int
	// New holds the keys the person set, if IsNew. The cipher and the KDF still come from FromCopy.
	New   gateway.Credentials
	IsNew bool
}

// ChooseKeys asks what opens the result. With no answer it is what opens the newest copy, and so
// it is after the end of the input and after the person has said to stop asking. Only one thing
// fails: new keys the person asked for and did not finish, because a file with other keys than
// the person meant is worse than no file.
func (s *Session) ChooseKeys(paths []string, newest int) (KeysChoice, error) {
	choice := KeysChoice{FromCopy: newest}
	if s.finished {
		return choice, nil
	}
	_ = report.WriteKeysQuestion(s.prompts, paths, newest)
	choices := len(paths) + 1
	for {
		fmt.Fprint(s.prompts, text.AnswerPrompt(choices, false))
		line, ok := s.readAnswer()
		switch {
		case !ok || line == "":
			return choice, nil
		case text.IsStopWord(line):
			s.finished = true
			return choice, nil
		}
		number, err := strconv.Atoi(line)
		switch {
		case err != nil || number < 1 || number > choices:
			s.say(text.AnswerNotUnderstood(choices))
		case number < choices:
			return KeysChoice{FromCopy: number - 1}, nil
		default:
			keys, err := s.askNewKeys()
			if err != nil {
				return KeysChoice{}, err
			}
			return KeysChoice{FromCopy: newest, New: keys, IsNew: true}, nil
		}
	}
}

// askNewKeys asks until the keys protect something: a file with no password, no key file and no
// YubiKey would open for anyone.
func (s *Session) askNewKeys() (gateway.Credentials, error) {
	for {
		password, err := s.askNewPassword()
		if err != nil {
			return gateway.Credentials{}, err
		}
		keyFile, err := s.askNewKeyFile()
		if err != nil {
			return gateway.Credentials{}, err
		}
		slot, err := s.askNewSlot()
		if err != nil {
			return gateway.Credentials{}, err
		}
		if password == nil && keyFile == "" && slot == 0 {
			s.say(text.NewKeysEmpty)
			continue
		}
		keys := gateway.Credentials{Password: password, KeyFile: keyFile}
		if slot != 0 {
			keys.YubiKey = s.responder.Key(slot)
		}
		return keys, nil
	}
}

// askNewPassword asks twice, because a slip in hidden typing would lock the person out of the
// result. Enter means no password at all.
func (s *Session) askNewPassword() ([]byte, error) {
	for {
		fmt.Fprint(s.prompts, text.NewKeysPasswordPrompt)
		password, err := s.readSecret()
		if err != nil {
			return nil, err
		}
		if len(password) == 0 {
			return nil, nil
		}
		fmt.Fprint(s.prompts, text.NewKeysRepeatPrompt)
		repeated, err := s.readSecret()
		if err != nil {
			return nil, err
		}
		if bytes.Equal(password, repeated) {
			return password, nil
		}
		s.say(text.NewKeysDifferent)
	}
}

func (s *Session) askNewKeyFile() (string, error) {
	for {
		fmt.Fprint(s.prompts, text.NewKeysKeyFilePrompt)
		line, err := s.readPlainLine()
		if err != nil {
			return "", err
		}
		if len(line) == 0 {
			return "", nil
		}
		path := string(line)
		if gateway.CheckKeyFile(path) == nil {
			return path, nil
		}
		s.say(text.KeyFileUnreadable(path))
	}
}

// askNewSlot gives 0 for no YubiKey. It asks again when the program for a key is not there, because
// the write would stop on that only after all the answers.
func (s *Session) askNewSlot() (int, error) {
	for {
		fmt.Fprint(s.prompts, text.NewKeysSlotPrompt)
		line, err := s.readPlainLine()
		if err != nil {
			return 0, err
		}
		if len(line) == 0 {
			return 0, nil
		}
		slot, ok := challenge.ParseSlot(string(line))
		switch {
		case !ok:
			s.say(text.YubiKeySlotAnswerInvalid)
		case s.responder.Ready() != nil:
			s.say(text.YkmanMissing)
		default:
			return slot, nil
		}
	}
}

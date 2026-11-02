// Package challenge is the Challenge Response module: it asks a slot of a YubiKey for the HMAC-SHA1
// response through ykman, the program of Yubico.
//
// The call is `ykman otp calculate {1|2} <challenge in hex>`, as Yubico documents it. The documents
// do not say what the command prints. The source of ykman prints the response as one line of hex
// on stdout, and the prompt to touch and every error on stderr. No real key ever ran this code.
//
// The challenge is the KDF salt from the header of the file, so it is no secret and may stand in
// an argument. The response is a part of the key of the database. It stays in memory, and no
// error, log or file gets it.
package challenge

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// The errors of this package. Their text is for developers.
var (
	// ErrNoProgram means ykman is not on PATH.
	ErrNoProgram = errors.New("ykman not found")
	// ErrNoResponse means ykman ran and gave no usable response: no key, an empty slot, more than
	// one key, or no touch in time. ykman tells which on stderr, in English, so it stays there.
	ErrNoResponse = errors.New("ykman gave no response")
)

const (
	programName = "ykman"
	// challengeSize is what a slot takes. KeePassXC always sends this many bytes.
	challengeSize = 64
	// responseSize is the size of HMAC-SHA1.
	responseSize = 20
	// callTimeout ends a call that hangs. It lies far above the time a key waits for a touch.
	callTimeout = 2 * time.Minute
	// waitDelay is how long the tool waits for the output of a program it has ended.
	waitDelay = 5 * time.Second
)

// ParseSlot reads the number of a slot. A key has two.
func ParseSlot(word string) (int, bool) {
	switch word {
	case "1":
		return 1, true
	case "2":
		return 2, true
	}
	return 0, false
}

// Responder asks the keys for one run of the tool. It remembers each response, so the same
// challenge needs one touch, also for another copy with the same salt.
type Responder struct {
	beforeCall func(slot int)
	responses  map[request][]byte
}

type request struct {
	slot      int
	challenge string
}

// New gives a Responder. beforeCall runs just before each call of ykman that may wait for a touch.
func New(beforeCall func(slot int)) *Responder {
	return &Responder{beforeCall: beforeCall, responses: map[request][]byte{}}
}

// Ready says whether ykman can be asked, before anyone types a password.
func (r *Responder) Ready() error {
	_, err := locate()
	return err
}

// Key gives one slot, in the form the KDBX Gateway needs.
func (r *Responder) Key(slot int) *Key { return &Key{responder: r, slot: slot} }

// Key is one slot of a key.
type Key struct {
	responder *Responder
	slot      int
}

// Slot gives the number of the slot.
func (k *Key) Slot() int { return k.slot }

// Respond gives the response of the slot to the KDF salt of a file. The challenge is the salt with
// PKCS#7 padding up to 64 bytes. By the source of KeePassXC that is what it sends to a key, and
// ykman leaves a challenge of that size as it is. Only a real key can confirm this.
func (k *Key) Respond(salt []byte) ([]byte, error) {
	challenge := padded(salt)
	asked := request{k.slot, string(challenge)}
	if response, known := k.responder.responses[asked]; known {
		return response, nil
	}
	path, err := locate()
	if err != nil {
		return nil, err
	}
	k.responder.beforeCall(k.slot)
	response, err := call(path, k.slot, challenge)
	if err != nil {
		return nil, err
	}
	k.responder.responses[asked] = response
	return response, nil
}

func padded(salt []byte) []byte {
	size := challengeSize - len(salt)
	return append(bytes.Clone(salt), bytes.Repeat([]byte{byte(size)}, size)...)
}

// locate finds the program on PATH. LookPath gives an error for a file that only lies in the
// current directory, and that may be a flash drive anyone has written to.
func locate() (string, error) {
	path, err := exec.LookPath(programName)
	if err != nil {
		return "", ErrNoProgram
	}
	return path, nil
}

func call(path string, slot int, challenge []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "otp", "calculate", fmt.Sprint(slot), hex.EncodeToString(challenge))
	// A launcher of Python can leave a child that holds the output open after a kill.
	cmd.WaitDelay = waitDelay
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: the program failed", ErrNoResponse)
	}
	return parseResponse(stdout.String())
}

// parseResponse takes nothing but the one form of a response, since what stands there could be
// anything. The error does not say what stood there.
func parseResponse(output string) ([]byte, error) {
	response, err := hex.DecodeString(strings.TrimSpace(output))
	if err != nil || len(response) != responseSize {
		return nil, fmt.Errorf("%w: the answer has another form", ErrNoResponse)
	}
	return response, nil
}

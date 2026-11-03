// Package softkey is the slot of a YubiKey in software, for tests only: a real HMAC-SHA1 with a
// known secret. It says nothing about real hardware or about KeePassXC.
package softkey

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
)

// FixtureSecret is the secret of the database that kdbxweb made. It is synthetic.
const FixtureSecret = "00112233445566778899aabbccddeeff00112233"

const challengeSize = 64

// Respond gives the HMAC-SHA1 of the challenge. It takes the whole challenge, the way a slot with
// fixed input does. The fixture from kdbxweb was made this way.
func Respond(secret string, challenge []byte) []byte {
	key, err := hex.DecodeString(secret)
	if err != nil {
		panic(err)
	}
	mac := hmac.New(sha1.New, key)
	mac.Write(challenge)
	return mac.Sum(nil)
}

// ChallengeOf pads a salt by PKCS#7 to the 64 bytes of a challenge.
func ChallengeOf(salt []byte) []byte {
	size := challengeSize - len(salt)
	return append(bytes.Clone(salt), bytes.Repeat([]byte{byte(size)}, size)...)
}

// Key is a slot with a secret. It fits gateway.Credentials, so that a test or a fake can open a
// file with no process and no hardware.
type Key struct {
	Number int
	Secret string
}

// Slot gives the number of the slot.
func (k Key) Slot() int { return k.Number }

// Respond gives the response to the salt of a file.
func (k Key) Respond(salt []byte) ([]byte, error) {
	return Respond(k.Secret, ChallengeOf(salt)), nil
}

package merge

import "crypto/rand"

// Key is the secret key of the content fingerprints of one run of the tool. It lives only
// in memory. The zero Key is not valid.
type Key [32]byte

// NewKey returns a new random key. It is the only code of this package that reads from
// the outside world, and Run does not call it.
func NewKey() Key {
	var key Key
	rand.Read(key[:]) // It never fails: the program crashes if the system cannot give random bytes.
	return key
}

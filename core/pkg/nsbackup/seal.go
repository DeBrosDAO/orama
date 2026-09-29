// Package nsbackup seals a namespace backup to the owner's X25519 public key.
// The cluster is given only that public key. Opening the backup needs the
// private key, which only the owner's machine uses and the cluster never holds.
package nsbackup

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

const (
	magic   = "ORBK"
	version = 1
)

// ErrNotForKey means the private key is wrong or the blob is corrupt.
var ErrNotForKey = errors.New("backup cannot be opened: the private key is wrong, or the file is corrupt or truncated")

// Seal encrypts plaintext for pub. The result does not contain the private key.
func Seal(pub *[32]byte, plaintext []byte) ([]byte, error) {
	if pub == nil {
		return nil, fmt.Errorf("backup public key is required")
	}
	sealed, err := box.SealAnonymous(nil, plaintext, pub, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("seal backup: %w", err)
	}
	out := make([]byte, 0, 5+len(sealed))
	out = append(out, magic...)
	out = append(out, version)
	out = append(out, sealed...)
	return out, nil
}

// Open decrypts a blob produced by Seal. A public key cannot be used here.
func Open(priv *[32]byte, blob []byte) ([]byte, error) {
	if priv == nil {
		return nil, fmt.Errorf("backup private key is required")
	}
	if len(blob) < 5 || string(blob[:4]) != magic || blob[4] != version {
		return nil, fmt.Errorf("not an Orama namespace backup")
	}
	pub, ok := publicFromPrivate(priv)
	if !ok {
		return nil, fmt.Errorf("backup private key is not a valid X25519 scalar")
	}
	plain, ok := box.OpenAnonymous(nil, blob[5:], pub, priv)
	if !ok {
		return nil, ErrNotForKey
	}
	return plain, nil
}

func publicFromPrivate(priv *[32]byte) (*[32]byte, bool) {
	var base, pub [32]byte
	base[0] = 9
	out, err := curve25519.X25519(priv[:], base[:])
	if err != nil || len(out) != 32 {
		return nil, false
	}
	copy(pub[:], out)
	return &pub, true
}

package nsbackup

import (
	"bytes"
	"crypto/rand"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

func TestSealRoundTripAndWrongKey(t *testing.T) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("namespace snapshot v1")
	blob, err := Seal(pub, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := Open(priv, blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("opened %q", got)
	}

	_, other, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(other, blob); err != ErrNotForKey {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestOpenRefusesAPublicKeySizedGuess(t *testing.T) {
	pub, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := Seal(pub, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	// The public key is not a private key. Opening with it must fail.
	if _, err := Open(pub, blob); err != ErrNotForKey {
		t.Fatalf("public key opened the backup: %v", err)
	}
}

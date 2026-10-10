package releasepub

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// The id of a fixed key, computed independently of this package: the SHA-256 of
// the canonical JSON written out by hand.
func TestKeyID_matchesTheCanonicalJSONDigest(t *testing.T) {
	pub := ed25519.PublicKey(bytes.Repeat([]byte{0xab}, ed25519.PublicKeySize))
	canonical := `{"keytype":"ed25519","keyval":{"public":"` + hex.EncodeToString(pub) + `"},"scheme":"ed25519"}`
	sum := sha256.Sum256([]byte(canonical))

	got, err := KeyID(pub)
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(sum[:]) {
		t.Fatalf("KeyID = %s, want %x", got, sum)
	}
}

// The id a client computes for the key in a root (go-tuf) is the id this
// package lists it under.
func TestKeyID_agreesWithGoTUF(t *testing.T) {
	for i := byte(1); i < 5; i++ {
		pub := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{i}, ed25519.SeedSize)).Public().(ed25519.PublicKey)
		key, err := metadata.KeyFromPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		want, err := key.ID()
		if err != nil {
			t.Fatal(err)
		}
		if got, err := KeyID(pub); err != nil || got != want {
			t.Fatalf("seed %d: KeyID = %q, %v; go-tuf says %q", i, got, err, want)
		}
	}
}

func TestKeyID_refusesAKeyOfTheWrongSize(t *testing.T) {
	for _, pub := range []ed25519.PublicKey{nil, {1, 2, 3}, bytes.Repeat([]byte{1}, ed25519.PublicKeySize+1)} {
		if _, err := KeyID(pub); err == nil {
			t.Errorf("a %d-byte key was given an id", len(pub))
		}
	}
}

func TestKeyID_differentKeysHaveDifferentIDs(t *testing.T) {
	a, _ := KeyID(bytes.Repeat([]byte{1}, ed25519.PublicKeySize))
	b, _ := KeyID(bytes.Repeat([]byte{2}, ed25519.PublicKeySize))
	if a == b {
		t.Fatal("two keys share an id")
	}
}

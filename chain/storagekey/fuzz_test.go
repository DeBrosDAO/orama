package storagekey

import (
	"bytes"
	"errors"
	"testing"
)

// FuzzStorageKeyRewrap: applying a slot's keystream twice returns the body; rewrapping from one
// slot to another and back returns the blob; rewrapping a slot to itself changes nothing; two
// slots give two different keystreams; a short seed or a nonce of the wrong width is refused and
// never gets as far as a key.
func FuzzStorageKeyRewrap(f *testing.F) {
	seed, nonce := bytes.Repeat([]byte{1}, MinSeedLen), bytes.Repeat([]byte{2}, DealNonceLen)
	f.Add(seed, nonce, uint32(0), uint32(1), []byte("a private slot body"))
	f.Add(seed[:MinSeedLen-1], nonce, uint32(0), uint32(0), []byte("x"))
	f.Add(seed, nonce[:5], uint32(7), uint32(9), []byte{})
	f.Fuzz(func(t *testing.T, seed, nonce []byte, from, to uint32, body []byte) {
		sealed, err := Apply(seed, nonce, from, body)
		if len(seed) < MinSeedLen {
			if !errors.Is(err, ErrSeed) {
				t.Fatalf("a %d byte seed was accepted (%v)", len(seed), err)
			}
			return
		}
		if len(nonce) != DealNonceLen {
			if err == nil {
				t.Fatalf("a %d byte deal nonce was accepted", len(nonce))
			}
			return
		}
		if err != nil || len(sealed) != len(body) {
			t.Fatalf("Apply: %v, %d bytes for %d", err, len(sealed), len(body))
		}
		if back, err := Apply(seed, nonce, from, sealed); err != nil || !bytes.Equal(back, body) {
			t.Fatalf("applying a slot's keystream twice does not return the body (%v)", err)
		}
		there, err := Rewrap(seed, nonce, from, to, sealed)
		if err != nil {
			t.Fatal(err)
		}
		if again, err := Rewrap(seed, nonce, to, from, there); err != nil || !bytes.Equal(again, sealed) {
			t.Fatalf("rewrapping there and back does not return the blob (%v)", err)
		}
		if direct, _ := Apply(seed, nonce, to, body); !bytes.Equal(direct, there) {
			t.Fatal("rewrapping slot from to slot to differs from sealing the body for slot to")
		}
		if len(body) >= 16 && from != to && bytes.Equal(there, sealed) {
			t.Fatal("two slots share a keystream")
		}
	})
}

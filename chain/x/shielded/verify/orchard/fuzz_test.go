package orchard

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
)

// FuzzOrchardSighash: the sighash is a deterministic function of the chain id, the binding and the
// effecting data only. It ignores everything after that prefix (the proof and the signatures),
// and it changes when the chain id, the binding or any effecting byte changes. A bound sighash is
// never an unbound one.
func FuzzOrchardSighash(f *testing.F) {
	for _, name := range []string{"ironwood-1-action", "ironwood-2-action", "ironwood-unshield"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "orchardffi", "testdata", name+".bundle"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add("orama-localnet-1", []byte(nil), raw, uint16(0), uint8(1))
		f.Add("orama-localnet-1", []byte("signer|target"), raw, uint16(7), uint8(255))
	}
	f.Add("", []byte{}, []byte{1}, uint16(0), uint8(0))
	f.Fuzz(func(t *testing.T, chainID string, binding, b []byte, at uint16, flip uint8) {
		sum, err := Sighash(chainID, binding, b)
		prefix, effErr := bundle.EffectingData(b)
		if (err == nil) != (effErr == nil) {
			if len(chainID) <= maxChainIDLen {
				t.Fatalf("Sighash error %v, EffectingData error %v", err, effErr)
			}
			return
		}
		if err != nil {
			return
		}
		again, _ := Sighash(chainID, bytes.Clone(binding), bytes.Clone(b))
		if again != sum {
			t.Fatal("Sighash is not deterministic")
		}
		if len(b) > len(prefix) {
			mut := bytes.Clone(b)
			mut[len(prefix)+int(at)%(len(b)-len(prefix))] ^= flip | 1
			if other, _ := Sighash(chainID, binding, mut); other != sum {
				t.Fatal("the sighash depends on the proof or the signatures")
			}
		}
		mut := bytes.Clone(b)
		mut[int(at)%len(prefix)] ^= flip | 1
		// Flipping the action count may make the bundle malformed, which is a refusal, not a hash.
		if other, err := Sighash(chainID, binding, mut); err == nil && other == sum {
			t.Fatal("flipping an effecting byte left the sighash unchanged")
		}
		if other, _ := Sighash(chainID+"x", binding, b); other == sum {
			t.Fatal("the sighash does not bind the chain id")
		}
		if other, _ := Sighash(chainID, append(bytes.Clone(binding), 'x'), b); other == sum {
			t.Fatal("the sighash does not bind the binding")
		}
		if len(binding) == 0 {
			if other, _ := Sighash(chainID, []byte{0}, b); other == sum {
				t.Fatal("a bound sighash equals the unbound one")
			}
		}
	})
}

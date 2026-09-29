package orchard

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

const testdataDir = "../../orchardffi/testdata"

var vectorNames = []string{"ironwood-1-action", "ironwood-2-action"}

func loadVector(t testing.TB, name string) (bundle []byte, sighash []byte, chainID string) {
	t.Helper()
	read := func(file string) []byte {
		b, err := os.ReadFile(filepath.Join(testdataDir, file))
		if err != nil {
			t.Fatalf("read vector %s: %v", file, err)
		}
		return b
	}
	return read(name + ".bundle"), read(name + ".sighash"), string(read("chain-id"))
}

// The Rust generator and this package define the sighash separately. They must agree.
func TestSighash_matchesRustGenerator(t *testing.T) {
	for _, name := range vectorNames {
		bundle, want, chainID := loadVector(t, name)
		got, err := Sighash(chainID, bundle)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got[:], want) {
			t.Fatalf("%s: sighash %x, generator wrote %x", name, got, want)
		}
	}
}

func TestSighash_bindsChainAndEffectingData(t *testing.T) {
	bundle, _, chainID := loadVector(t, "ironwood-1-action")
	base, err := Sighash(chainID, bundle)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := Sighash(chainID+"x", bundle)
	if other == base {
		t.Fatal("a different chain id must change the sighash")
	}
	// epk and ciphertext bytes are covered by the sighash, not by the proof.
	for _, at := range []int{1 + 32*4 + 3, 1 + 32*5 + 100, 1 + actionLen - 1, 1 + actionLen + 8} {
		mut := bytes.Clone(bundle)
		mut[at] ^= 1
		got, err := Sighash(chainID, mut)
		if err != nil {
			t.Fatal(err)
		}
		if got == base {
			t.Fatalf("flipping byte %d did not change the sighash", at)
		}
	}
}

func TestSighash_ignoresProofAndSignatures(t *testing.T) {
	bundle, _, chainID := loadVector(t, "ironwood-1-action")
	base, _ := Sighash(chainID, bundle)
	mut := bytes.Clone(bundle)
	mut[len(mut)-1] ^= 1
	mut[1+actionLen+bundleHeaderLen+3+10] ^= 1
	got, _ := Sighash(chainID, mut)
	if got != base {
		t.Fatal("the proof and signatures are not part of the sighash input")
	}
}

func TestSighash_malformedInput(t *testing.T) {
	bundle, _, chainID := loadVector(t, "ironwood-1-action")
	cases := map[string][]byte{
		"empty":               nil,
		"zero actions":        {0},
		"truncated":           bundle[:100],
		"absurd action count": append([]byte{0xff}, bytes.Repeat([]byte{0xff}, 8)...),
		"non-canonical count": {0xfd, 0x01, 0x00},
	}
	for name, in := range cases {
		if _, err := Sighash(chainID, in); !errors.Is(err, verify.ErrMalformed) {
			t.Errorf("%s: got %v, want ErrMalformed", name, err)
		}
	}
	if _, err := Sighash(string(make([]byte, maxChainIDLen+1)), bundle); err == nil {
		t.Error("an oversize chain id must be refused")
	}
}

func TestSighash_emptyChainIDIsHashedAsEmpty(t *testing.T) {
	bundle, _, _ := loadVector(t, "ironwood-1-action")
	a, err := Sighash("", bundle)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Sighash("a", bundle)
	if a == b {
		t.Fatal("chain id must affect the hash")
	}
}

func mustNew(t testing.TB, chainID string) verify.Verifier {
	t.Helper()
	v, err := New(chainID)
	if err != nil {
		t.Fatalf("New(%q): %v", chainID, err)
	}
	return v
}

// A verifier bound to no chain would accept a bundle built for the empty chain id anywhere.
func TestNew_refusesAnEmptyChainID(t *testing.T) {
	v, err := New("")
	if !errors.Is(err, verify.ErrEmptyChainID) || v != nil {
		t.Fatalf("New(\"\") = %v, %v; want ErrEmptyChainID", v, err)
	}
	if v, err := New("orama-test-1"); err != nil || v == nil || v.ID() != VerifierID {
		t.Fatalf("New with a chain id = %v, %v", v, err)
	}
}

// Both verifier builds report the same identity, and the same one twice is not two verifiers.
func TestVerifierID_sameVerifierTwiceIsRefused(t *testing.T) {
	bundle, _, chainID := loadVector(t, "ironwood-1-action")
	v := mustNew(t, chainID)
	if err := verify.Check(bundle, v, v); !errors.Is(err, verify.ErrVerifierNotLinked) {
		t.Fatalf("Check(v, v) = %v, want a fail-closed refusal", err)
	}
	other := mustNew(t, chainID)
	if err := verify.Check(bundle, v, other); !errors.Is(err, verify.ErrDuplicateVerifier) {
		t.Fatalf("two instances of one implementation = %v, want ErrDuplicateVerifier", err)
	}
}

package bundle

import (
	"bytes"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

var vectorNames = []string{"ironwood-1-action", "ironwood-2-action", "ironwood-transfer", "ironwood-unshield"}

// CompactActions reads each action's fields at the offsets of the serialized action: cv, nf, rk,
// cmx, epk, then enc (580) and out (80).
func TestCompactActions_vectors(t *testing.T) {
	for _, name := range vectorNames {
		raw := load(t, name)
		got, err := CompactActions(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		parsed, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != parsed.Actions {
			t.Fatalf("%s: %d compact actions for %d actions", name, len(got), parsed.Actions)
		}
		for i, a := range got {
			at := 1 + i*ActionLen
			if !bytes.Equal(a.Nullifier[:], raw[at+32:at+64]) || a.Nullifier != parsed.Nullifiers[i] {
				t.Errorf("%s action %d: nullifier", name, i)
			}
			if !bytes.Equal(a.Cmx[:], raw[at+96:at+128]) || a.Cmx != parsed.Commitments[i] {
				t.Errorf("%s action %d: cmx", name, i)
			}
			if !bytes.Equal(a.EphemeralKey[:], raw[at+128:at+160]) {
				t.Errorf("%s action %d: ephemeral key", name, i)
			}
			if !bytes.Equal(a.EncCompact[:], raw[at+160:at+160+EncCompactLen]) {
				t.Errorf("%s action %d: compact ciphertext", name, i)
			}
			if !bytes.Equal(a.OutCipher[:], raw[at+740:at+820]) {
				t.Errorf("%s action %d: out ciphertext", name, i)
			}
			if a.EphemeralKey == ([NodeLen]byte{}) || a.EncCompact == ([EncCompactLen]byte{}) || a.OutCipher == ([OutLen]byte{}) {
				t.Errorf("%s action %d: a field was left empty", name, i)
			}
		}
	}
}

// A bundle Parse refuses, CompactActions refuses with the same error.
func TestCompactActions_refusesWhatParseRefuses(t *testing.T) {
	good := load(t, "ironwood-1-action")
	for name, in := range map[string][]byte{
		"empty":      nil,
		"truncated":  good[:len(good)-1],
		"trailing":   append(append([]byte(nil), good...), 0),
		"no actions": {0},
	} {
		if _, err := CompactActions(in); !errors.Is(err, verify.ErrMalformed) {
			t.Errorf("%s: got %v, want ErrMalformed", name, err)
		}
	}
}

// Len is the size of every canonical bundle: the committed vectors have exactly the length it
// gives for their action count, and Parse accepts a bundle of that length and no other.
func TestLen_matchesTheVectors(t *testing.T) {
	for _, name := range vectorNames {
		raw := load(t, name)
		parsed, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := Len(parsed.Actions); got != len(raw) {
			t.Errorf("%s: Len(%d) = %d, the vector is %d bytes", name, parsed.Actions, got, len(raw))
		}
	}
}

// compactSizeLen agrees with ReadCompactSize on every boundary of the encoding.
func TestCompactSizeLen_agreesWithTheReader(t *testing.T) {
	for _, v := range []uint64{0, 1, 252, 253, 65535, 65536, 1<<32 - 1, 1 << 32} {
		_, used, ok := ReadCompactSize(compactSize(v))
		if !ok || used != compactSizeLen(v) {
			t.Errorf("%d: reader used %d, compactSizeLen %d", v, used, compactSizeLen(v))
		}
	}
}

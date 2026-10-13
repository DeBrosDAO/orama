package bundle

import (
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// The largest bundle the protocol allows is a canonical bundle of MaxActions actions, and MaxBytes
// is exactly that size: every constant that bounds a bundle is derived from MaxActions and the
// layout, not chosen separately.
func TestMaxBytes_isTheSizeOfAMaximalBundle(t *testing.T) {
	if MaxBytes != Len(MaxActions) {
		t.Fatalf("MaxBytes %d, Len(MaxActions) %d", MaxBytes, Len(MaxActions))
	}
	if maxCountLen != compactSizeLen(MaxActions) || maxProofLenLen != compactSizeLen(uint64(ProofLen(MaxActions))) {
		t.Fatalf("the CompactSize lengths %d and %d disagree with the encoding", maxCountLen, maxProofLenLen)
	}
	// Pinned: the sizes the documentation and core/pkg/txgate state. A change to the layout or the
	// ceiling fails here, and the documents and the gate's constants follow.
	if MaxBytes != 103_823 {
		t.Errorf("a bundle of %d actions is %d bytes, the documented size is 103823", MaxActions, MaxBytes)
	}
	if got := Len(16); got != 53_325 {
		t.Errorf("a bundle of 16 actions (the default limit) is %d bytes, the documented size is 53325", got)
	}
}

// Len(n) is exact: a canonical bundle of n actions is Len(n) bytes, so one more or one fewer is
// refused, and the bundle of MaxActions actions is the last that fits MaxBytes.
func TestLen_isExactAndMaxActionsIsTheLastThatFits(t *testing.T) {
	for n := 1; n <= MaxActions; n++ {
		if Len(n) > MaxBytes {
			t.Errorf("a bundle of %d actions is %d bytes, over MaxBytes %d", n, Len(n), MaxBytes)
		}
		if n > 1 && Len(n) <= Len(n-1) {
			t.Errorf("Len(%d) = %d does not grow", n, Len(n))
		}
	}
	if Len(MaxActions+1) <= MaxBytes {
		t.Errorf("a bundle of %d actions (%d bytes) fits MaxBytes %d: the ceiling is not the last that fits", MaxActions+1, Len(MaxActions+1), MaxBytes)
	}
}

// A bundle over MaxBytes is refused before any parsing, and a canonical one of MaxActions actions
// parses (the framing, not the proof, which is the verifiers' to check).
func TestParse_maximalBundleParsesAndOneMoreActionDoesNot(t *testing.T) {
	build := func(n int) []byte {
		out := []byte{byte(n)}
		out = append(out, make([]byte, n*ActionLen)...)
		out = append(out, make([]byte, HeaderLen)...)
		proof := ProofLen(n)
		out = append(out, compactSize(uint64(proof))...)
		out = append(out, make([]byte, proof+(n+1)*SigLen)...)
		return out
	}
	maximal := build(MaxActions)
	if len(maximal) != MaxBytes {
		t.Fatalf("built %d bytes, want MaxBytes %d", len(maximal), MaxBytes)
	}
	if b, err := Parse(maximal); err != nil || b.Actions != MaxActions {
		t.Fatalf("a maximal bundle: %+v, %v", b, err)
	}
	if _, err := Parse(build(MaxActions + 1)); !errors.Is(err, verify.ErrMalformed) {
		t.Fatalf("a bundle of %d actions: %v, want ErrMalformed", MaxActions+1, err)
	}
}

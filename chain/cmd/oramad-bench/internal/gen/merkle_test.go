package gen

import (
	"bytes"
	"crypto/sha256"
	"testing"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

func leafHashes(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		h := sha256.Sum256([]byte{byte(i), byte(i >> 8), 7})
		out[i] = h[:]
	}
	return out
}

// proofSet must agree with the chain's own reference implementation for every leaf.
func TestProofSet_matchesCnftProof(t *testing.T) {
	for _, n := range []int{1, 2, 3, 5, 8, 11} {
		const depth = 4
		leaves := leafHashes(n)
		siblings, root, err := proofSet(leaves, depth)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		for i := 0; i < n; i++ {
			wantSibs, wantRoot, err := cnfttypes.Proof(leaves, i, depth)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(root, wantRoot) {
				t.Fatalf("n=%d: root differs", n)
			}
			for h := range wantSibs {
				if !bytes.Equal(siblings[i][h], wantSibs[h]) {
					t.Fatalf("n=%d leaf %d level %d: sibling differs", n, i, h)
				}
			}
		}
	}
}

func TestProofSet_emptyTreeAndLimits(t *testing.T) {
	siblings, root, err := proofSet(nil, 3)
	if err != nil || len(siblings) != 0 {
		t.Fatalf("empty: %v, %d paths", err, len(siblings))
	}
	want, err := cnfttypes.FullRoot(nil, 3)
	if err != nil || !bytes.Equal(root, want) {
		t.Fatalf("empty root differs: %v", err)
	}
	if _, _, err := proofSet(leafHashes(9), 3); err == nil {
		t.Error("9 leaves in a depth-3 tree accepted")
	}
	if _, _, err := proofSet(nil, cnfttypes.MaxDepth+1); err == nil {
		t.Error("depth past the maximum accepted")
	}
	if _, _, err := proofSet(nil, 0); err == nil {
		t.Error("depth 0 accepted")
	}
}

func TestDepthFor(t *testing.T) {
	cases := map[int]uint32{0: cnfttypes.MinDepth, 1: 1, 2: 1, 3: 2, 4: 2, 5: 3, 1000: 10, 1025: 11}
	for n, want := range cases {
		if got := depthFor(n); got != want {
			t.Errorf("depthFor(%d) = %d, want %d", n, got, want)
		}
	}
}

package piece

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

// FuzzPieceRoundTrip: for any data, every leaf's proof verifies against the commitment, a
// challenge proof verifies exactly for the real leaves (never a padding leaf), the proof has the
// size ProofSize promises, and changing any byte of the leaf or of a sibling, the index, or the
// root makes verification fail.
func FuzzPieceRoundTrip(f *testing.F) {
	f.Add([]byte("hello"), uint64(0), uint16(0))
	f.Add(bytes.Repeat([]byte{7}, 3*LeafSize+5), uint64(2), uint16(40))
	f.Add([]byte{}, uint64(0), uint16(0))
	f.Add(bytes.Repeat([]byte{0}, 5*LeafSize), uint64(7), uint16(1500))
	f.Fuzz(func(t *testing.T, data []byte, index uint64, at uint16) {
		if len(data) > 40*LeafSize {
			data = data[:40*LeafSize]
		}
		c, err := Commit(data)
		if err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if c.RealLeafCount != RealLeafCount(len(data)) || c.PaddedLeafCount != PaddedLeafCount(c.RealLeafCount) {
			t.Fatalf("leaf counts %d/%d for %d bytes", c.RealLeafCount, c.PaddedLeafCount, len(data))
		}
		again, _ := Commit(bytes.Clone(data))
		if !bytes.Equal(again.Root, c.Root) {
			t.Fatal("Commit is not deterministic")
		}
		if len(data) == 0 {
			if !bytes.Equal(c.Root, EmptyRoot()) {
				t.Fatal("the empty piece does not commit to EmptyRoot")
			}
			if _, err := Prove(data, 0); err == nil {
				t.Fatal("an empty piece produced a proof")
			}
			return
		}
		index %= c.PaddedLeafCount
		proof, err := Prove(data, index)
		if err != nil {
			t.Fatalf("Prove(%d): %v", index, err)
		}
		if size, _ := ProofSize(c.PaddedLeafCount); size != len(proof.Leaf)+32*len(proof.Siblings) {
			t.Fatalf("proof is %d bytes, ProofSize says %d", len(proof.Leaf)+32*len(proof.Siblings), size)
		}
		if err := Verify(c, proof); err != nil {
			t.Fatalf("a proof Prove produced does not verify: %v", err)
		}
		if err := VerifyChallenge(c, proof); (err == nil) != (index < c.RealLeafCount) {
			t.Fatalf("VerifyChallenge at %d of %d real leaves: %v", index, c.RealLeafCount, err)
		}

		bad := Proof{Index: proof.Index, Leaf: bytes.Clone(proof.Leaf), Siblings: cloneAll(proof.Siblings)}
		bad.Leaf[int(at)%LeafSize] ^= 1
		if Verify(c, bad) == nil {
			t.Fatal("a proof with a changed leaf verified")
		}
		if len(proof.Siblings) > 0 {
			bad = Proof{Index: proof.Index, Leaf: proof.Leaf, Siblings: cloneAll(proof.Siblings)}
			bad.Siblings[int(at)%len(bad.Siblings)][int(at)%sha256.Size] ^= 1
			if Verify(c, bad) == nil {
				t.Fatal("a proof with a changed sibling verified")
			}
			if c.PaddedLeafCount > 1 {
				moved := Proof{Index: proof.Index ^ 1, Leaf: proof.Leaf, Siblings: proof.Siblings}
				if !bytes.Equal(proof.Leaf, mustLeaf(t, data, proof.Index^1)) && Verify(c, moved) == nil {
					t.Fatal("a proof for a different index verified")
				}
			}
		}
		wrongRoot := Commitment{Root: bytes.Clone(c.Root), RealLeafCount: c.RealLeafCount, PaddedLeafCount: c.PaddedLeafCount}
		wrongRoot.Root[int(at)%sha256.Size] ^= 1
		if Verify(wrongRoot, proof) == nil {
			t.Fatal("a proof verified against another root")
		}
	})
}

func cloneAll(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = bytes.Clone(in[i])
	}
	return out
}

func mustLeaf(t *testing.T, data []byte, index uint64) []byte {
	t.Helper()
	p, err := Prove(data, index)
	if err != nil {
		t.Fatal(err)
	}
	return p.Leaf
}

// FuzzPieceVerifyGarbage: Verify and VerifyChallenge on arbitrary commitments and proofs never
// panic, refuse a commitment whose padded count is not the padded real count, refuse a proof of the
// wrong shape, and are deterministic. LeafIndex stays below the real leaf count.
func FuzzPieceVerifyGarbage(f *testing.F) {
	good, _ := Commit(bytes.Repeat([]byte{1}, 2*LeafSize))
	p, _ := Prove(bytes.Repeat([]byte{1}, 2*LeafSize), 1)
	f.Add(good.Root, good.RealLeafCount, good.PaddedLeafCount, p.Index, p.Leaf, bytes.Join(p.Siblings, nil))
	f.Add([]byte{}, uint64(0), uint64(0), uint64(0), []byte{}, []byte{})
	f.Add(bytes.Repeat([]byte{9}, 32), uint64(1<<63+5), uint64(0), uint64(0), make([]byte, LeafSize), []byte{})
	f.Fuzz(func(t *testing.T, root []byte, real, padded, index uint64, leaf, siblings []byte) {
		var sibs [][]byte
		for len(siblings) >= sha256.Size && len(sibs) < 70 {
			sibs = append(sibs, siblings[:sha256.Size])
			siblings = siblings[sha256.Size:]
		}
		c := Commitment{Root: root, RealLeafCount: real, PaddedLeafCount: padded}
		pr := Proof{Index: index, Leaf: leaf, Siblings: sibs}
		err := Verify(c, pr)
		if again := Verify(c, pr); (err == nil) != (again == nil) {
			t.Fatal("Verify is not deterministic")
		}
		if err == nil {
			if padded != PaddedLeafCount(real) || padded == 0 || index >= padded || len(leaf) != LeafSize || len(root) != sha256.Size {
				t.Fatalf("a malformed commitment or proof verified: real=%d padded=%d index=%d leaf=%d root=%d", real, padded, index, len(leaf), len(root))
			}
			if VerifyChallenge(c, pr) == nil && index >= real {
				t.Fatal("a challenge on a padding leaf verified")
			}
		}
		if real > 0 {
			if i, err := LeafIndex(root, real); err != nil || i >= real {
				t.Fatalf("LeafIndex = %d, %v for %d real leaves", i, err, real)
			}
		}
	})
}

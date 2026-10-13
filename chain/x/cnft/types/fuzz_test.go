package types

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

// FuzzCNFTLeafEncoding: EncodeLeaf is injective, so two different leaves can never share a hash
// (a length-prefixed field cannot be shifted into its neighbour), and the leaf hash is the
// SHA-256 of the encoding.
func FuzzCNFTLeafEncoding(f *testing.F) {
	f.Add([]byte("a"), []byte("b"), []byte(""), []byte("cid"), []byte("h"), uint64(1), uint32(0),
		[]byte("ab"), []byte(""), []byte(""), []byte("cid"), []byte("h"), uint64(1), uint32(0))
	f.Add([]byte{}, []byte{}, []byte{}, []byte{}, []byte{}, uint64(0), uint32(0),
		[]byte{}, []byte{}, []byte{}, []byte{}, []byte{}, uint64(0), uint32(1))
	f.Fuzz(func(t *testing.T, a1, o1, d1, m1, c1 []byte, n1 uint64, h1 uint32, a2, o2, d2, m2, c2 []byte, n2 uint64, h2 uint32) {
		e1 := EncodeLeaf(a1, o1, d1, m1, c1, n1, h1)
		e2 := EncodeLeaf(a2, o2, d2, m2, c2, n2, h2)
		same := bytes.Equal(a1, a2) && bytes.Equal(o1, o2) && bytes.Equal(d1, d2) && bytes.Equal(m1, m2) && bytes.Equal(c1, c2) && n1 == n2 && h1 == h2
		if same != bytes.Equal(e1, e2) {
			t.Fatalf("equal leaves=%v but equal encodings=%v", same, bytes.Equal(e1, e2))
		}
		if sum := sha256.Sum256(e1); !bytes.Equal(sum[:], LeafHash(a1, o1, d1, m1, c1, n1, h1)) {
			t.Fatal("LeafHash is not the SHA-256 of EncodeLeaf")
		}
	})
}

type snapshot struct {
	root   []byte
	leaves [][]byte
}

// FuzzCNFTMerkleTree drives the concurrent Merkle tree with a byte-coded sequence of appends,
// replacements and proofs, against a plain model. After every step the root is the full-tree root
// of the model; a proof of a current leaf is accepted (in full or with the canopy levels left
// out), a proof with a changed sibling is refused, and a proof taken against an older root is
// accepted exactly when that root is still buffered and the leaf has not changed since.
func FuzzCNFTMerkleTree(f *testing.F) {
	f.Add([]byte{2, 1, 0, 1, 2, 0, 3, 4, 1, 0, 9, 2, 1, 0, 3, 0, 5, 6, 4, 0, 1, 5, 1, 0})
	f.Add([]byte{0, 0, 0, 1, 1, 0, 1, 2, 1, 2, 0, 0, 1, 2, 3, 3, 3, 2, 1, 5, 2, 0, 1})
	f.Fuzz(func(t *testing.T, script []byte) {
		const depth = 4
		if len(script) < 2 {
			return
		}
		buffer, canopy := uint32(script[0]%4)+1, uint32(script[1]%3)
		script = script[2:]
		tree, err := NewTree(1, 1, "creator", depth, buffer, canopy)
		if err != nil {
			t.Fatal(err)
		}
		var model [][]byte
		history := []snapshot{{root: tree.Root()}}
		fresh := func(a, b byte) []byte {
			sum := sha256.Sum256([]byte{a, b, byte(len(model))})
			return sum[:]
		}
		check := func(step string) {
			t.Helper()
			want, err := FullRoot(model, depth)
			if err != nil || !bytes.Equal(tree.Root(), want) {
				t.Fatalf("%s: tree root differs from the full root of %d leaves (%v)", step, len(model), err)
			}
			if tree.Rightmost.Index != uint32(len(model)) {
				t.Fatalf("%s: rightmost index %d, model has %d leaves", step, tree.Rightmost.Index, len(model))
			}
		}
		remember := func() {
			history = append(history, snapshot{root: tree.Root(), leaves: cloneLeaves(model)})
		}
		for ; len(script) >= 3; script = script[3:] {
			op, a, b := script[0]%5, script[1], script[2]
			switch op {
			case 0:
				if len(model) == 1<<depth {
					continue
				}
				leaf := fresh(a, b)
				if err := tree.Append(tree.Root(), leaf); err != nil {
					t.Fatalf("Append: %v", err)
				}
				model = append(model, leaf)
				check("append")
				remember()
			case 1:
				if len(model) == 0 {
					continue
				}
				i := int(a) % len(model)
				siblings, root, err := Proof(model, i, depth)
				if err != nil {
					t.Fatal(err)
				}
				next := fresh(a, b)
				if b%2 == 1 {
					siblings = siblings[:depth-int(canopy)]
				}
				if err := tree.SetLeaf(root, model[i], next, uint32(i), siblings); err != nil {
					t.Fatalf("SetLeaf(%d) with a proof against the current root: %v", i, err)
				}
				model[i] = next
				check("set leaf")
				remember()
			case 2:
				if len(model) == 0 {
					continue
				}
				i := int(a) % len(model)
				siblings, root, _ := Proof(model, i, depth)
				if err := tree.Prove(root, model[i], uint32(i), siblings); err != nil {
					t.Fatalf("Prove(%d) of a current leaf: %v", i, err)
				}
				bad := cloneLeaves(siblings)
				bad[int(b)%len(bad)][int(b)%HashSize] ^= 1
				if tree.Prove(root, model[i], uint32(i), bad) == nil {
					t.Fatalf("Prove(%d) accepted a changed sibling", i)
				}
				otherSum := sha256.Sum256(model[i])
				other := otherSum[:]
				if tree.Prove(root, other, uint32(i), siblings) == nil {
					t.Fatalf("Prove(%d) accepted a leaf that is not there", i)
				}
			default:
				old := history[int(a)%len(history)]
				if len(old.leaves) == 0 {
					continue
				}
				i := int(b) % len(old.leaves)
				siblings, root, _ := Proof(old.leaves, i, depth)
				if !bytes.Equal(root, old.root) {
					t.Fatalf("model root of snapshot differs from the tree's root then")
				}
				clone := cloneTree(t, tree)
				err := clone.Prove(old.root, old.leaves[i], uint32(i), siblings)
				// The changelog replays every change of leaf i since that root, so the proof holds
				// when the buffered root's leaf is the leaf now, even if it changed and changed back.
				want := tree.ContainsRoot(old.root) && bytes.Equal(model[i], old.leaves[i])
				if (err == nil) != want {
					t.Fatalf("Prove(%d) against an older root: error %v, want accepted=%v (root buffered=%v, leaf equal now=%v)",
						i, err, want, tree.ContainsRoot(old.root), bytes.Equal(model[i], old.leaves[i]))
				}
			}
		}
	})
}

func cloneTree(t *testing.T, tree *Tree) *Tree {
	t.Helper()
	raw, err := tree.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var out Tree
	if err := out.Unmarshal(raw); err != nil {
		t.Fatal(err)
	}
	return &out
}

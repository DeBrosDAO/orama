package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func leafHash(i int) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(i)+1)
	sum := sha256.Sum256(buf[:])
	return sum[:]
}

func newTree(t *testing.T, depth, buffer, canopy uint32) *Tree {
	t.Helper()
	tree, err := NewTree(1, 1, "creator", depth, buffer, canopy)
	require.NoError(t, err)
	return tree
}

func requireRoot(t *testing.T, tree *Tree, leaves [][]byte, depth uint32) {
	t.Helper()
	want, err := FullRoot(leaves, depth)
	require.NoError(t, err)
	require.Equal(t, want, tree.Root())
}

func TestAppend_matchesFullRoot(t *testing.T) {
	const depth = 4
	tree := newTree(t, depth, 8, 0)
	var leaves [][]byte
	anchor := tree.Root()
	for i := 0; i < 6; i++ {
		leaves = append(leaves, leafHash(i))
		require.NoError(t, tree.Append(anchor, leaves[i]))
		anchor = tree.Root()
		requireRoot(t, tree, leaves, depth)
	}

	leaves[3] = leafHash(30)
	siblings, root, err := Proof(leavesAt(leaves, 3, leafHash(3)), 3, depth)
	require.NoError(t, err)
	require.NoError(t, tree.SetLeaf(root, leafHash(3), leaves[3], 3, siblings))
	requireRoot(t, tree, leaves, depth)

	leaves[0] = leafHash(40)
	siblings, root, err = Proof(replaceAt(leaves, 0, leafHash(0)), 0, depth)
	require.NoError(t, err)
	require.NoError(t, tree.SetLeaf(root, leafHash(0), leaves[0], 0, siblings))
	requireRoot(t, tree, leaves, depth)

	for i := 6; i < 10; i++ {
		anchor = tree.Root()
		leaves = append(leaves, leafHash(i))
		require.NoError(t, tree.Append(anchor, leaves[i]))
		requireRoot(t, tree, leaves, depth)
	}

	siblings, root, err = Proof(leaves, 2, depth)
	require.NoError(t, err)
	require.NoError(t, tree.SetLeaf(root, leaves[2], emptyNode(0), 2, siblings))
	leaves[2] = emptyNode(0)
	requireRoot(t, tree, leaves, depth)

	anchor = tree.Root()
	leaves = append(leaves, leafHash(99))
	require.NoError(t, tree.Append(anchor, leaves[len(leaves)-1]))
	requireRoot(t, tree, leaves, depth)
}

func leavesAt(leaves [][]byte, index int, previous []byte) [][]byte {
	out := cloneLeaves(leaves)
	out[index] = previous
	return out
}

func replaceAt(leaves [][]byte, index int, previous []byte) [][]byte {
	return leavesAt(leaves, index, previous)
}

func cloneLeaves(leaves [][]byte) [][]byte {
	out := make([][]byte, len(leaves))
	for i := range leaves {
		out[i] = append([]byte(nil), leaves[i]...)
	}
	return out
}

func TestTwoAppends_keepTheAnchorRoot(t *testing.T) {
	wide := newTree(t, 4, 2, 0)
	empty := wide.Root()
	require.NoError(t, wide.Append(empty, leafHash(1)))
	intermediate := wide.Root()
	require.NoError(t, wide.Append(empty, leafHash(2)))
	require.True(t, wide.ContainsRoot(intermediate))
	require.False(t, wide.ContainsRoot(empty))

	narrow := newTree(t, 4, 1, 0)
	empty = narrow.Root()
	require.NoError(t, narrow.Append(empty, leafHash(1)))
	require.ErrorIs(t, narrow.Append(empty, leafHash(2)), ErrRootNotInBuffer)
}

func TestProof_fastForwardAndStale(t *testing.T) {
	const depth = 4
	tree := newTree(t, depth, 8, 0)
	require.NoError(t, tree.Append(tree.Root(), leafHash(1)))
	afterFirst := tree.Root()
	siblings, _, err := Proof([][]byte{leafHash(1)}, 0, depth)
	require.NoError(t, err)

	require.NoError(t, tree.Append(afterFirst, leafHash(2)))
	require.NoError(t, tree.Prove(afterFirst, leafHash(1), 0, siblings))

	require.NoError(t, tree.SetLeaf(afterFirst, leafHash(1), leafHash(9), 0, siblings))
	err = tree.SetLeaf(afterFirst, leafHash(1), leafHash(8), 0, siblings)
	require.ErrorIs(t, err, ErrStaleProof)

	require.ErrorIs(t, tree.Prove(bytes.Repeat([]byte{0xab}, HashSize), leafHash(9), 0, siblings), ErrRootNotInBuffer)
}

func TestProof_olderThanBufferFails(t *testing.T) {
	const depth = 3
	tree := newTree(t, depth, 2, 0)
	empty := tree.Root()
	require.NoError(t, tree.Append(empty, leafHash(1)))
	require.NoError(t, tree.Append(tree.Root(), leafHash(2)))
	require.NoError(t, tree.Append(tree.Root(), leafHash(3)))
	siblings, _, err := Proof([][]byte{leafHash(1)}, 0, depth)
	require.NoError(t, err)
	require.ErrorIs(t, tree.Prove(empty, leafHash(1), 0, siblings), ErrRootNotInBuffer)
}

func TestCanopy_shortProofMatchesCurrentRoot(t *testing.T) {
	const depth = 5
	const canopy = 2
	tree := newTree(t, depth, 8, canopy)
	var leaves [][]byte
	for i := 0; i < 9; i++ {
		leaves = append(leaves, leafHash(i))
		require.NoError(t, tree.Append(tree.Root(), leaves[i]))
	}
	siblings, root, err := Proof(leaves, 7, depth)
	require.NoError(t, err)
	require.Equal(t, root, tree.Root())
	short := siblings[:depth-canopy]
	require.NoError(t, tree.Prove(root, leaves[7], 7, short))
	updated := leafHash(70)
	require.NoError(t, tree.SetLeaf(root, leaves[7], updated, 7, short))
	leaves[7] = updated
	requireRoot(t, tree, leaves, depth)
}

func TestMerkleProof_1024Leaves(t *testing.T) {
	const depth = 10
	const n = 1024
	tree := newTree(t, depth, 8, 0)
	leaves := make([][]byte, n)
	for i := 0; i < n; i++ {
		leaves[i] = leafHash(i)
		require.NoError(t, tree.Append(tree.Root(), leaves[i]))
	}
	for _, index := range []int{0, 512, 1023} {
		siblings, root, err := Proof(leaves, index, depth)
		require.NoError(t, err)
		require.Equal(t, tree.Root(), root)
		require.NoError(t, tree.Prove(root, leaves[index], uint32(index), siblings))
	}
}

func TestMerkleBenchmark_1MLeaves(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1M-leaf benchmark in short mode")
	}
	const depth = 20
	const n = 1_000_000
	tree := newTree(t, depth, 16, 0)
	leaves := make([][]byte, n)
	for i := 0; i < n; i++ {
		h := leafHash(i)
		leaves[i] = h
		if err := tree.Append(tree.Root(), h); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	for _, index := range []int{12345, n - 1} {
		siblings, root, err := Proof(leaves, index, depth)
		require.NoError(t, err)
		require.Equal(t, tree.Root(), root)
		require.NoError(t, tree.Prove(root, leaves[index], uint32(index), siblings))
	}
}

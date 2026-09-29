package types_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func testTuple() types.Tuple {
	return types.Tuple{
		BundleCid:  "bafyvalidarchivecid",
		BundleHash: bytes.Repeat([]byte{1}, types.HashLen),
		MerkleRoot: bytes.Repeat([]byte{2}, types.HashLen),
		Piece:      types.Piece{Root: bytes.Repeat([]byte{7}, types.HashLen), RealLeafCount: 3, PaddedLeafCount: 4, PieceBytes: 3000},
	}
}

func TestTuple_mismatchNamesTheFirstFieldThatDiffers(t *testing.T) {
	base := testTuple()
	require.NoError(t, base.Mismatch(testTuple()))
	require.True(t, base.Equal(testTuple()))

	for name, tc := range map[string]struct {
		mutate func(*types.Tuple)
		want   error
	}{
		"root":   {func(x *types.Tuple) { x.MerkleRoot = bytes.Repeat([]byte{9}, types.HashLen) }, types.ErrWrongRoot},
		"cid":    {func(x *types.Tuple) { x.BundleCid = "bafyother" }, types.ErrWrongBundle},
		"hash":   {func(x *types.Tuple) { x.BundleHash = bytes.Repeat([]byte{9}, types.HashLen) }, types.ErrWrongBundle},
		"piece":  {func(x *types.Tuple) { x.Piece.Root = bytes.Repeat([]byte{9}, types.HashLen) }, types.ErrWrongPiece},
		"length": {func(x *types.Tuple) { x.Piece.PieceBytes = 1024 }, types.ErrWrongPiece},
	} {
		other := testTuple()
		tc.mutate(&other)
		require.Falsef(t, base.Equal(other), "%s", name)
		require.ErrorIsf(t, base.Mismatch(other), tc.want, "%s", name)
	}
}

func TestRangeRecord_attestedByAndContested(t *testing.T) {
	win := testTuple()
	other := testTuple()
	other.MerkleRoot = bytes.Repeat([]byte{9}, types.HashLen)

	decided := types.RangeRecord{
		Decided: true, BundleCid: win.BundleCid, BundleHash: win.BundleHash, MerkleRoot: win.MerkleRoot,
		PieceRoot: win.Piece.Root, RealLeafCount: 3, PaddedLeafCount: 4, PieceBytes: 3000,
		Archivers: []string{"a", "b", "c"},
	}
	got, ok := decided.AttestedBy("b")
	require.True(t, ok)
	require.True(t, got.Equal(win))
	_, ok = decided.AttestedBy("z")
	require.False(t, ok)
	require.False(t, decided.Contested(win))
	require.True(t, decided.Contested(other))

	c1, c2 := types.NewCandidate(win), types.NewCandidate(other)
	c1.Archivers, c2.Archivers = []string{"a"}, []string{"b"}
	open := types.RangeRecord{Candidates: []types.Candidate{c1, c2}}
	got, ok = open.AttestedBy("b")
	require.True(t, ok)
	require.True(t, got.Equal(other))
	_, ok = open.AttestedBy("z")
	require.False(t, ok)
	require.True(t, open.Contested(win), "another candidate is on the range")
	require.False(t, types.RangeRecord{Candidates: []types.Candidate{c1}}.Contested(win))
	require.False(t, types.RangeRecord{}.Contested(win), "a range nobody attested is not contested")
}

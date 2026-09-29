package types_test

import (
	"bytes"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func TestProtoRoundTrip(t *testing.T) {
	roundTrip(t, &types.Params{RetentionWindowBlocks: types.DefaultBlocksIn14Days, MaxPieceBytes: types.DefaultMaxPieceBytes})
	roundTrip(t, &types.RangeRecord{
		StartHeight: 1,
		EndHeight:   8,
		BundleCid:   "bafyroundtrip",
		BundleHash:  bytes.Repeat([]byte{3}, types.HashLen),
		MerkleRoot:  bytes.Repeat([]byte{4}, types.HashLen),
		DealIds:     []string{"deal-1", "deal-2"},
		Archivers:   []string{"archiver-not-parsed-here"},
		Archived:    true,
		PieceRoot:   bytes.Repeat([]byte{8}, types.HashLen), RealLeafCount: 3, PaddedLeafCount: 4, PieceBytes: 3000,
	})
	roundTrip(t, &types.MsgAttest{
		Archiver:    "cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
		StartHeight: 1,
		EndHeight:   2,
		BundleCid:   "bafyattest",
		BundleHash:  bytes.Repeat([]byte{5}, types.HashLen),
		MerkleRoot:  bytes.Repeat([]byte{6}, types.HashLen),
		PieceRoot:   bytes.Repeat([]byte{7}, types.HashLen), RealLeafCount: 3, PaddedLeafCount: 4, PieceBytes: 3000,
	})
	roundTrip(t, &types.MsgAttachReplicas{
		Archiver:    "cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
		StartHeight: 1,
		EndHeight:   2,
		DealIds:     []string{"deal-9"},
	})
	roundTrip(t, &types.MsgCreateArchiveDeal{
		Archiver:        "cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
		StartHeight:     1,
		EndHeight:       2,
		NodeId:          "node-1",
		PieceRoot:       bytes.Repeat([]byte{7}, types.HashLen),
		RealLeafCount:   3,
		PaddedLeafCount: 4,
		PieceBytes:      3000,
	})
	roundTrip(t, &types.MsgCreateArchiveDealResponse{DealId: 9, Archived: true, Replicas: 3})
	roundTrip(t, &types.GenesisState{
		Params:             types.DefaultParams(),
		Ranges:             []types.RangeRecord{{StartHeight: 4, EndHeight: 9, BundleCid: "bafygenesis"}},
		LastArchivedHeight: 0,
	})
	roundTrip(t, &types.QueryRetainHeightResponse{
		RetainHeight:          10,
		Tip:                   80,
		LastArchivedHeight:    10,
		RetentionWindowBlocks: types.DefaultBlocksIn14Days,
	})
}

func roundTrip[T any, PT interface {
	*T
	proto.Message
}](t *testing.T, in PT) {
	t.Helper()
	bz, err := proto.Marshal(in)
	require.NoError(t, err)
	require.NotEmpty(t, bz)
	var out T
	require.NoError(t, proto.Unmarshal(bz, PT(&out)))
	require.True(t, proto.Equal(in, PT(&out)), "%T", in)
}

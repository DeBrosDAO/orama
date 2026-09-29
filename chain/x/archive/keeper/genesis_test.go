package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func TestInitGenesis_defaultState(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)

	params, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultBlocksIn14Days, params.RetentionWindowBlocks)

	last, err := f.Keeper.LastArchivedHeight.Get(f.Ctx)
	require.NoError(t, err)
	require.Zero(t, last)

	ok, err := f.Keeper.PruneAllowed(f.Ctx, 1)
	require.NoError(t, err)
	require.False(t, ok, "nothing is archived, so nothing may be pruned")
}

func TestInitGenesis_rejectsArchivedRangeWithoutQuorum(t *testing.T) {
	f := newTestFixture(t)
	gs := types.DefaultGenesisState()
	gs.Ranges = []types.RangeRecord{{
		StartHeight: 1,
		EndHeight:   10,
		BundleCid:   "bafyvalidarchivecid",
		BundleHash:  digest(1),
		MerkleRoot:  digest(2),
		DealIds:     []string{"1", "2", "3"},
		Archivers:   []string{acc(1).String()},
		Operators:   []string{opOf(1)},
		Archived:    true,
	}}
	gs.LastArchivedHeight = 10
	require.Error(t, f.Keeper.InitGenesis(f.Ctx, *gs))
}

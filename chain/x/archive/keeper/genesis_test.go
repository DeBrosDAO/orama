package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

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
		Decided:     true,
	}}
	gs.LastArchivedHeight = 10
	require.Error(t, f.Keeper.InitGenesis(f.Ctx, *gs))
}

// A range whose tuples are still contested exports and imports with its candidates and their tallies,
// and the imported keeper keeps counting toward the same winner.
func TestExportGenesis_roundTripsAContestedRange(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.attest(t, 2, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.attest(t, 3, 1, 50, "bafyarchivecid", digest(1), digest(3))

	gs, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, gs.Validate())
	require.Len(t, gs.Ranges[0].Candidates, 2)

	g := newTestFixture(t)
	require.NoError(t, g.Keeper.InitGenesis(g.Ctx, *gs))
	again, err := g.Keeper.ExportGenesis(g.Ctx)
	require.NoError(t, err)
	require.Equal(t, gs, again)

	res := g.attest(t, 4, 1, 50, "bafyarchivecid", digest(1), digest(2))
	require.Equal(t, uint32(3), res.Attesters)
	rec, err := g.Keeper.GetRange(g.Ctx, 1, 50)
	require.NoError(t, err)
	require.True(t, rec.Decided)
	require.Equal(t, digest(2), rec.MerkleRoot)
}

// Export refuses state that breaks a registry invariant instead of writing it out.
func TestExportGenesis_refusesAnOperatorInTwoCandidates(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.attest(t, 1, 1, 50, "bafyarchivecid", digest(1), digest(2))
	f.attest(t, 2, 1, 50, "bafyarchivecid", digest(1), digest(3))
	rec, err := f.Keeper.GetRange(f.Ctx, 1, 50)
	require.NoError(t, err)
	rec.Candidates[1].Operators = rec.Candidates[0].Operators
	require.NoError(t, f.Keeper.Ranges.Set(f.Ctx, collections.Join(int64(1), int64(50)), rec))

	_, err = f.Keeper.ExportGenesis(f.Ctx)
	require.ErrorContains(t, err, "attested two candidate tuples")
}

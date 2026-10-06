package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// Storage and relay payments are minted against their ceilings. Before
// cumulative_service_minted existed, the first such mint left the bank supply
// above genesis + validator + development - burned, and the supply
// invariant (and a genesis re-import) failed from then on.
func TestMintStorageService_keepsTheSupplyInvariantWithRelayMints(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	storage, err := f.Keeper.StorageCeiling(ctx, 1)
	require.NoError(t, err)
	require.True(t, storage.IsPositive())
	relay, err := f.Keeper.RelayCeiling(ctx, 1)
	require.NoError(t, err)

	emissionBefore := f.Bank.balanceOf(types.ModuleName)
	require.NoError(t, f.Keeper.MintStorageService(ctx, 1, storage.QuoRaw(2)))
	require.NoError(t, f.Keeper.MintRelayReward(ctx, 1, relay))
	require.True(t, f.Bank.balanceOf(storagetypes.ModuleName).Equal(storage.QuoRaw(2)))
	require.True(t, f.Bank.balanceOf(relaytypes.ModuleName).Equal(relay))
	require.True(t, f.Bank.balanceOf(types.ModuleName).Equal(emissionBefore), "a service mint does not stay in x/emission")

	detail, broken := f.Keeper.CheckSupplyInvariant(ctx)
	require.False(t, broken, detail)
	state, err := f.Keeper.EpochState.Get(ctx)
	require.NoError(t, err)
	require.True(t, state.CumulativeServiceMinted.Equal(storage.QuoRaw(2).Add(relay)))

	// A burn in the same block is still attributed as a burn, not hidden by the mints.
	f.Bank.supply = f.Bank.supply.Sub(math.NewInt(7))
	require.NoError(t, f.Keeper.ReconcileBurns(ctx))
	detail, broken = f.Keeper.CheckSupplyInvariant(ctx)
	require.False(t, broken, detail)
}

func TestMintStorageService_refusesPastTheCeilingAndBadAmounts(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))
	storage, err := f.Keeper.StorageCeiling(ctx, 1)
	require.NoError(t, err)

	require.NoError(t, f.Keeper.MintStorageService(ctx, 1, storage))
	before := f.Bank.GetSupply(ctx, params.BaseDenom).Amount
	require.Error(t, f.Keeper.MintStorageService(ctx, 1, math.NewInt(1)), "the ceiling is spent")
	require.Error(t, f.Keeper.MintStorageService(ctx, 1, math.ZeroInt()))
	require.Error(t, f.Keeper.MintStorageService(ctx, 99, math.NewInt(1)), "no such epoch")
	require.True(t, f.Bank.GetSupply(ctx, params.BaseDenom).Amount.Equal(before), "a refused mint writes nothing")
}

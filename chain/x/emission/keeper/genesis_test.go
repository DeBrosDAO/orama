package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

func TestInitGenesis_normalGenesisHasZeroGenesisSupply(t *testing.T) {
	f := newTestFixture(t)
	// No coins funded into the bank keeper before InitGenesis: a normal, zero-premine genesis.
	f.initGenesis(t, nil)

	state, err := f.Keeper.EpochState.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, state.GenesisSupply.IsZero())
	require.Equal(t, uint64(1), state.CurrentEpoch)
}

func TestInitGenesis_rejectsNonzeroSupplyWhenBootstrapStakeNotAllowed(t *testing.T) {
	f := newTestFixture(t)
	f.Bank.FundBondedPool(math.NewInt(1_500_000_000_000))

	gs := types.DefaultGenesisState() // AllowBootstrapStake defaults to false
	err := f.Keeper.InitGenesis(f.Ctx, *gs)
	require.Error(t, err)
}

func TestInitGenesis_rejectsBootstrapStakeOnProductionChainID(t *testing.T) {
	f := newTestFixture(t)
	f.Bank.FundBondedPool(math.NewInt(1_500_000_000_000))
	ctx := f.Ctx.WithChainID("orama-1") // no -devnet-/-stagenet-/-localnet- marker

	gs := types.DefaultGenesisState()
	gs.Params.AllowBootstrapStake = true
	err := f.Keeper.InitGenesis(ctx, *gs)
	require.Error(t, err)
}

func TestInitGenesis_rejectsBootstrapStakeLeftIdleOutsideBondedPool(t *testing.T) {
	f := newTestFixture(t)
	// 1,500 ORAMA in the bonded pool, but ALSO 500 ORAMA sitting idle elsewhere (e.g. an
	// under-bonded genesis account) - the total genesis supply of 2,000 ORAMA must equal the
	// bonded pool balance exactly, and here it doesn't.
	f.Bank.FundBondedPool(math.NewInt(1_500_000_000_000))
	f.Bank.SetGenesisSupply(math.NewInt(2_000_000_000_000))
	ctx := f.Ctx.WithChainID("orama-devnet-1")

	gs := types.DefaultGenesisState()
	gs.Params.AllowBootstrapStake = true
	err := f.Keeper.InitGenesis(ctx, *gs)
	require.Error(t, err)
}

func TestInitGenesis_acceptsBootstrapStakeExactlyInBondedPoolOnDevnet(t *testing.T) {
	f := newTestFixture(t)
	f.Bank.FundBondedPool(math.NewInt(1_500_000_000_000)) // 1,500 ORAMA, all of it bonded
	ctx := f.Ctx.WithChainID("orama-devnet-1")

	gs := types.DefaultGenesisState()
	gs.Params.AllowBootstrapStake = true
	require.NoError(t, f.Keeper.InitGenesis(ctx, *gs))

	state, err := f.Keeper.EpochState.Get(ctx)
	require.NoError(t, err)
	require.True(t, state.GenesisSupply.Equal(math.NewInt(1_500_000_000_000)))

	_, broken := f.Keeper.CheckSupplyInvariant(ctx)
	require.False(t, broken)
}

func TestInitGenesis_acceptsBootstrapStakeOnStagenetAndLocalnetChainIDs(t *testing.T) {
	for _, chainID := range []string{"orama-stagenet-1", "orama-localnet-1"} {
		t.Run(chainID, func(t *testing.T) {
			f := newTestFixture(t)
			f.Bank.FundBondedPool(math.NewInt(1_000_000_000))
			ctx := f.Ctx.WithChainID(chainID)

			gs := types.DefaultGenesisState()
			gs.Params.AllowBootstrapStake = true
			require.NoError(t, f.Keeper.InitGenesis(ctx, *gs))
		})
	}
}

func TestInitGenesis_reimportedGenesisTrustsGivenGenesisSupply(t *testing.T) {
	f := newTestFixture(t)
	// A genesis produced by ExportGenesis after the chain has already minted 4 epochs' worth
	// (current_epoch=5): this is NOT a fresh genesis (cumulative_minted > 0), so the given
	// genesis_supply (0) must be trusted as-is rather than recomputed from live bank supply.
	wantMinted := math.NewInt(4 * 8_908_800_000_000) // 4 closed epochs' hard-coded validator share
	f.Bank.SetGenesisSupply(wantMinted)              // bank supply must match the final invariant
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.EpochState.CurrentEpoch = 5
		gs.EpochState.CumulativeMinted = wantMinted
		gs.EpochState.GenesisSupply = math.ZeroInt()
	})

	state, err := f.Keeper.EpochState.Get(f.Ctx)
	require.NoError(t, err)
	require.True(t, state.GenesisSupply.IsZero(), "a re-imported genesis's GenesisSupply must not be recomputed from live bank supply")
}

func TestInitGenesis_rejectsInvalidGenesisState(t *testing.T) {
	f := newTestFixture(t)
	gs := types.DefaultGenesisState()
	gs.Params = types.NewParams(0, 0, false) // invalid params

	err := f.Keeper.InitGenesis(f.Ctx, *gs)
	require.Error(t, err, "InitGenesis must call GenesisState.Validate() itself, not just trust ValidateGenesis was run beforehand")
}

func TestInitGenesis_rejectsStateThatWouldBreakTheSupplyInvariant(t *testing.T) {
	f := newTestFixture(t)
	// Bank supply doesn't match genesis_supply + cumulative_minted - cumulative_burned, even
	// though GenesisState.Validate() alone can't see that (it only checks the genesis state is
	// internally consistent, not that it matches live bank state).
	f.Bank.SetGenesisSupply(math.NewInt(999_000_000_000_000))
	gs := types.DefaultGenesisState()
	gs.EpochState.CurrentEpoch = 2
	gs.EpochState.CumulativeMinted = math.NewInt(8_908_800_000_000)

	err := f.Keeper.InitGenesis(f.Ctx, *gs)
	require.Error(t, err)
}

func TestInitGenesis_rejectsShortEpochsOnProductionChainIDWithZeroSupply(t *testing.T) {
	f := newTestFixture(t)
	gs := types.DefaultGenesisState()
	gs.Params = types.NewParams(30*time.Second, 1, true)

	err := f.Keeper.InitGenesis(f.Ctx.WithChainID("orama-1"), *gs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "allow_bootstrap_stake requires a chain-id")
}

func TestExportGenesis_roundTripsState(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})

	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	exported, err := f.Keeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), exported.EpochState.CurrentEpoch)
	require.Len(t, exported.Ceilings, 1)
	require.Equal(t, uint64(1), exported.Ceilings[0].Epoch)
}

// TestInitGenesis_exportReimportRoundTripDoesNotStallOnHeight simulates exactly the scenario item
// 2 of the review is about: exporting mid-chain and re-importing into a fresh chain incarnation
// that restarts at a low height. Before BlocksInEpoch replaced an absolute epoch_start_height,
// ShouldCloseEpoch compared the new (low) block height against the old (high) start height,
// underflowing and stalling the epoch forever. This proves the epoch can still close normally
// after the round trip.
func TestInitGenesis_exportReimportRoundTripDoesNotStallOnHeight(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 5, true)
	})

	// Run the original chain up to a high height/time and close exactly one epoch there: by the
	// 5th block, 50s have elapsed (>= the 30s epoch_duration) and 5 blocks have been produced
	// (>= min_blocks_per_epoch), so this closes epoch 1 on the 5th call.
	ctx := f.Ctx
	for h := int64(1); h <= 5; h++ {
		ctx = ctx.WithBlockHeight(1_000_000 + h).WithBlockTime(time.Unix(1_700_000_000+h*10, 0))
		require.NoError(t, f.Keeper.AdvanceBlock(ctx))
	}
	preExport, err := f.Keeper.EpochState.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), preExport.CurrentEpoch, "one epoch should have closed during the original run")

	exported, err := f.Keeper.ExportGenesis(ctx)
	require.NoError(t, err)

	// Re-import into a brand new keeper/store, as a fresh chain incarnation starting at height 1
	// again (a coordinated hard fork or a chain restart), with the bank supply already matching
	// what the exported state implies.
	f2 := newTestFixture(t)
	f2.Bank.SetGenesisSupply(exported.EpochState.GenesisSupply.Add(exported.EpochState.CumulativeMinted))
	freshCtx := f2.Ctx.WithBlockHeight(1).WithBlockTime(time.Unix(1_800_000_000, 0)).WithChainID("orama-devnet-2")
	require.NoError(t, f2.Keeper.InitGenesis(freshCtx, *exported))

	// Drive it forward at the new, much lower height: the epoch must still be able to close using
	// BlocksInEpoch, which was reset to 0 by the original close and carried across the export
	// untouched by the new chain's low height numbering.
	postImportCtx := freshCtx
	for i := int64(1); i <= 5; i++ {
		postImportCtx = postImportCtx.WithBlockHeight(1 + i).WithBlockTime(time.Unix(1_800_000_000+i*10, 0))
		require.NoError(t, f2.Keeper.AdvanceBlock(postImportCtx))
	}
	postImport, err := f2.Keeper.EpochState.Get(postImportCtx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), postImport.CurrentEpoch, "the epoch must close again after the round trip, not stall")
}

// TestInitGenesis_reconcileBurnsAfterASlashKeepsInvariantHolding drives one epoch close (a mint),
// then simulates an x/slashing-style burn from the bonded pool, and confirms ReconcileBurns (run
// once by EndBlock every block) attributes it to cumulative_burned so the supply invariant keeps
// holding - proving the fix for item 1 of the review ("burns are never tracked").
func TestInitGenesis_reconcileBurnsAfterASlashKeepsInvariantHolding(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})

	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	_, broken := f.Keeper.CheckSupplyInvariant(ctx)
	require.False(t, broken, "invariant must hold right after the mint, before any slash")

	// Simulate x/slashing burning 5% of the bonded pool's stake on a double-sign. It doesn't
	// matter which module account the burn comes from - ReconcileBurns only looks at total
	// supply - so this burns from the fee collector for simplicity.
	slashed := math.NewInt(1_000_000_000)
	f.Bank.SimulateExternalBurn(testFeeCollectorName, slashed)

	require.NoError(t, f.Keeper.ReconcileBurns(ctx))

	state, err := f.Keeper.EpochState.Get(ctx)
	require.NoError(t, err)
	require.True(t, state.CumulativeBurned.Equal(slashed), "the burn must be attributed to cumulative_burned")

	_, broken = f.Keeper.CheckSupplyInvariant(ctx)
	require.False(t, broken, "invariant must hold again once ReconcileBurns has run")
}

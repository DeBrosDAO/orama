package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

func shortEpochFixture(t *testing.T) (*testFixture, time.Time) {
	t.Helper()
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})
	return f, time.Unix(1_700_000_031, 0)
}

// ReconcileBurns repairs only a drop in supply. A rise it never touches, so the per-block check must
// be the one that refuses it: norama created by a path that did not record itself with x/emission.
func TestAssertSupplyConserved_refusesNoramaCreatedOutsideEmission(t *testing.T) {
	f, at := shortEpochFixture(t)
	ctx := f.Ctx.WithBlockTime(at)
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))
	require.NoError(t, f.Keeper.ReconcileBurns(ctx))
	require.NoError(t, f.Keeper.AssertSupplyConserved(ctx), "an honest block passes")

	f.Bank.supply = f.Bank.supply.AddRaw(7)
	require.NoError(t, f.Keeper.ReconcileBurns(ctx), "a rise is not a burn: ReconcileBurns does nothing")
	err := f.Keeper.AssertSupplyConserved(ctx)
	require.ErrorContains(t, err, "above what x/emission recorded")
	require.ErrorContains(t, err, "7 was created outside x/emission")
}

func TestAssertSupplyConserved_acceptsABurnOnceReconciled(t *testing.T) {
	f, at := shortEpochFixture(t)
	ctx := f.Ctx.WithBlockTime(at)
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))
	f.Bank.SimulateExternalBurn(testFeeCollectorName, math.NewInt(1_000))

	require.Error(t, f.Keeper.AssertSupplyConserved(ctx), "before the reconcile the drop is unexplained")
	require.NoError(t, f.Keeper.ReconcileBurns(ctx))
	require.NoError(t, f.Keeper.AssertSupplyConserved(ctx))
}

func TestAssertSupplyConserved_refusesAMintedTotalOffTheSchedule(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	f.Bank.SetGenesisSupply(math.NewInt(1))
	state, err := f.Keeper.EpochState.Get(f.Ctx)
	require.NoError(t, err)
	state.CumulativeMinted = math.NewInt(1)
	require.NoError(t, f.Keeper.EpochState.Set(f.Ctx, state))

	require.ErrorContains(t, f.Keeper.AssertSupplyConserved(f.Ctx), "cumulative_minted is not the emission schedule's")
}

func TestAssertSupplyConserved_freshGenesisHolds(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, nil)
	require.NoError(t, f.Keeper.AssertSupplyConserved(f.Ctx))
}

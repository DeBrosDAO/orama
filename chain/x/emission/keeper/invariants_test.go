package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

func TestCheckSupplyInvariant_holdsAfterNormalEpochClose(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})

	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	_, broken := f.Keeper.CheckSupplyInvariant(ctx)
	require.False(t, broken)
}

func TestCheckSupplyInvariant_breaksWhenSupplyDrifts(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})

	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	// Corrupt the bank supply behind x/emission's back (bypassing ReconcileBurns entirely) to
	// simulate a bug elsewhere; the invariant must catch the mismatch.
	f.Bank.supply = f.Bank.supply.AddRaw(1)

	_, broken := f.Keeper.CheckSupplyInvariant(ctx)
	require.True(t, broken)
}

func TestCheckSupplyInvariant_breaksWhenMintedDoesNotMatchScheduleExactly(t *testing.T) {
	f := newTestFixture(t)
	// InitGenesis itself now rejects a mismatched cumulative_minted (see
	// TestInitGenesis_rejectsInvalidGenesisState and TestInitGenesis_rejectsStateThatWouldBreakTheSupplyInvariant),
	// so to exercise CheckSupplyInvariant against a corrupted state, start from a valid genesis and
	// corrupt EpochState directly afterward, simulating a bug elsewhere that bypassed InitGenesis.
	f.initGenesis(t, nil)
	f.Bank.SetGenesisSupply(math.NewInt(1))
	state, err := f.Keeper.EpochState.Get(f.Ctx)
	require.NoError(t, err)
	// current_epoch=1 means 0 completed epochs, so the exact expected cumulative_minted is zero;
	// any positive cumulative_minted, however small, must now break the invariant (tightened from
	// "at most" to "exactly").
	state.CumulativeMinted = math.NewInt(1)
	require.NoError(t, f.Keeper.EpochState.Set(f.Ctx, state))

	_, broken := f.Keeper.CheckSupplyInvariant(f.Ctx)
	require.True(t, broken)
}

// TestCheckSupplyInvariant_simulated4000EpochRun closes 4,000 epochs back to back (spanning every
// halving boundary and well into the permanent tail) via AdvanceBlock (the actual BeginBlock path,
// not an internal-only helper) and checks the supply invariant after every single one, exactly as
// plans/open-network/track-c-chain.md C3 requires ("a 20-year simulation stays <= the table"). It
// runs entirely at the keeper level against an in-memory store, so it stays fast despite the
// epoch count.
func TestCheckSupplyInvariant_simulated4000EpochRun(t *testing.T) {
	f := newTestFixture(t)
	// epoch_duration=1s and min_blocks_per_epoch=1: each AdvanceBlock call below adds exactly one
	// block and one second, so exactly one epoch closes per call - keeping 4,000 epochs' worth of
	// calls fast against the in-memory store.
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(time.Second, 1, true)
	})

	ctx := f.Ctx
	unixTime := int64(1_700_000_000)

	const epochsToSimulate = 4000
	for epoch := uint64(1); epoch <= epochsToSimulate; epoch++ {
		unixTime++
		ctx = ctx.WithBlockTime(time.Unix(unixTime, 0))
		require.NoError(t, f.Keeper.AdvanceBlock(ctx))

		state, err := f.Keeper.EpochState.Get(ctx)
		require.NoError(t, err)
		require.Equal(t, epoch+1, state.CurrentEpoch, "epoch %d must have closed by now", epoch)

		detail, broken := f.Keeper.CheckSupplyInvariant(ctx)
		require.False(t, broken, "invariant broke at epoch %d: %s", epoch, detail)
	}

	state, err := f.Keeper.EpochState.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(epochsToSimulate+1), state.CurrentEpoch)

	// 12,657,924,000,000,000 norama is the exact validator share minted over 4,000 closed epochs:
	// 60% of CumulativeScheduleMax(3650) (12,600,384,000,000,000, all five halving brackets) plus
	// 350 tail epochs at 164,400,000,000 norama each (60% of 274 ORAMA) = 57,540,000,000,000.
	// Hard-coded here rather than computed via CumulativeValidatorMinted, which is exactly what
	// this run cross-checks against the keeper's own block-by-block accounting.
	wantCumulativeMinted := math.NewInt(12_657_924_000_000_000)
	require.True(t, state.CumulativeMinted.Equal(wantCumulativeMinted),
		"got %s, want %s", state.CumulativeMinted, wantCumulativeMinted)
	require.True(t, state.CumulativeMinted.Equal(types.CumulativeValidatorMinted(epochsToSimulate)))

	scheduleMaxAt4000 := types.CumulativeScheduleMax(epochsToSimulate)
	require.True(t, state.CumulativeMinted.LT(scheduleMaxAt4000),
		"the validator share (60%%) minted must sit strictly below the schedule's 100%% maximum")
}

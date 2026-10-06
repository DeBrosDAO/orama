package keeper_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission/keeper"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

func epochStateAt(startUnixNano int64, blocksInEpoch uint64) types.EpochState {
	gs := types.DefaultGenesisState()
	gs.EpochState.EpochStartUnixNano = startUnixNano
	gs.EpochState.BlocksInEpoch = blocksInEpoch
	return gs.EpochState
}

func TestShouldCloseEpoch_timeOnlyNotEnoughBlocks(t *testing.T) {
	f := newTestFixture(t)
	p := types.NewParams(30*time.Second, 5, true)
	state := epochStateAt(1_700_000_000_000000000, 2)

	// 40s have passed (enough time), but only 2 blocks (not enough blocks).
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_040, 0))
	require.False(t, keeper.ShouldCloseEpoch(ctx, p, state))
}

func TestShouldCloseEpoch_blocksOnlyNotEnoughTime(t *testing.T) {
	f := newTestFixture(t)
	p := types.NewParams(30*time.Second, 5, true)
	state := epochStateAt(1_700_000_000_000000000, 11)

	// 11 blocks have passed (enough blocks), but only 5s (not enough time).
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_005, 0))
	require.False(t, keeper.ShouldCloseEpoch(ctx, p, state))
}

func TestShouldCloseEpoch_neitherSatisfied(t *testing.T) {
	f := newTestFixture(t)
	p := types.NewParams(30*time.Second, 5, true)
	state := epochStateAt(1_700_000_000_000000000, 2)

	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_005, 0))
	require.False(t, keeper.ShouldCloseEpoch(ctx, p, state))
}

func TestShouldCloseEpoch_bothSatisfied(t *testing.T) {
	f := newTestFixture(t)
	p := types.NewParams(30*time.Second, 5, true)
	state := epochStateAt(1_700_000_000_000000000, 6)

	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.True(t, keeper.ShouldCloseEpoch(ctx, p, state))
}

func TestShouldCloseEpoch_exactBoundaryIsSatisfied(t *testing.T) {
	f := newTestFixture(t)
	p := types.NewParams(30*time.Second, 5, true)
	state := epochStateAt(1_700_000_000_000000000, 5)

	// Exactly 30s and exactly 5 blocks: both conditions use >=, so this must close.
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_030, 0))
	require.True(t, keeper.ShouldCloseEpoch(ctx, p, state))
}

func TestShouldCloseEpoch_subSecondPrecisionNotTruncated(t *testing.T) {
	f := newTestFixture(t)
	p := types.NewParams(30*time.Second, 5, true)
	// Epoch started at 1_700_000_000.900s. 30.000s later is 1_700_000_030.900s: at exactly
	// 1_700_000_030.999s (99ms past the boundary), the condition must already be true; at
	// 1_700_000_030.899s (1ms before it), it must still be false. A second-truncated clock would
	// get the second case wrong.
	state := epochStateAt(1_700_000_000_900000000, 5)

	notYet := f.Ctx.WithBlockTime(time.Unix(1_700_000_030, 899000000))
	require.False(t, keeper.ShouldCloseEpoch(notYet, p, state))

	justAfter := f.Ctx.WithBlockTime(time.Unix(1_700_000_030, 999000000))
	require.True(t, keeper.ShouldCloseEpoch(justAfter, p, state))
}

func TestAdvanceBlock_incrementsBlocksInEpochWithoutClosing(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 5, true)
	})

	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_001, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	state, err := f.Keeper.EpochState.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.BlocksInEpoch)
	require.Equal(t, uint64(1), state.CurrentEpoch, "epoch must not have closed yet")
	require.True(t, state.CumulativeMinted.IsZero())
}

func TestAdvanceBlock_mintsValidatorShareToFeeCollectorAndAdvancesState(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})

	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	// Epoch 1's validator share, hard-coded (see TestSplitEpochMint_epoch1TotalSplitsExactly).
	wantValidator := math.NewInt(8_908_800_000_000)

	got := f.Bank.GetSupply(ctx, params.BaseDenom)
	require.True(t, got.Amount.Equal(wantValidator), "fee collector must have received exactly the epoch 1 validator share")

	state, err := f.Keeper.EpochState.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.CurrentEpoch)
	require.Equal(t, uint64(0), state.BlocksInEpoch, "the block counter must reset when an epoch closes")
	require.Equal(t, int64(1_700_000_031)*int64(time.Second), state.EpochStartUnixNano)
	require.True(t, state.CumulativeMinted.Equal(wantValidator))
}

func TestAdvanceBlock_recordsCeilingsButDoesNotMintThem(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})

	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	record, err := f.Keeper.Ceilings.Get(ctx, 1)
	require.NoError(t, err)
	// Epoch 1's ceilings, hard-coded (see TestSplitEpochMint_epoch1TotalSplitsExactly).
	require.True(t, record.StorageCeiling.Equal(math.NewInt(3_712_000_000_000)))
	require.True(t, record.RelayCeiling.Equal(math.NewInt(1_484_800_000_000)))
	require.True(t, record.DevelopmentCeiling.Equal(math.NewInt(742_400_000_000)))
	require.True(t, record.ValidatorMinted.Equal(math.NewInt(8_908_800_000_000)))

	// Only the validator share was minted; the ceilings never touched bank supply.
	totalSupply := f.Bank.GetSupply(ctx, params.BaseDenom).Amount
	require.True(t, totalSupply.Equal(math.NewInt(8_908_800_000_000)))
}

func TestAdvanceBlock_prunesCeilingsOutsideWindow(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(time.Second, 1, true)
	})

	ctx := f.Ctx
	unixSec := int64(1_700_000_000)
	for i := uint64(1); i <= types.CeilingWindow+5; i++ {
		unixSec++
		ctx = ctx.WithBlockTime(time.Unix(unixSec, 0))
		require.NoError(t, f.Keeper.AdvanceBlock(ctx))
	}

	// Epoch 1 is now well outside the trailing window and must have been pruned.
	has, err := f.Keeper.Ceilings.Has(ctx, 1)
	require.NoError(t, err)
	require.False(t, has, "epoch 1's ceiling record should have been pruned")

	lastEpoch := uint64(types.CeilingWindow + 5)
	has, err = f.Keeper.Ceilings.Has(ctx, lastEpoch)
	require.NoError(t, err)
	require.True(t, has)
}

func TestAdvanceBlock_noCatchUpAfterLongGap(t *testing.T) {
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(30*time.Second, 1, true)
	})

	// Close epoch 1 normally.
	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_031, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	// Simulate a huge gap: 100 days pass before the next block is even produced. Even though far
	// more than one epoch's worth of time has elapsed, AdvanceBlock must still advance the epoch
	// counter by exactly one, and the new epoch must start "now" rather than being backdated to
	// when it would have originally closed.
	farLaterUnix := int64(1_700_000_031) + 100*24*60*60
	ctxLater := ctx.WithBlockTime(time.Unix(farLaterUnix, 0))
	require.NoError(t, f.Keeper.AdvanceBlock(ctxLater))

	state, err := f.Keeper.EpochState.Get(ctxLater)
	require.NoError(t, err)
	require.Equal(t, uint64(3), state.CurrentEpoch, "must close exactly epoch 2, not skip ahead")
	require.Equal(t, uint64(0), state.BlocksInEpoch)
	require.Equal(t, farLaterUnix*int64(time.Second), state.EpochStartUnixNano, "the next epoch must start now, not be backdated")

	// Epoch 1 + epoch 2's validator shares, both hard-coded.
	wantMinted := math.NewInt(8_908_800_000_000 + 8_908_800_000_000)
	require.True(t, state.CumulativeMinted.Equal(wantMinted))
}

func TestAdvanceBlock_noCatchUpAcrossManyMissedEpochs(t *testing.T) {
	// A gap long enough to have "fit" 100 epochs of the configured duration must still close
	// exactly one epoch per AdvanceBlock call - not 100 - proving there is no catch-up loop
	// anywhere in the BeginBlock path (only ever one CloseEpoch-equivalent transition per call).
	f := newTestFixture(t)
	f.initGenesis(t, func(gs *types.GenesisState) {
		gs.Params = types.NewParams(time.Second, 1, true)
	})

	ctx := f.Ctx.WithBlockTime(time.Unix(1_700_000_100, 0)) // 100s later: "100 epochs" worth of time
	require.NoError(t, f.Keeper.AdvanceBlock(ctx))

	state, err := f.Keeper.EpochState.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.CurrentEpoch, "exactly one epoch must close, never a catch-up loop")
}

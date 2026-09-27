package keeper

import (
	"fmt"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// ShouldCloseEpoch reports whether the epoch in progress satisfies both closing conditions: at
// least Params.EpochDuration of BFT time has passed since it started, and at least
// Params.MinBlocksPerEpoch blocks have been produced since it started
// (plans/open-network/track-c-chain.md C3). It reads state.BlocksInEpoch and
// state.EpochStartUnixNano rather than the chain's absolute block height/genesis time, so it
// keeps working correctly across a chain export and re-import at a fresh height (see
// EpochState.BlocksInEpoch's doc comment).
func ShouldCloseEpoch(ctx sdk.Context, p types.Params, state types.EpochState) bool {
	elapsed := ctx.BlockTime().Sub(time.Unix(0, state.EpochStartUnixNano))
	return elapsed >= p.EpochDuration() && state.BlocksInEpoch >= p.MinBlocksPerEpoch
}

// AdvanceBlock runs once per BeginBlock: it counts this block toward the epoch in progress and,
// if that satisfies both closing conditions, closes exactly one epoch. It never closes more than
// one epoch per call. Missed epochs are never caught up: if a long gap has passed, the very next
// call still only starts the next epoch from "now", so the epoch counter (and therefore the
// halving schedule) advances by exactly one regardless of how much wall-clock time or how many
// blocks the gap took (plans/open-network/track-c-chain.md C3, "Missed epochs are skipped, not
// caught up").
func (k Keeper) AdvanceBlock(ctx sdk.Context) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load emission params: %w", err)
	}
	state, err := k.EpochState.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load emission epoch state: %w", err)
	}

	state.BlocksInEpoch++

	if !ShouldCloseEpoch(ctx, p, state) {
		if err := k.EpochState.Set(ctx, state); err != nil {
			return fmt.Errorf("failed to record emission block count: %w", err)
		}
		return nil
	}

	if err := k.closeEpoch(ctx, state); err != nil {
		return fmt.Errorf("failed to close emission epoch %d: %w", state.CurrentEpoch, err)
	}
	return nil
}

// closeEpoch closes the epoch currently in progress. It:
//  1. mints the epoch's validator/delegator share and forwards it to the fee collector, so
//     x/distribution's existing BeginBlocker pays it out on capped power;
//  2. records the epoch's non-minted storage/relay/development ceilings for a later module to
//     claim, and prunes any ceiling record that has fallen outside the trailing window;
//  3. advances EpochState to the next epoch, starting now, with BlocksInEpoch reset to zero.
func (k Keeper) closeEpoch(ctx sdk.Context, state types.EpochState) error {
	closingEpoch := state.CurrentEpoch
	maxMint := types.MaxMintableForEpoch(closingEpoch)
	split := types.SplitEpochMint(maxMint)

	if split.Validator.IsPositive() {
		coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, split.Validator))
		if err := k.bankKeeper.MintCoins(ctx, types.ModuleName, coins); err != nil {
			return fmt.Errorf("failed to mint epoch %d validator share of %s: %w", closingEpoch, coins, err)
		}
		if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.ModuleName, k.feeCollectorName, coins); err != nil {
			return fmt.Errorf("failed to forward epoch %d validator share to the %s module: %w", closingEpoch, k.feeCollectorName, err)
		}
	}

	record := types.CeilingRecord{
		Epoch:              closingEpoch,
		StorageCeiling:     split.Storage,
		RelayCeiling:       split.Relay,
		DevelopmentCeiling: split.Development,
		ValidatorMinted:    split.Validator,
	}
	if err := k.Ceilings.Set(ctx, closingEpoch, record); err != nil {
		return fmt.Errorf("failed to record epoch %d ceilings: %w", closingEpoch, err)
	}
	if err := k.pruneCeilings(ctx, closingEpoch); err != nil {
		return err
	}

	state.CumulativeMinted = state.CumulativeMinted.Add(split.Validator)
	state.CurrentEpoch = closingEpoch + 1
	state.EpochStartUnixNano = ctx.BlockTime().UnixNano()
	state.BlocksInEpoch = 0
	if err := k.EpochState.Set(ctx, state); err != nil {
		return fmt.Errorf("failed to advance emission epoch state past epoch %d: %w", closingEpoch, err)
	}

	k.Logger(ctx).Info(
		"emission epoch closed",
		"epoch", closingEpoch,
		"validator_minted", split.Validator.String(),
		"storage_ceiling", split.Storage.String(),
		"relay_ceiling", split.Relay.String(),
		"development_ceiling", split.Development.String(),
	)

	return nil
}

// pruneCeilings removes the ceiling record that has just fallen outside the trailing
// types.CeilingWindow, if one exists.
func (k Keeper) pruneCeilings(ctx sdk.Context, closingEpoch uint64) error {
	if closingEpoch <= types.CeilingWindow {
		return nil
	}
	old := closingEpoch - types.CeilingWindow
	has, err := k.Ceilings.Has(ctx, old)
	if err != nil {
		return fmt.Errorf("failed to check emission ceiling record for epoch %d: %w", old, err)
	}
	if !has {
		return nil
	}
	if err := k.Ceilings.Remove(ctx, old); err != nil {
		return fmt.Errorf("failed to prune emission ceiling record for epoch %d: %w", old, err)
	}
	return nil
}

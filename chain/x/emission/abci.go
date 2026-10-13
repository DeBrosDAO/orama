package emission

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/emission/keeper"
)

// BeginBlocker counts this block toward the epoch in progress and, if that satisfies both closing
// conditions, closes exactly one epoch (plans/open-network/track-c-chain.md C3). It never closes
// more than one epoch per block: a long gap since the last check still only advances the epoch
// counter by one.
func BeginBlocker(ctx context.Context, k keeper.Keeper) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if err := k.AdvanceBlock(sdkCtx); err != nil {
		return fmt.Errorf("failed to advance emission epoch: %w", err)
	}
	return nil
}

// EndBlocker reconciles x/emission's cumulative_burned against any drop in bank supply that
// happened elsewhere this block (e.g. an x/slashing burn), so the supply invariant keeps holding
// without x/emission needing a direct dependency on x/slashing (see keeper.Keeper.ReconcileBurns).
func EndBlocker(ctx context.Context, k keeper.Keeper) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if err := k.ReconcileBurns(sdkCtx); err != nil {
		return fmt.Errorf("failed to reconcile emission burns: %w", err)
	}
	return nil
}

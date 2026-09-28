package power

import (
	"context"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/keeper"
	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// EndBlocker runs x/power's end-blocker: it is the ONLY source of CometBFT ValidatorUpdates this
// app returns (plans/open-network/track-c-chain.md C4). x/staking's own EndBlock still runs (see
// app.stakingEndBlockOverride) for its bonding/unbonding bookkeeping, but its returned updates are
// discarded there rather than reaching CometBFT.
func EndBlocker(ctx context.Context, k keeper.Keeper, emissionKeeper types.EmissionKeeper) ([]abci.ValidatorUpdate, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	updates, err := k.RunEndBlock(sdkCtx, emissionKeeper)
	if err != nil {
		return nil, fmt.Errorf("failed to run power end block: %w", err)
	}
	return updates, nil
}

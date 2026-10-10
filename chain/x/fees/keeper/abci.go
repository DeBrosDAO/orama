package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// AdvanceBaseFee runs once per EndBlock: it recomputes the per-gas-unit base fee for the NEXT
// block from how full THIS block was (plans/open-network/track-c-chain.md C2's EIP-1559-style
// rule), using ctx.BlockGasMeter() (the cumulative gas every tx in this block has consumed) against
// the block's configured max gas. If no max gas is configured (CometBFT's convention: <= 0 means
// unlimited), there is no meaningful "fullness" to react to and the base fee is left unchanged.
func (k Keeper) AdvanceBaseFee(ctx sdk.Context) error {
	blockParams := ctx.ConsensusParams().Block
	if blockParams == nil || blockParams.MaxGas <= 0 {
		return nil
	}
	maxGas := blockParams.MaxGas

	p, err := k.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load fees params: %w", err)
	}
	current, err := k.BaseFee.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load base fee: %w", err)
	}

	gasUsed := ctx.BlockGasMeter().GasConsumed()
	next := types.NextBaseFee(current, gasUsed, uint64(maxGas), p)
	if next.Equal(current) {
		return nil
	}
	if err := k.BaseFee.Set(ctx, next); err != nil {
		return fmt.Errorf("failed to set base fee: %w", err)
	}
	k.Logger(ctx).Debug("base fee adjusted", "gas_used", gasUsed, "max_gas", maxGas, "previous", current.String(), "next", next.String())
	return nil
}

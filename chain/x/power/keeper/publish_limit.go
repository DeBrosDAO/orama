package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// limitPublishedIncreases caps this block's normalized shares so an increase
// moves at most MaxVotingPowerShiftPerEpoch from the power last returned to
// CometBFT. Stake, ramp, and lambda can otherwise jump the published set even
// when the lambda step itself is within the bound.
func (k Keeper) limitPublishedIncreases(ctx sdk.Context, entries []validatorEntry, shares []math.LegacyDec) ([]math.LegacyDec, error) {
	desired := make(map[string]math.LegacyDec, len(entries))
	for i, entry := range entries {
		desired[entry.operatorAddr] = shares[i]
	}
	prev, err := k.previousPowerShares(ctx)
	if err != nil {
		return nil, err
	}
	clamped := types.ClampPublishedIncreases(prev, desired, types.MaxVotingPowerShiftPerEpoch())
	out := make([]math.LegacyDec, len(entries))
	for i, entry := range entries {
		share, ok := clamped[entry.operatorAddr]
		if !ok || share.IsNil() {
			out[i] = math.LegacyZeroDec()
			continue
		}
		out[i] = share
	}
	return out, nil
}

// previousPowerShares normalizes the CometBFT powers stored at the previous block.
func (k Keeper) previousPowerShares(ctx sdk.Context) (map[string]math.LegacyDec, error) {
	weights := map[string]int64{}
	total := int64(0)
	err := k.LastPower.Walk(ctx, nil, func(addr string, power int64) (bool, error) {
		if power <= 0 {
			return false, nil
		}
		weights[addr] = power
		total += power
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read the previous voting powers: %w", err)
	}
	out := make(map[string]math.LegacyDec, len(weights))
	if total <= 0 {
		return out, nil
	}
	scale := math.LegacyNewDec(total)
	for addr, power := range weights {
		out[addr] = math.LegacyNewDec(power).Quo(scale)
	}
	return out, nil
}

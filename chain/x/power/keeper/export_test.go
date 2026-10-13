package keeper

import (
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// PowerShares returns every validator's normalized power share for the current block, by valoper
// address,. It writes what computePowers writes for
// a reward walk (ramp clocks) but never advances lambda or the cap.
func (k Keeper) PowerShares(ctx sdk.Context, emission types.EmissionKeeper) (map[string]math.LegacyDec, error) {
	_, lambda, entries, err := k.computePowers(ctx, emission, false)
	if err != nil {
		return nil, err
	}
	shares := k.normalizedShares(entries, lambda)
	out := make(map[string]math.LegacyDec, len(entries))
	for i, e := range entries {
		out[e.operatorAddr] = shares[i]
	}
	return out, nil
}

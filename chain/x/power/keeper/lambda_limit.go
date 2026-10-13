package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// limitLambdaStep clamps a just-computed lambda so the normalized voting-power
// distribution moves by at most MaxVotingPowerShiftPerEpoch from the previous
// epoch's lambda. The unconstrained value is already stored; this overwrites
// it when the step is too large. A later call in the same epoch sees the
// clamped value and does not advance again.
func (k Keeper) limitLambdaStep(ctx sdk.Context, prev, candidate math.LegacyDec, entries []validatorEntry) (math.LegacyDec, error) {
	if !candidate.GT(prev) {
		return candidate, nil
	}
	bootstrap := make([]math.LegacyDec, len(entries))
	published := make([]math.LegacyDec, len(entries))
	latent := make([]math.LegacyDec, len(entries))
	for i, e := range entries {
		bootstrap[i] = e.bootstrap
		published[i] = e.rampedCapped
		latent[i] = e.unrampedCapped
	}
	clamped := types.ClampLambdaStepBoth(prev, candidate, bootstrap, published, latent, types.MaxVotingPowerShiftPerEpoch())
	if clamped.Equal(candidate) {
		return candidate, nil
	}
	if err := k.Lambda.Set(ctx, clamped); err != nil {
		return math.LegacyDec{}, fmt.Errorf("failed to store the rate-limited lambda: %w", err)
	}
	k.Logger(ctx).Info(
		"power lambda step rate-limited",
		"unconstrained", candidate.String(),
		"lambda", clamped.String(),
		"max_shift", types.MaxVotingPowerShiftPerEpoch().String(),
	)
	return clamped, nil
}

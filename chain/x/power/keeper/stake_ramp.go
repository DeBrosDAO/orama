package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// effectiveBond is the token amount ComputeCappedShares should see.
// Tokens already admitted stay admitted. An increase ramps from zero over
// RampEpochs instead of joining C_i in the block it arrives. A decrease is
// applied immediately. persist is true only for RunEndBlock.
func (k Keeper) effectiveBond(ctx sdk.Context, addr string, current math.Int, epoch, rampEpochs uint64, persist bool) (math.Int, error) {
	admitted, err := k.rampInt(ctx, addr, true)
	if err != nil {
		return math.Int{}, err
	}
	excess, err := k.rampInt(ctx, addr, false)
	if err != nil {
		return math.Int{}, err
	}
	excessEpoch, hasEpoch, err := k.rampEpoch(ctx, addr)
	if err != nil {
		return math.Int{}, err
	}

	if !current.IsPositive() || rampEpochs == 0 {
		admitted = current
		if !current.IsPositive() {
			admitted = math.ZeroInt()
		}
		return k.writeBond(ctx, addr, admitted, math.ZeroInt(), 0, false, persist)
	}

	if excess.IsPositive() && hasEpoch && epoch >= excessEpoch && epoch-excessEpoch >= rampEpochs {
		admitted = admitted.Add(excess)
		excess = math.ZeroInt()
		hasEpoch = false
	}
	if current.LT(admitted) {
		admitted = current
		excess = math.ZeroInt()
		hasEpoch = false
	}
	gap := current.Sub(admitted)
	if gap.GT(excess) {
		// Vest the progress of the open cohort before starting a new one.
		// Resetting the whole excess would publish zero and drop voting power
		// that this validator had already earned.
		if excess.IsPositive() && hasEpoch {
			factor := types.RampFactor(excessEpoch, epoch, rampEpochs)
			vested := excess.ToLegacyDec().Mul(factor).TruncateInt()
			if vested.IsPositive() {
				admitted = admitted.Add(vested)
			}
		}
		gap = current.Sub(admitted)
		excess = gap
		excessEpoch = epoch
		hasEpoch = true
	} else if gap.LT(excess) {
		excess = gap
	}

	effective := admitted
	if excess.IsPositive() && hasEpoch {
		factor := types.RampFactor(excessEpoch, epoch, rampEpochs)
		effective = admitted.Add(excess.ToLegacyDec().Mul(factor).TruncateInt())
	}
	if _, err := k.writeBond(ctx, addr, admitted, excess, excessEpoch, hasEpoch, persist); err != nil {
		return math.Int{}, err
	}
	return effective, nil
}

func (k Keeper) rampInt(ctx sdk.Context, addr string, admitted bool) (math.Int, error) {
	var value math.Int
	var err error
	if admitted {
		value, err = k.RampAdmitted.Get(ctx, addr)
	} else {
		value, err = k.RampExcess.Get(ctx, addr)
	}
	if err == nil {
		if value.IsNil() {
			return math.ZeroInt(), nil
		}
		return value, nil
	}
	if isNotFound(err) {
		return math.ZeroInt(), nil
	}
	return math.Int{}, err
}

func (k Keeper) rampEpoch(ctx sdk.Context, addr string) (uint64, bool, error) {
	epoch, err := k.RampExcessEpoch.Get(ctx, addr)
	if err == nil {
		return epoch, true, nil
	}
	if isNotFound(err) {
		return 0, false, nil
	}
	return 0, false, err
}

// clearStakeRamp drops the token ramp for an operator who left the bonded set.
// A later rebond starts from zero instead of keeping stake that was admitted
// before the validator unbonded, jailed, or was tombstoned.
func (k Keeper) clearStakeRamp(ctx sdk.Context, addr string) error {
	if err := k.RampAdmitted.Remove(ctx, addr); err != nil && !isNotFound(err) {
		return fmt.Errorf("failed to clear admitted bond for %q: %w", addr, err)
	}
	if err := k.RampExcess.Remove(ctx, addr); err != nil && !isNotFound(err) {
		return fmt.Errorf("failed to clear ramping bond for %q: %w", addr, err)
	}
	if err := k.RampExcessEpoch.Remove(ctx, addr); err != nil && !isNotFound(err) {
		return fmt.Errorf("failed to clear ramp epoch for %q: %w", addr, err)
	}
	return nil
}

func (k Keeper) writeBond(ctx sdk.Context, addr string, admitted, excess math.Int, excessEpoch uint64, hasEpoch, persist bool) (math.Int, error) {
	effective := admitted
	if !persist {
		return effective, nil
	}
	if err := k.RampAdmitted.Set(ctx, addr, admitted); err != nil {
		return math.Int{}, fmt.Errorf("failed to store admitted bond for %q: %w", addr, err)
	}
	if excess.IsPositive() {
		if err := k.RampExcess.Set(ctx, addr, excess); err != nil {
			return math.Int{}, fmt.Errorf("failed to store ramping bond for %q: %w", addr, err)
		}
		if hasEpoch {
			if err := k.RampExcessEpoch.Set(ctx, addr, excessEpoch); err != nil {
				return math.Int{}, fmt.Errorf("failed to store ramp epoch for %q: %w", addr, err)
			}
		}
		return effective, nil
	}
	if err := k.RampExcess.Remove(ctx, addr); err != nil && !isNotFound(err) {
		return math.Int{}, fmt.Errorf("failed to clear ramping bond for %q: %w", addr, err)
	}
	if err := k.RampExcessEpoch.Remove(ctx, addr); err != nil && !isNotFound(err) {
		return math.Int{}, fmt.Errorf("failed to clear ramp epoch for %q: %w", addr, err)
	}
	return effective, nil
}

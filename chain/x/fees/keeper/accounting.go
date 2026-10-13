package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
)

// feeTotal reads one fee-accounting counter, treating a missing key as zero.
func (k Keeper) feeTotal(ctx context.Context, item collections.Item[math.Int]) (math.Int, error) {
	value, err := item.Get(ctx)
	if err == nil {
		if value.IsNil() {
			return math.ZeroInt(), nil
		}
		return value, nil
	}
	if errors.Is(err, collections.ErrNotFound) {
		return math.ZeroInt(), nil
	}
	return math.Int{}, err
}

// recordFeeSettlement adds one settled transaction to the fee-accounting counters.
// collected is the fee taken from the payer, burned is the part destroyed, and
// distributed is the part credited to the proposer. The three stay in the
// relation burned + distributed == collected.
func (k Keeper) recordFeeSettlement(ctx context.Context, collected, burned, distributed math.Int) error {
	if err := k.addFeeTotal(ctx, k.Collected, collected); err != nil {
		return fmt.Errorf("failed to record collected fees: %w", err)
	}
	if err := k.addFeeTotal(ctx, k.Burned, burned); err != nil {
		return fmt.Errorf("failed to record burned fees: %w", err)
	}
	if err := k.addFeeTotal(ctx, k.Distributed, distributed); err != nil {
		return fmt.Errorf("failed to record distributed fees: %w", err)
	}
	return nil
}

func (k Keeper) addFeeTotal(ctx context.Context, item collections.Item[math.Int], delta math.Int) error {
	current, err := k.feeTotal(ctx, item)
	if err != nil {
		return err
	}
	if delta.IsNil() || delta.IsZero() {
		return item.Set(ctx, current)
	}
	return item.Set(ctx, current.Add(delta))
}

package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
)

// StorageCeiling is the storage share recorded for a closed epoch.
// A missing epoch is zero, not an error: the epoch has not closed.
func (k Keeper) StorageCeiling(ctx context.Context, epoch uint64) (math.Int, error) {
	rec, err := k.Ceilings.Get(ctx, epoch)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return math.ZeroInt(), nil
		}
		return math.Int{}, fmt.Errorf("load ceiling for epoch %d: %w", epoch, err)
	}
	if rec.StorageCeiling.IsNil() {
		return math.ZeroInt(), nil
	}
	return rec.StorageCeiling, nil
}

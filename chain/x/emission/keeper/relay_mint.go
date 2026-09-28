package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// RelayCeiling is the relay share recorded for a closed epoch.
func (k Keeper) RelayCeiling(ctx context.Context, epoch uint64) (math.Int, error) {
	rec, err := k.Ceilings.Get(ctx, epoch)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return math.Int{}, fmt.Errorf("no relay ceiling for epoch %d", epoch)
		}
		return math.Int{}, fmt.Errorf("load relay ceiling for epoch %d: %w", epoch, err)
	}
	if rec.RelayCeiling.IsNil() {
		return math.Int{}, fmt.Errorf("epoch %d relay ceiling is unset", epoch)
	}
	return rec.RelayCeiling, nil
}

// MintRelayReward mints amt of norama into the relay module account against one
// closed epoch's relay ceiling, minus what this method has already minted.
// A second call that would pass the ceiling is refused and writes nothing.
func (k Keeper) MintRelayReward(ctx context.Context, epoch uint64, amt math.Int) error {
	if amt.IsNil() || !amt.IsPositive() {
		return fmt.Errorf("relay reward must be a positive amount, got %s", amt)
	}
	record, err := k.Ceilings.Get(ctx, epoch)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("no relay ceiling for epoch %d", epoch)
		}
		return fmt.Errorf("load relay ceiling for epoch %d: %w", epoch, err)
	}
	if record.RelayCeiling.IsNil() || !record.RelayCeiling.IsPositive() {
		return fmt.Errorf("epoch %d has no relay ceiling", epoch)
	}
	already := record.RelayMinted
	if already.IsNil() {
		already = math.ZeroInt()
	}
	remaining := record.RelayCeiling.Sub(already)
	if amt.GT(remaining) {
		return fmt.Errorf("relay reward %s exceeds epoch %d remaining ceiling %s", amt, epoch, remaining)
	}
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amt))
	if err := k.bankKeeper.MintCoins(ctx, relaytypes.ModuleName, coins); err != nil {
		return fmt.Errorf("mint relay reward: %w", err)
	}
	record.RelayMinted = already.Add(amt)
	if err := k.Ceilings.Set(ctx, epoch, record); err != nil {
		return fmt.Errorf("record relay mint for epoch %d: %w", epoch, err)
	}
	return nil
}

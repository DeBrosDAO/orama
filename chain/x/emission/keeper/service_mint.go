package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// MintStorageService mints amt of norama into the storage module account
// against one closed epoch's storage ceiling, minus what this method has
// already minted for that epoch. A call that would pass the ceiling is
// refused and writes nothing. The mint is counted in
// cumulative_service_minted so the supply invariant holds.
func (k Keeper) MintStorageService(ctx context.Context, epoch uint64, amt math.Int) error {
	if amt.IsNil() || !amt.IsPositive() {
		return fmt.Errorf("storage payment must be a positive amount, got %s", amt)
	}
	record, err := k.Ceilings.Get(ctx, epoch)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("no storage ceiling for epoch %d", epoch)
		}
		return fmt.Errorf("load storage ceiling for epoch %d: %w", epoch, err)
	}
	already := nonNilInt(record.StorageMinted)
	remaining := nonNilInt(record.StorageCeiling).Sub(already)
	if amt.GT(remaining) {
		return fmt.Errorf("storage payment %s exceeds epoch %d remaining ceiling %s", amt, epoch, remaining)
	}
	if err := k.mintTo(ctx, storagetypes.ModuleName, amt); err != nil {
		return fmt.Errorf("mint storage payment: %w", err)
	}
	record.StorageMinted = already.Add(amt)
	if err := k.Ceilings.Set(ctx, epoch, record); err != nil {
		return fmt.Errorf("record storage mint for epoch %d: %w", epoch, err)
	}
	return k.recordServiceMint(ctx, amt)
}

// mintTo mints amt into x/emission's account, the only norama minter, and
// moves it to recipient.
func (k Keeper) mintTo(ctx context.Context, recipient string, amt math.Int) error {
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amt))
	if err := k.bankKeeper.MintCoins(ctx, types.ModuleName, coins); err != nil {
		return err
	}
	return k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.ModuleName, recipient, coins)
}

// recordServiceMint adds a storage or relay mint to cumulative_service_minted.
func (k Keeper) recordServiceMint(ctx context.Context, amt math.Int) error {
	state, err := k.EpochState.Get(ctx)
	if err != nil {
		return fmt.Errorf("load emission epoch state: %w", err)
	}
	state.CumulativeServiceMinted = nonNilInt(state.CumulativeServiceMinted).Add(amt)
	if err := k.EpochState.Set(ctx, state); err != nil {
		return fmt.Errorf("record %s norama service mint: %w", amt, err)
	}
	return nil
}

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
)

// MintDevelopmentSpend mints amount of norama into x/emission's module account
// against one closed epoch's development ceiling (5% of that epoch's schedule
// maximum), minus what this method has already minted for that epoch.
//
// It refuses any other amount: zero, negative, an unknown or pruned epoch, a
// ceiling record that is not the schedule's 5% share, or an amount above the
// remainder. It does not mint the validator, storage or relay shares, and it
// does not change the schedule. On success the coins sit in the emission
// module account; the caller pays the recipient. On error it has written
// nothing.
func (k Keeper) MintDevelopmentSpend(ctx context.Context, epoch uint64, amount math.Int) (string, error) {
	if amount.IsNil() || !amount.IsPositive() {
		return "", fmt.Errorf("development spend must be a positive amount, got %s", amount)
	}
	record, err := k.Ceilings.Get(ctx, epoch)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return "", fmt.Errorf("no development ceiling for epoch %d", epoch)
		}
		return "", fmt.Errorf("failed to load development ceiling for epoch %d: %w", epoch, err)
	}
	want := types.SplitEpochMint(types.MaxMintableForEpoch(epoch)).Development
	if !record.DevelopmentCeiling.Equal(want) {
		return "", fmt.Errorf("epoch %d development ceiling %s is not the 5%% schedule share %s", epoch, record.DevelopmentCeiling, want)
	}
	already := nonNilInt(record.DevelopmentMinted)
	remaining := record.DevelopmentCeiling.Sub(already)
	if amount.GT(remaining) {
		return "", fmt.Errorf("development spend %s exceeds epoch %d remaining ceiling %s (ceiling %s, already minted %s)", amount, epoch, remaining, record.DevelopmentCeiling, already)
	}

	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))
	if err := k.bankKeeper.MintCoins(ctx, types.ModuleName, coins); err != nil {
		return "", fmt.Errorf("failed to mint development spend of %s: %w", amount, err)
	}
	record.DevelopmentMinted = already.Add(amount)
	if err := k.Ceilings.Set(ctx, epoch, record); err != nil {
		return "", fmt.Errorf("failed to record development mint for epoch %d: %w", epoch, err)
	}
	state, err := k.EpochState.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to load emission epoch state: %w", err)
	}
	state.CumulativeDevelopmentMinted = nonNilInt(state.CumulativeDevelopmentMinted).Add(amount)
	if err := k.EpochState.Set(ctx, state); err != nil {
		return "", fmt.Errorf("failed to record cumulative development mint: %w", err)
	}
	return types.ModuleName, nil
}

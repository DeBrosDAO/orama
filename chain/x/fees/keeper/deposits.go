package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// LockDeposit locks amount of a caller-assigned, globally unique deposit id from owner's own bank
// balance into x/fees's dedicated deposits module account (plans/open-network/track-c-chain.md C2
// "State deposits"). No module consumes this API yet (x/token, x/cnft, x/market, x/nodes and
// CosmWasm storage all will, once built); it exists now so they can all share one tested
// lock/refund/burn implementation instead of reinventing it.
func (k Keeper) LockDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error {
	if !amount.IsPositive() {
		return fmt.Errorf("deposit amount must be positive, got %s", amount)
	}
	has, err := k.Deposits.Has(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to check deposit id %q: %w", id, err)
	}
	if has {
		return fmt.Errorf("deposit id %q already exists", id)
	}

	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, amount))
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, owner, types.DepositsModuleName, coins); err != nil {
		return fmt.Errorf("failed to lock deposit %q from %s: %w", id, owner, err)
	}
	if err := k.Deposits.Set(ctx, id, types.Deposit{Id: id, Owner: owner.String(), Amount: amount}); err != nil {
		return fmt.Errorf("failed to record deposit %q: %w", id, err)
	}
	return nil
}

// ReleaseDeposit releases a locked deposit, refunding Params.DepositRefundFraction of it to the
// owner's earnings account and burning the rest (plans/open-network/track-c-chain.md C2: "99% is
// refunded to earnings, and 1% is burned"). It returns the refund and burn amounts.
func (k Keeper) ReleaseDeposit(ctx context.Context, id string) (refund, burn math.Int, err error) {
	deposit, err := k.Deposits.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return math.Int{}, math.Int{}, fmt.Errorf("deposit id %q does not exist", id)
		}
		return math.Int{}, math.Int{}, fmt.Errorf("failed to load deposit %q: %w", id, err)
	}
	owner, err := sdk.AccAddressFromBech32(deposit.Owner)
	if err != nil {
		return math.Int{}, math.Int{}, fmt.Errorf("deposit %q has an invalid owner %q: %w", id, deposit.Owner, err)
	}
	p, err := k.Params.Get(ctx)
	if err != nil {
		return math.Int{}, math.Int{}, fmt.Errorf("failed to load fees params: %w", err)
	}

	refund, burn = types.SplitDeposit(deposit.Amount, p)

	if refund.IsPositive() {
		refundCoins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, refund))
		if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.DepositsModuleName, types.ModuleName, refundCoins); err != nil {
			return math.Int{}, math.Int{}, fmt.Errorf("failed to move deposit %q refund into earnings: %w", id, err)
		}
		if err := k.creditLedgerOnly(ctx, owner, refund); err != nil {
			return math.Int{}, math.Int{}, err
		}
	}
	if burn.IsPositive() {
		burnCoins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, burn))
		if err := k.bankKeeper.BurnCoins(ctx, types.DepositsModuleName, burnCoins); err != nil {
			return math.Int{}, math.Int{}, fmt.Errorf("failed to burn deposit %q's burn share: %w", id, err)
		}
	}

	if err := k.Deposits.Remove(ctx, id); err != nil {
		return math.Int{}, math.Int{}, fmt.Errorf("failed to remove deposit %q: %w", id, err)
	}

	return refund, burn, nil
}

// GetDeposit returns the open deposit with the given id.
func (k Keeper) GetDeposit(ctx context.Context, id string) (types.Deposit, error) {
	return k.Deposits.Get(ctx, id)
}

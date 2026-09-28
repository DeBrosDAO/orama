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

	if err := k.fundDeposit(ctx, owner, id, amount); err != nil {
		return err
	}
	if err := k.Deposits.Set(ctx, id, types.Deposit{Id: id, Owner: owner.String(), Amount: amount}); err != nil {
		return fmt.Errorf("failed to record deposit %q: %w", id, err)
	}
	return nil
}

// fundDeposit moves amount into the deposits module account, taking the owner's bank balance
// first and the shortfall from that same owner's earnings (C2: earnings can fund the signer's
// own deposits). It checks both balances before moving anything.
func (k Keeper) fundDeposit(ctx context.Context, owner sdk.AccAddress, id string, amount math.Int) error {
	spendable := k.bankKeeper.SpendableCoins(ctx, owner).AmountOf(params.BaseDenom)
	fromBank := amount
	if fromBank.GT(spendable) {
		fromBank = spendable
	}
	fromEarnings := amount.Sub(fromBank)
	if fromEarnings.IsPositive() {
		balance, err := k.GetEarnings(ctx, owner)
		if err != nil {
			return err
		}
		if balance.LT(fromEarnings) {
			return fmt.Errorf(
				"insufficient funds to lock deposit %q: need %s%s, have %s%s spendable and %s%s in earnings",
				id, amount, params.BaseDenom, spendable, params.BaseDenom, balance, params.BaseDenom,
			)
		}
	}
	if fromBank.IsPositive() {
		coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, fromBank))
		if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, owner, types.DepositsModuleName, coins); err != nil {
			return fmt.Errorf("failed to lock deposit %q from %s's bank balance: %w", id, owner, err)
		}
	}
	if !fromEarnings.IsPositive() {
		return nil
	}
	debited, err := k.DebitEarningsUpTo(ctx, owner, fromEarnings)
	if err != nil {
		return err
	}
	if debited.LT(fromEarnings) {
		return fmt.Errorf("deposit %q: earnings debit %s was short of the %s shortfall", id, debited, fromEarnings)
	}
	coins := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, fromEarnings))
	if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.ModuleName, types.DepositsModuleName, coins); err != nil {
		return fmt.Errorf("failed to move deposit %q out of earnings: %w", id, err)
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

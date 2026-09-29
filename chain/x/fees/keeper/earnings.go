package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// CreditEarnings moves amt from senderModule's own account into x/fees's module account and
// credits it to addr's earnings ledger entry (plans/open-network/track-c-chain.md C2: "every
// protocol payout lands in the recipient's earnings account ... credited automatically every
// epoch"). It implements power/types.EarningsKeeper for x/power's reward path, and is the entry
// point any future module (storage, relay, market, ...) uses for the same purpose.
func (k Keeper) CreditEarnings(ctx context.Context, senderModule string, addr sdk.AccAddress, amt sdk.Coin) error {
	if !amt.IsPositive() {
		return nil
	}
	if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, senderModule, types.ModuleName, sdk.NewCoins(amt)); err != nil {
		return fmt.Errorf("failed to move %s from %s into %s: %w", amt, senderModule, types.ModuleName, err)
	}
	return k.creditLedgerOnly(ctx, addr, amt.Amount)
}

// creditLedgerOnly increases addr's earnings ledger entry without moving any coins - used where
// the coins already sit in x/fees's own module account (e.g. a tx's tip, deposited there by the
// ante fee decorator in the same call).
func (k Keeper) creditLedgerOnly(ctx context.Context, addr sdk.AccAddress, amt math.Int) error {
	if !amt.IsPositive() {
		return nil
	}
	current, err := k.GetEarnings(ctx, addr)
	if err != nil {
		return err
	}
	if err := k.setEarnings(ctx, addr, current.Add(amt)); err != nil {
		return fmt.Errorf("failed to credit earnings for %s: %w", addr, err)
	}
	return nil
}

// setEarnings stores addr's earnings balance, or REMOVES the entry entirely when the balance is
// exactly zero (security review B7): a stored zero-balance entry fails
// GenesisState.Validate's "must be positive" check on export/re-import, so a balance that has been
// fully debited back to zero (e.g. by DebitEarningsUpTo) must not be left behind as a literal zero
// row.
func (k Keeper) setEarnings(ctx context.Context, addr sdk.AccAddress, balance math.Int) error {
	if balance.IsZero() {
		if err := k.Earnings.Remove(ctx, addr.String()); err != nil {
			return fmt.Errorf("failed to clear zero earnings balance for %s: %w", addr, err)
		}
		return nil
	}
	return k.Earnings.Set(ctx, addr.String(), balance)
}

// GetEarnings returns addr's current earnings balance (zero if it has none).
func (k Keeper) GetEarnings(ctx context.Context, addr sdk.AccAddress) (math.Int, error) {
	balance, err := k.Earnings.Get(ctx, addr.String())
	if err == nil {
		return balance, nil
	}
	if errors.Is(err, collections.ErrNotFound) {
		return math.ZeroInt(), nil
	}
	return math.Int{}, fmt.Errorf("failed to load earnings for %s: %w", addr, err)
}

// TopUpBondFromEarnings moves up to amount of denom from addr's own earnings account into addr's
// own bank balance, and returns how much was actually moved (security review B8: "implement
// bonding from earnings ... earnings go only to the signer's own bond"). It is used by
// fees/ante.BondTopUpDecorator, an ante decorator that funds a signer's own
// MsgCreateValidator/MsgDelegate shortfall from their earnings before the real staking message
// handler runs - the only path by which an outsider who has never held a public bank balance (this
// chain starts every account at zero, and payouts land only in earnings - see docs/CHAIN.md) can
// ever create or grow a validator.
func (k Keeper) TopUpBondFromEarnings(ctx context.Context, addr sdk.AccAddress, denom string, amount math.Int) (math.Int, error) {
	if !amount.IsPositive() {
		return math.ZeroInt(), nil
	}
	debited, err := k.DebitEarningsUpTo(ctx, addr, amount)
	if err != nil {
		return math.Int{}, err
	}
	if !debited.IsPositive() {
		return math.ZeroInt(), nil
	}
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, addr, sdk.NewCoins(sdk.NewCoin(denom, debited))); err != nil {
		return math.Int{}, fmt.Errorf("failed to move earnings top-up to %s's bank balance: %w", addr, err)
	}
	return debited, nil
}

// DebitEarningsUpTo debits up to want from addr's earnings ledger (never more than its balance)
// and returns the amount actually debited, WITHOUT moving any coins out of x/fees's module account
// - the caller (the ante fee decorator) is responsible for spending that amount from the module
// account for its own purpose (burning the base fee, crediting a tip) immediately afterward, so
// the invariant "sum of earnings balances == the fees module balance" keeps holding across the
// whole operation.
func (k Keeper) DebitEarningsUpTo(ctx context.Context, addr sdk.AccAddress, want math.Int) (math.Int, error) {
	if !want.IsPositive() {
		return math.ZeroInt(), nil
	}
	balance, err := k.GetEarnings(ctx, addr)
	if err != nil {
		return math.Int{}, err
	}
	debit := want
	if debit.GT(balance) {
		debit = balance
	}
	if !debit.IsPositive() {
		return math.ZeroInt(), nil
	}
	if err := k.setEarnings(ctx, addr, balance.Sub(debit)); err != nil {
		return math.Int{}, fmt.Errorf("failed to debit earnings for %s: %w", addr, err)
	}
	return debit, nil
}

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

// PayEarnings moves amt from payer's bank balance into x/fees's module account and credits it to
// recipient's earnings ledger entry. It is the one way a contract pays a user in norama: the
// payment lands in the recipient's earnings, never as a public user balance (C9).
func (k Keeper) PayEarnings(ctx context.Context, payer, recipient sdk.AccAddress, amt sdk.Coin) error {
	if !amt.IsPositive() {
		return fmt.Errorf("earnings payment must be positive, got %s", amt)
	}
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, payer, types.ModuleName, sdk.NewCoins(amt)); err != nil {
		return fmt.Errorf("failed to move %s from %s into %s: %w", amt, payer, types.ModuleName, err)
	}
	return k.creditLedgerOnly(ctx, recipient, amt.Amount)
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
// bonding from earnings ... earnings go only to the signer's own bond"). It is the primitive under
// FundBondFromEarnings, which the bond-funding message handlers call while they execute - the only
// path by which an outsider who has never held a public bank balance (this chain starts every
// account at zero, and payouts land only in earnings - see docs/CHAIN.md) can ever create or grow
// a validator or bond a node.
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

// FundBondFromEarnings makes addr's spendable balance of denom cover needed by moving the shortfall
// from addr's own earnings into its own bank balance. It moves nothing when the bank balance
// already covers needed, and nothing when earnings cannot cover the whole shortfall (a partial
// top-up cannot make the bond succeed, so the message is left to fail with its own error).
//
// Message handlers call it while they execute, before they spend the balance. It must never run in
// an ante handler: ante writes survive a message that then fails, which would turn earnings into a
// spendable bank balance for free. A handler runs in the message's own cache branch, so if the
// message fails for any reason after this call, the top-up is discarded with everything else the
// message did.
func (k Keeper) FundBondFromEarnings(ctx context.Context, addr sdk.AccAddress, denom string, needed math.Int) error {
	if needed.IsNil() || !needed.IsPositive() {
		return nil
	}
	spendable := k.bankKeeper.SpendableCoins(ctx, addr).AmountOf(denom)
	if spendable.GTE(needed) {
		return nil
	}
	shortfall := needed.Sub(spendable)
	earnings, err := k.GetEarnings(ctx, addr)
	if err != nil {
		return err
	}
	if earnings.LT(shortfall) {
		return nil
	}
	if _, err := k.TopUpBondFromEarnings(ctx, addr, denom, shortfall); err != nil {
		return fmt.Errorf("failed to top up %s's bond shortfall from earnings: %w", addr, err)
	}
	return nil
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

// GetFeeBalance returns addr's fee-only balance (zero if it has none).
func (k Keeper) GetFeeBalance(ctx context.Context, addr sdk.AccAddress) (math.Int, error) {
	balance, err := k.FeeBalances.Get(ctx, addr.String())
	if err == nil {
		return balance, nil
	}
	if errors.Is(err, collections.ErrNotFound) {
		return math.ZeroInt(), nil
	}
	return math.Int{}, fmt.Errorf("failed to load fee balance for %s: %w", addr, err)
}

func (k Keeper) setFeeBalance(ctx context.Context, addr sdk.AccAddress, balance math.Int) error {
	if balance.IsZero() {
		if err := k.FeeBalances.Remove(ctx, addr.String()); err != nil {
			return fmt.Errorf("failed to clear zero fee balance for %s: %w", addr, err)
		}
		return nil
	}
	return k.FeeBalances.Set(ctx, addr.String(), balance)
}

// FundFeeBalance moves exactly amount from from's earnings ledger entry into to's fee-only balance,
// without moving any coins: both ledgers are backed by x/fees's one module account, so the
// invariant "earnings + fee balances == the fees module balance" is untouched.
//
// A fee balance is what an operator gives a node's hot key so the key can sign its own
// transactions (C2 item 5). It can pay a transaction's base fee (SettleFee) and nothing else:
// nothing bonds it, shields it, deposits it or moves it on. It is the only way earnings reach
// another address, and it exists for one caller: x/nodes MsgFundHotKey. The caller chooses the
// target; this method fails when from holds less than amount rather than moving a partial amount.
func (k Keeper) FundFeeBalance(ctx context.Context, from, to sdk.AccAddress, amount math.Int) error {
	if amount.IsNil() || !amount.IsPositive() {
		return fmt.Errorf("fee balance transfer amount must be positive, got %s", amount)
	}
	if from.Equals(to) {
		return fmt.Errorf("cannot fund %s's fee balance from its own earnings", from)
	}
	balance, err := k.GetEarnings(ctx, from)
	if err != nil {
		return err
	}
	if balance.LT(amount) {
		return fmt.Errorf("insufficient earnings: %s holds %s, needs %s", from, balance, amount)
	}
	if err := k.setEarnings(ctx, from, balance.Sub(amount)); err != nil {
		return fmt.Errorf("failed to debit earnings for %s: %w", from, err)
	}
	current, err := k.GetFeeBalance(ctx, to)
	if err != nil {
		return err
	}
	return k.setFeeBalance(ctx, to, current.Add(amount))
}

// debitFeeBalanceUpTo debits up to want from addr's fee balance and returns what it debited. Like
// DebitEarningsUpTo it moves no coins; the caller burns what it debited.
func (k Keeper) debitFeeBalanceUpTo(ctx context.Context, addr sdk.AccAddress, want math.Int) (math.Int, error) {
	if !want.IsPositive() {
		return math.ZeroInt(), nil
	}
	balance, err := k.GetFeeBalance(ctx, addr)
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
	if err := k.setFeeBalance(ctx, addr, balance.Sub(debit)); err != nil {
		return math.Int{}, fmt.Errorf("failed to debit fee balance for %s: %w", addr, err)
	}
	return debit, nil
}

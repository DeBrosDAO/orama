package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// SettleFee settles one transaction's fee (plans/open-network/track-c-chain.md C2: "100% of the
// base fee is burned. Tips go to the proposer"): baseFeeAmount is burned and tipAmount is credited
// to proposer's earnings account.
//
// The tip MUST come entirely from payer's own bank balance (security review M4: "earnings may pay
// only the base fee; tips must come from the bank balance") - earnings are a restricted balance
// (usable only for fees and bonding, never a public payment), and letting a tip draw on it would
// open exactly the public-payment leak mandatory shielding forbids: someone could otherwise pump up
// a tip while broke in the bank to slip value to a specific address (whichever validator proposes
// next) using funds that were never supposed to become a public transfer.
//
// The base fee is paid from payer's bank balance first; if allowEarningsForBase is true and that is
// not enough, the remainder is drawn from payer's own earnings account. allowEarningsForBase must be
// false whenever a fee granter (not the original signer) is paying (security review, non-blocking
// "fee granter": "only fall back to earnings when the fee payer is the signer, never through a fee
// granter") - a granter sponsors from their own public bank balance only, never the original
// signer's or their own restricted earnings.
func (k Keeper) SettleFee(ctx context.Context, payer, proposer sdk.AccAddress, baseFeeAmount, tipAmount math.Int, allowEarningsForBase bool) error {
	if baseFeeAmount.IsNil() || baseFeeAmount.IsNegative() || tipAmount.IsNil() || tipAmount.IsNegative() {
		return fmt.Errorf("baseFeeAmount and tipAmount must both be non-negative, got %s and %s", baseFeeAmount, tipAmount)
	}
	total := baseFeeAmount.Add(tipAmount)
	if !total.IsPositive() {
		return nil
	}

	spendable := k.bankKeeper.SpendableCoins(ctx, payer).AmountOf(params.BaseDenom)
	if spendable.LT(tipAmount) {
		return fmt.Errorf(
			"insufficient bank balance to pay a tip of %s%s (only %s%s spendable): a tip must come from a public bank balance, never earnings",
			tipAmount, params.BaseDenom, spendable, params.BaseDenom,
		)
	}

	availableForBase := spendable.Sub(tipAmount)
	bankForBase := baseFeeAmount
	if bankForBase.GT(availableForBase) {
		bankForBase = availableForBase
	}
	fromBank := tipAmount.Add(bankForBase)
	if fromBank.IsPositive() {
		if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, payer, types.ModuleName, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, fromBank))); err != nil {
			return fmt.Errorf("failed to collect %s%s in fees from %s: %w", fromBank, params.BaseDenom, payer, err)
		}
	}

	remainingBase := baseFeeAmount.Sub(bankForBase)
	if remainingBase.IsPositive() {
		if !allowEarningsForBase {
			return fmt.Errorf(
				"insufficient funds: %s owes a base fee of %s%s but only has %s%s spendable, and a fee granter cannot draw on the payer's earnings",
				payer, baseFeeAmount, params.BaseDenom, bankForBase, params.BaseDenom,
			)
		}
		debited, err := k.DebitEarningsUpTo(ctx, payer, remainingBase)
		if err != nil {
			return err
		}
		if debited.LT(remainingBase) {
			return fmt.Errorf(
				"insufficient funds: %s owes a base fee of %s%s but has only %s%s spendable and %s%s more in earnings",
				payer, baseFeeAmount, params.BaseDenom, bankForBase, params.BaseDenom, debited, params.BaseDenom,
			)
		}
	}

	if baseFeeAmount.IsPositive() {
		if err := k.bankKeeper.BurnCoins(ctx, types.ModuleName, sdk.NewCoins(sdk.NewCoin(params.BaseDenom, baseFeeAmount))); err != nil {
			return fmt.Errorf("failed to burn the base fee: %w", err)
		}
	}
	if tipAmount.IsPositive() {
		if err := k.creditLedgerOnly(ctx, proposer, tipAmount); err != nil {
			return fmt.Errorf("failed to credit the tip to the proposer: %w", err)
		}
	}

	return nil
}

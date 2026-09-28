package ante

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/x/fees/keeper"
)

// BankKeeperSpendable is the subset of x/bank's keeper BondTopUpDecorator needs to check whether a
// signer already has enough of its own bank balance before reaching into their earnings.
type BankKeeperSpendable interface {
	SpendableCoins(ctx context.Context, addr sdk.AccAddress) sdk.Coins
}

// BondTopUpDecorator funds a signer's own MsgCreateValidator/MsgDelegate shortfall from their own
// earnings account before the real staking message handler runs (security review B8: "outsiders
// can never bond ... implement bonding from earnings"). This chain starts every account at exactly
// zero norama (plans/open-network.md D16), and every protocol payout lands in an earnings account,
// never a public bank balance (C2) - without this, nobody outside the genesis bootstrap committee
// could ever accumulate enough of a PUBLIC bank balance to self-bond a validator or delegate at
// all, since the mandatory-shielding design gives ordinary users no other route to a spendable
// balance.
//
// It only ever tops up the shortfall between the message's own declared amount and the signer's
// current spendable balance (never more), and only ever moves the SAME address's own earnings into
// its own bank balance - "earnings go only to the signer's own bond": a malicious tx naming someone
// else's address as delegator/validator would fail normal signature verification regardless
// (MsgDelegate/MsgCreateValidator's GetSigners() requires that exact address to have signed), so
// this decorator never needs to check that itself. If the top-up still cannot cover the full
// shortfall (insufficient earnings too), the real staking message is left to fail on its own with
// the ordinary "insufficient funds" error.
type BondTopUpDecorator struct {
	bankKeeper BankKeeperSpendable
	feesKeeper keeper.Keeper
}

// NewBondTopUpDecorator builds a BondTopUpDecorator.
func NewBondTopUpDecorator(bankKeeper BankKeeperSpendable, feesKeeper keeper.Keeper) BondTopUpDecorator {
	return BondTopUpDecorator{bankKeeper: bankKeeper, feesKeeper: feesKeeper}
}

func (d BondTopUpDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	for _, msg := range tx.GetMsgs() {
		switch m := msg.(type) {
		case *stakingtypes.MsgCreateValidator:
			valAddr, err := sdk.ValAddressFromBech32(m.ValidatorAddress)
			if err != nil {
				continue // let the real message handler reject the malformed address
			}
			if err := d.topUp(ctx, sdk.AccAddress(valAddr), m.Value.Denom, m.Value.Amount); err != nil {
				return ctx, err
			}
		case *stakingtypes.MsgDelegate:
			delAddr, err := sdk.AccAddressFromBech32(m.DelegatorAddress)
			if err != nil {
				continue
			}
			if err := d.topUp(ctx, delAddr, m.Amount.Denom, m.Amount.Amount); err != nil {
				return ctx, err
			}
		}
	}
	return next(ctx, tx, simulate)
}

// topUp funds addr's shortfall (needed minus its current spendable balance of denom) from its own
// earnings account, if any shortfall exists.
func (d BondTopUpDecorator) topUp(ctx sdk.Context, addr sdk.AccAddress, denom string, needed math.Int) error {
	if needed.IsNil() || !needed.IsPositive() {
		return nil
	}
	spendable := d.bankKeeper.SpendableCoins(ctx, addr).AmountOf(denom)
	if spendable.GTE(needed) {
		return nil
	}
	shortfall := needed.Sub(spendable)
	if _, err := d.feesKeeper.TopUpBondFromEarnings(ctx, addr, denom, shortfall); err != nil {
		return fmt.Errorf("failed to top up %s's bond shortfall from earnings: %w", addr, err)
	}
	return nil
}

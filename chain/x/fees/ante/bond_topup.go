package ante

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/fees/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
)

// BankKeeperSpendable is the subset of x/bank's keeper BondTopUpDecorator needs to check whether a
// signer already has enough of its own bank balance before reaching into their earnings.
type BankKeeperSpendable interface {
	SpendableCoins(ctx context.Context, addr sdk.AccAddress) sdk.Coins
}

// BondTopUpDecorator funds a signer's own MsgCreateValidator/MsgDelegate/x/nodes MsgBondNode shortfall from their own
// earnings account before the real staking message handler runs (security review B8: "outsiders
// can never bond ... implement bonding from earnings"). This chain starts every account at exactly
// zero norama (plans/open-network.md D16), and every protocol payout lands in an earnings account,
// never a public bank balance (C2) - without this, nobody outside the genesis bootstrap committee
// could ever accumulate enough of a PUBLIC bank balance to self-bond a validator or delegate at
// all, since the mandatory-shielding design gives ordinary users no other route to a spendable
// balance.
//
// It only ever tops up the shortfall between the message's own declared amount and the signer's
// current spendable balance (never more), only when the earnings cover all of it, and only ever moves the SAME address's own earnings into
// its own bank balance - "earnings go only to the signer's own bond": a malicious tx naming someone
// else's address as delegator/validator would fail normal signature verification regardless
// (MsgDelegate/MsgCreateValidator/MsgBondNode's signer is that exact address and it must have
// signed; x/nodes escrows the bond from that same operator address), so this decorator never
// needs to check that itself. Several bond messages from one signer in a tx are summed first. If the top-up still cannot cover the full
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
	// Every bond-funding message in the tx is summed per signer first: the top-up happens before
	// any message runs, so topping up message by message against the unspent balance would let two
	// messages each look covered by the same coins.
	var needs []bondNeed
	for _, msg := range tx.GetMsgs() {
		switch m := msg.(type) {
		case *stakingtypes.MsgCreateValidator:
			valAddr, err := sdk.ValAddressFromBech32(m.ValidatorAddress)
			if err != nil {
				continue // let the real message handler reject the malformed address
			}
			needs = addNeed(needs, sdk.AccAddress(valAddr), m.Value.Denom, m.Value.Amount)
		case *stakingtypes.MsgDelegate:
			delAddr, err := sdk.AccAddressFromBech32(m.DelegatorAddress)
			if err != nil {
				continue
			}
			needs = addNeed(needs, delAddr, m.Amount.Denom, m.Amount.Amount)
		case *nodestypes.MsgBondNode:
			// x/nodes escrows the bond from the signing operator's bank balance, in norama.
			operator, err := sdk.AccAddressFromBech32(m.Operator)
			if err != nil {
				continue
			}
			needs = addNeed(needs, operator, params.BaseDenom, m.Amount)
		}
	}
	for _, n := range needs {
		if err := d.topUp(ctx, n.addr, n.denom, n.amount); err != nil {
			return ctx, err
		}
	}
	return next(ctx, tx, simulate)
}

// bondNeed is the total a single signer's bond-funding messages in one tx will pull from its bank
// balance.
type bondNeed struct {
	addr   sdk.AccAddress
	denom  string
	amount math.Int
}

// addNeed adds amount to addr's running total for denom, keeping first-seen order.
func addNeed(needs []bondNeed, addr sdk.AccAddress, denom string, amount math.Int) []bondNeed {
	if amount.IsNil() || !amount.IsPositive() {
		return needs
	}
	for i := range needs {
		if needs[i].denom == denom && needs[i].addr.Equals(addr) {
			needs[i].amount = needs[i].amount.Add(amount)
			return needs
		}
	}
	return append(needs, bondNeed{addr: addr, denom: denom, amount: amount})
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
	earnings, err := d.feesKeeper.GetEarnings(ctx, addr)
	if err != nil {
		return err
	}
	if earnings.LT(shortfall) {
		// A partial top-up cannot make the bond succeed, and ante writes survive a failed message,
		// so leave the earnings untouched and let the message fail with the ordinary
		// insufficient-funds error.
		return nil
	}
	if _, err := d.feesKeeper.TopUpBondFromEarnings(ctx, addr, denom, shortfall); err != nil {
		return fmt.Errorf("failed to top up %s's bond shortfall from earnings: %w", addr, err)
	}
	return nil
}

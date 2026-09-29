package ante

import (
	"context"
	"errors"
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

// NodeLookup is the subset of x/nodes' keeper BondTopUpDecorator needs to check that a MsgBondNode
// names an existing node its signer operates, before any earnings move.
type NodeLookup interface {
	GetNode(ctx sdk.Context, nodeID string) (nodestypes.Node, error)
}

// OwnFundsQuoter is implemented by a module whose messages pull their signer's own money out of its bank
// balance (deal fee and escrow, token creation fee and metadata deposit). OwnFunds returns the payer
// and the norama the message will take from the payer's bank balance. A message the module does
// not price, or one whose payer is not the signer (a deal paid by a grantor), returns a nil payer.
// The quote must be exactly what the message handler will pull: a smaller one leaves the message
// to fail on insufficient funds, a larger one moves earnings the message never spends.
type OwnFundsQuoter interface {
	OwnFunds(ctx sdk.Context, msg sdk.Msg) (payer sdk.AccAddress, amount math.Int, err error)
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
//
// A MsgBondNode is topped up only when its node exists and its operator is the signer: ante writes
// survive a failed message, so topping up for a nonexistent or foreign node would move the signer's
// earnings into its bank balance and then fail the message. The real handler still rejects such a
// message with its own error.
//
// The same top-up covers a signer's own storage deal (fee and escrow, create and extend) and token
// creation (fee and metadata deposit) through the OwnFundsQuoters it is built with (C2 item 4:
// "fund deposits and escrow for the signer's own deals, tokens, trees and contracts"). Those
// modules pull from the bank balance only, so without the top-up a provider or operator that
// holds only earnings could never open a deal. Tree and node deposits already fall back to
// earnings inside x/fees' LockDeposit.
type BondTopUpDecorator struct {
	bankKeeper BankKeeperSpendable
	feesKeeper keeper.Keeper
	nodes      NodeLookup
	quoters    []OwnFundsQuoter
}

// NewBondTopUpDecorator builds a BondTopUpDecorator.
func NewBondTopUpDecorator(bankKeeper BankKeeperSpendable, feesKeeper keeper.Keeper, nodes NodeLookup, quoters ...OwnFundsQuoter) BondTopUpDecorator {
	return BondTopUpDecorator{bankKeeper: bankKeeper, feesKeeper: feesKeeper, nodes: nodes, quoters: quoters}
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
			owned, err := d.operatesNode(ctx, m.NodeId, m.Operator)
			if err != nil {
				return ctx, err
			}
			if !owned {
				continue // the bond handler rejects it; earnings must not move for it
			}
			needs = addNeed(needs, operator, params.BaseDenom, m.Amount)
		default:
			var err error
			if needs, err = d.quoteOwnFunds(ctx, needs, msg); err != nil {
				return ctx, err
			}
		}
	}
	for _, n := range needs {
		if err := d.topUp(ctx, n.addr, n.denom, n.amount); err != nil {
			return ctx, err
		}
	}
	return next(ctx, tx, simulate)
}

// quoteOwnFunds adds what msg will pull from its payer's bank balance, as quoted by the module that
// owns it, to the running needs.
func (d BondTopUpDecorator) quoteOwnFunds(ctx sdk.Context, needs []bondNeed, msg sdk.Msg) ([]bondNeed, error) {
	for _, q := range d.quoters {
		payer, amount, err := q.OwnFunds(ctx, msg)
		if err != nil {
			return nil, fmt.Errorf("failed to price %s for the earnings top-up: %w", sdk.MsgTypeURL(msg), err)
		}
		if payer != nil {
			return addNeed(needs, payer, params.BaseDenom, amount), nil
		}
	}
	return needs, nil
}

// operatesNode reports whether nodeID exists and signer is its operator. A missing node is not an
// error here: the bond handler reports it.
func (d BondTopUpDecorator) operatesNode(ctx sdk.Context, nodeID, signer string) (bool, error) {
	canonical, err := nodestypes.CanonicalAddress(signer)
	if err != nil {
		return false, nil
	}
	node, err := d.nodes.GetNode(ctx, nodeID)
	if err != nil {
		if errors.Is(err, nodestypes.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to look up node %s for the bond top-up: %w", nodeID, err)
	}
	return node.Operator == canonical, nil
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

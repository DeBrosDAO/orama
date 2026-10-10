//go:build e2e_fleet

package chaincore

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// sdkInvalidRequest is cosmos-sdk ErrInvalidRequest's code.
const sdkInvalidRequest = 18

// powerParams is the part of orama.power.v1.Params these tests read.
type powerParams struct {
	Params struct {
		MinDelegationForRewards chain.Int `json:"min_delegation_for_rewards"`
	} `json:"params"`
}

func minDelegation(t *testing.T, c *chain.Chain) chain.Int {
	t.Helper()
	var p powerParams
	c.Query(t, c.Node(t, 0), &p, "power", "params")
	if p.Params.MinDelegationForRewards.Cmp(chain.Orama(1)) != 0 {
		t.Errorf("min_delegation_for_rewards %s, want the genesis default 1 ORAMA (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md)", p.Params.MinDelegationForRewards.String())
	}
	return p.Params.MinDelegationForRewards
}

// undelegateAtCleanup withdraws whatever delegator still has with valoper.
// A withdrawal enters the 21-day unbonding queue: the run cannot see it pay
// out, but the delegation itself is gone and the stake stops counting.
func undelegateAtCleanup(t *testing.T, c *chain.Chain, k chain.Key, valoper string) {
	t.Cleanup(func() {
		left := delegated(t, c, k, k.Address, valoper)
		if left.IsZero() {
			return
		}
		c.CleanupSubmit(t, k, "withdraw "+left.String()+" from "+valoper, undelegateMsg(k.Address, valoper, left))
	})
}

// TestStaking_delegateFromEarnings: a validator delegates to another
// validator with an empty bank balance; the bond is topped up from its own
// earnings (x/fees/ante/bond_topup.go, docs/whitepaper/technical-reference/vol2/40-economics.md "Outsiders can bond from
// earnings"): earnings drop by exactly the bond plus the fee, the bank
// balance is unchanged, and the delegation exists.
func TestStaking_delegateFromEarnings(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	min := minDelegation(t, c)
	a := c.FundedValidator(t, 0, chain.Orama(10))
	target := c.Valoper(t, c.Validator(t, c.Node(t, 1)))
	undelegateAtCleanup(t, c, a, target)
	before := delegated(t, c, a, a.Address, target)
	r := chain.RequireOK(t, "delegate from earnings", c.Submit(t, a, chain.TxOptions{}, delegateMsg(a.Address, target, min)))
	h := r.Height
	fee := eventInt(t, r, "tx", "base_fee")
	bank0, bank1 := c.BankAt(t, a.Node, a.Address, h-1), c.BankAt(t, a.Node, a.Address, h)
	if bank0.Cmp(bank1) != 0 {
		t.Errorf("bank balance moved %s -> %s: a bond topped up from earnings must not touch it", bank0.String(), bank1.String())
	}
	if bank0.IsZero() && c.Epoch(t, a.Node, h-1).CurrentEpoch.Cmp(c.Epoch(t, a.Node, h).CurrentEpoch) == 0 {
		spent := c.EarningsAt(t, a.Node, a.Address, h-1).Sub(c.EarningsAt(t, a.Node, a.Address, h))
		if spent.Cmp(min.Add(fee)) != 0 {
			t.Errorf("earnings dropped by %s, want bond %s + fee %s", spent.String(), min.String(), fee.String())
		}
	}
	got := delegated(t, c, a, a.Address, target).Sub(before)
	if diff := got.Sub(min); diff.Sign() > 0 || diff.Cmp(chain.NewInt(-1)) < 0 {
		t.Errorf("delegation grew by %s, want %s (share truncation may lose one norama)", got.String(), min.String())
	}
	c.RequireInvariants(t, "a delegation funded from earnings")
}

// TestStaking_minDelegationRefused: a delegation that would sit strictly
// between zero and MinDelegationForRewards is refused in the ante chain,
// before any top-up is kept (x/power/ante/min_delegation.go); zero is refused
// as not positive (boundary values: 0, min-1).
func TestStaking_minDelegationRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	min := minDelegation(t, c)
	// Pair node-2 -> node-1 is used by no other test, so no delegation exists
	// that the dust would add to.
	a := c.FundedValidator(t, 2, chain.Orama(10))
	target := c.Valoper(t, c.Validator(t, c.Node(t, 1)))
	if have := delegated(t, c, a, a.Address, target); !have.IsZero() {
		t.Fatalf("%s already delegates %s to %s; the boundary needs an empty delegation", a.Address, have.String(), target)
	}
	below := c.Submit(t, a, chain.TxOptions{}, delegateMsg(a.Address, target, min.Sub(chain.NewInt(1))))
	chain.RequireCode(t, "delegation of min-1", below, sdkSpace, sdkInvalidRequest, "resulting delegation must be at least")
	zero := c.Submit(t, a, chain.TxOptions{}, delegateMsg(a.Address, target, chain.NewInt(0)))
	chain.RequireCode(t, "delegation of 0", zero, sdkSpace, sdkInvalidRequest, "delegation amount must be positive")
	c.RequireInvariants(t, "refused dust delegations")
}

// TestStaking_partialWithdrawalBelowMinRefused: withdrawing part of a
// delegation so that less than the minimum stays is refused; withdrawing all
// of it is a full exit and is allowed (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md, MinDelegationDecorator).
func TestStaking_partialWithdrawalBelowMinRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	min := minDelegation(t, c)
	a := c.FundedValidator(t, 0, chain.Orama(10))
	target := c.Valoper(t, c.Validator(t, c.Node(t, 2)))
	undelegateAtCleanup(t, c, a, target)
	half := chain.NewInt(min.Int64() / 2)
	chain.RequireOK(t, "delegate 1.5x min", c.Submit(t, a, chain.TxOptions{}, delegateMsg(a.Address, target, min.Add(half))))
	partial := c.Submit(t, a, chain.TxOptions{}, undelegateMsg(a.Address, target, min))
	chain.RequireCode(t, "withdrawal leaving half the minimum", partial, sdkSpace, sdkInvalidRequest, "delegation left after withdrawal must be at least")
	all := delegated(t, c, a, a.Address, target)
	chain.RequireOK(t, "full exit", c.Submit(t, a, chain.TxOptions{}, undelegateMsg(a.Address, target, all)))
	c.RequireInvariants(t, "a full exit")
}

// TestStaking_createValidatorForExistingOperatorRefused: a committee member
// already has a validator record (x/power InitGenesis), so its
// MsgCreateValidator is refused by x/staking. The bond it declared was
// topped up from earnings in the ante chain; a refused bond must leave no
// public bank balance behind (docs/whitepaper/technical-reference/vol2/40-economics.md: the top-up "rolls back with"
// the transaction; earnings may never become a public balance, M4/B8).
func TestStaking_createValidatorForExistingOperatorRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	min := minDelegation(t, c)
	k := c.FundedValidator(t, 2, chain.Orama(10))
	own := c.Valoper(t, k)
	r := c.Submit(t, k, chain.TxOptions{}, createValidatorMsg(t, own, min))
	chain.RequireRefused(t, "second MsgCreateValidator", r, "validator already exist")
	if r.Stage != chain.StageBlock {
		t.Fatalf("refused at %s, want the x/staking handler in a block", r.Stage)
	}
	b0, b1 := c.BankAt(t, k.Node, k.Address, r.Height-1), c.BankAt(t, k.Node, k.Address, r.Height)
	if leaked := b1.Sub(b0); leaked.Sign() != 0 {
		// Put the leaked coins back into stake so later tests see the
		// validator as it was (bank balance unchanged overall).
		t.Cleanup(func() { c.CleanupSubmit(t, k, "re-bond the leaked norama", delegateMsg(k.Address, own, leaked)) })
		t.Errorf("PRODUCT BUG: the refused MsgCreateValidator left %s norama of earnings in the public bank balance of %s "+
			"(x/fees/ante/bond_topup.go tops up in the ante chain, whose writes baseapp keeps when the message fails)",
			leaked.String(), k.Address)
	}
	c.RequireInvariants(t, "a refused MsgCreateValidator")
}

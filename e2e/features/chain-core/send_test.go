//go:build e2e_fleet

package chaincore

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// publicPaymentRefused is x/shielded/policy ErrPublicPayment.
const publicPaymentRefused = "public user-to-user norama transfer is refused"

// TestSend_userToUserRefused: a bank MsgSend of norama from one user to
// another is refused by the send restriction before any balance is checked
// (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md "Denom and accounts"; x/shielded/policy/restriction.go runs
// before subUnlockedCoins in x/bank SendCoins), so even an account with no
// bank balance gets the restriction, not "insufficient funds". The refusal is
// delivered (the message runs in the block) and the fee is still charged.
func TestSend_userToUserRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	a := c.FundedValidator(t, 0, chain.Orama(1))
	b := c.Validator(t, c.Node(t, 1))
	for _, amount := range []chain.Int{chain.NewInt(1), chain.Orama(1000)} {
		r := c.Submit(t, a, chain.TxOptions{}, sendMsg(a.Address, b.Address, amount))
		chain.RequireRefused(t, fmt.Sprintf("send %s norama to another user", amount.String()), r, publicPaymentRefused)
		if r.Stage != chain.StageBlock {
			t.Errorf("the refusal came from %s, want the message handler in a block", r.Stage)
		}
	}
	c.RequireInvariants(t, "refused user-to-user sends")
}

// TestSend_toSelfRefused: paying yourself is still a user-to-user payment.
func TestSend_toSelfRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	a := c.FundedValidator(t, 0, chain.Orama(1))
	r := c.Submit(t, a, chain.TxOptions{}, sendMsg(a.Address, a.Address, chain.NewInt(1)))
	chain.RequireRefused(t, "send norama to self", r, publicPaymentRefused)
}

// TestSend_toModuleAccountRefused: a user cannot pay a module account either:
// every module account is a blocked address of x/bank (chain/app/app.go
// BlockedAddresses). The fees module is the one that holds everyone's
// earnings.
func TestSend_toModuleAccountRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	a := c.FundedValidator(t, 0, chain.Orama(1))
	feesModule := chain.ModuleAddress("fees")
	r := c.Submit(t, a, chain.TxOptions{}, sendMsg(a.Address, feesModule, chain.NewInt(1)))
	chain.RequireRefused(t, "send norama to the fees module account", r, "is not allowed to receive funds")
}

// TestSend_moduleAccountsMoveNorama: module accounts may move norama: every
// closed epoch x/emission mints into its own account and x/power pays
// validators' earnings through the fees module account, so a validator's
// earnings grow across an epoch close, and the earnings ledger still equals
// the fees module balance (x/fees invariant).
func TestSend_moduleAccountsMoveNorama(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.Validator(t, c.Node(t, 2))
	start := c.Epoch(t, k.Node, 0).CurrentEpoch
	before := c.Earnings(t, k.Node, k.Address)
	eventually.Require(t, chain.PollEvery, chain.EpochBudget, "an epoch close to pay the validators", func() (bool, error) {
		if e := c.Epoch(t, k.Node, 0).CurrentEpoch; e.Cmp(start) <= 0 {
			return false, fmt.Errorf("still epoch %s", e.String())
		}
		return true, nil
	})
	eventually.Require(t, chain.PollEvery, chain.EpochBudget, "the epoch payout to reach earnings", func() (bool, error) {
		if got := c.Earnings(t, k.Node, k.Address); got.Cmp(before) <= 0 {
			return false, fmt.Errorf("earnings %s, was %s", got.String(), before.String())
		}
		return true, nil
	})
	c.RequireInvariants(t, "an epoch payout")
}

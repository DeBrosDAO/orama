//go:build e2e_fleet

package chaincore

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// TestSend_userToUserMovesNorama: a bank MsgSend of norama from one user to another is a public
// payment and goes through. The recipient holds exactly the amount, the sender is down the amount
// and the fee, and the earnings ledger still equals the fees module balance.
func TestSend_userToUserMovesNorama(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.FaucetNode(t)
	a := c.NewFundedKey(t, n, "e2e-send-payer", chain.Orama(5))
	b := c.NewKey(t, n, "e2e-send-payee")
	r := c.Submit(t, a, chain.TxOptions{}, sendMsg(a.Address, b.Address, chain.Orama(2)))
	chain.RequireOK(t, "send norama to another user", r)
	if got := c.Bank(t, n, b.Address); got.Cmp(chain.Orama(2)) != 0 {
		t.Errorf("the payee holds %s norama, want %s", got.String(), chain.Orama(2).String())
	}
	if got := c.Bank(t, n, a.Address); got.Cmp(chain.Orama(3)) >= 0 {
		t.Errorf("the payer holds %s norama, want less than %s (the amount and the fee left)", got.String(), chain.Orama(3).String())
	}
	c.RequireInvariants(t, "a public user-to-user send")
}

// TestSend_moreThanTheBankBalanceRefused: a public send spends the bank balance only. Earnings are
// not a spendable balance, so a validator with thousands of ORAMA of earnings and an empty bank
// balance cannot send, and the payee receives nothing.
func TestSend_moreThanTheBankBalanceRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.FaucetNode(t)
	a := c.NewFundedKey(t, n, "e2e-send-overdraw", chain.Orama(1))
	b := c.NewKey(t, n, "e2e-send-overdraw-payee")
	r := c.Submit(t, a, chain.TxOptions{}, sendMsg(a.Address, b.Address, chain.Orama(1000)))
	chain.RequireRefused(t, "send more norama than the bank balance", r, "insufficient")
	if got := c.Bank(t, n, b.Address); !got.IsZero() {
		t.Errorf("a refused send paid %s norama", got.String())
	}
}

// TestSend_toModuleAccountRefused: a user cannot pay a module account: every module account is a
// blocked address of x/bank (chain/app/app.go BlockedAddresses). The fees module is the one that
// holds everyone's earnings, so a public send into it would unbalance the earnings ledger.
func TestSend_toModuleAccountRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	a := c.FundedValidator(t, 0, chain.Orama(1))
	feesModule := chain.ModuleAddress("fees")
	r := c.Submit(t, a, chain.TxOptions{}, sendMsg(a.Address, feesModule, chain.NewInt(1)))
	chain.RequireRefused(t, "send norama to the fees module account", r, "is not allowed to receive funds")
	c.RequireInvariants(t, "a refused send to the fees module")
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

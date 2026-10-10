//go:build e2e_fleet

package chaincore

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

func withdrawEarningsMsg(signer string, amount chain.Int) chain.Msg {
	return chain.NewMsg("/orama.fees.v1.MsgWithdrawEarnings", map[string]any{"signer": signer, "amount": amount.String()})
}

// TestWithdrawEarnings_becomesAPublicBalanceThatCanBeSent: a validator operator holds epoch rewards
// as earnings and no bank balance. MsgWithdrawEarnings moves part of them to the signer's own bank
// balance (less the fee, which the bank balance did not cover, so it came out of earnings), and a
// plain MsgSend then pays a fresh key from it. The earnings ledger still equals the fees module
// balance afterwards (x/fees/keeper/withdraw.go).
func TestWithdrawEarnings_becomesAPublicBalanceThatCanBeSent(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(20))
	payee := c.NewKey(t, k.Node, "e2e-withdraw-payee")
	bankBefore := c.Bank(t, k.Node, k.Address)

	chain.RequireOK(t, "withdraw earnings", c.Submit(t, k, chain.TxOptions{}, withdrawEarningsMsg(k.Address, chain.Orama(5))))

	maxFee := chain.NewInt(c.BaseFee(t, k.Node).Int64() * chain.DefaultGas)
	gained := c.Bank(t, k.Node, k.Address).Sub(bankBefore)
	if gained.Cmp(chain.Orama(5)) > 0 || gained.Cmp(chain.Orama(5).Sub(maxFee)) < 0 {
		t.Errorf("the bank balance rose by %s norama, want %s less at most the fee %s", gained.String(), chain.Orama(5).String(), maxFee.String())
	}
	chain.RequireOK(t, "send withdrawn earnings", c.Submit(t, k, chain.TxOptions{}, sendMsg(k.Address, payee.Address, chain.Orama(1))))
	if got := c.Bank(t, k.Node, payee.Address); got.Cmp(chain.Orama(1)) != 0 {
		t.Errorf("the payee holds %s norama, want %s", got.String(), chain.Orama(1).String())
	}
	c.RequireInvariants(t, "a withdrawal of earnings")
}

// TestWithdrawEarnings_moreThanTheEarningsIsRefused: a withdrawal above the signer's earnings fails
// as a whole and credits no bank balance.
func TestWithdrawEarnings_moreThanTheEarningsIsRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.FaucetNode(t)
	k := c.NewFundedKey(t, n, "e2e-withdraw-nothing", chain.Orama(1))
	r := c.Submit(t, k, chain.TxOptions{}, withdrawEarningsMsg(k.Address, chain.Orama(1)))
	chain.RequireRefused(t, "withdraw earnings that do not exist", r, "insufficient earnings")
	if got := c.Bank(t, n, k.Address); got.Cmp(chain.Orama(1)) > 0 {
		t.Errorf("a refused withdrawal raised the bank balance to %s norama", got.String())
	}
	c.RequireInvariants(t, "a refused withdrawal")
}

//go:build e2e_fleet

package chaincore

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

func grantMsg(granter, grantee string) chain.Msg {
	return chain.NewMsg("/cosmos.feegrant.v1beta1.MsgGrantAllowance", map[string]any{
		"granter": granter, "grantee": grantee,
		"allowance": map[string]any{"@type": "/cosmos.feegrant.v1beta1.BasicAllowance", "spend_limit": []any{}},
	})
}

func revokeMsg(granter, grantee string) chain.Msg {
	return chain.NewMsg("/cosmos.feegrant.v1beta1.MsgRevokeAllowance", map[string]any{"granter": granter, "grantee": grantee})
}

// TestFeegrant_granterCannotPayFromEarnings: a fee granter sponsors only from
// its public bank balance, never from anyone's earnings (x/fees/keeper
// feepay.go SettleFee allowEarningsForBase=false, docs/whitepaper/technical-reference/vol2/40-economics.md "Feegrant
// sponsorship"). Validator A grants validator B an unlimited allowance; A's
// bank balance is empty, so B's transaction naming A as granter is refused,
// although both have ample earnings. The grant is revoked at cleanup.
func TestFeegrant_granterCannotPayFromEarnings(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	a := c.FundedValidator(t, 0, chain.Orama(1))
	b := c.FundedValidator(t, 2, chain.Orama(1))
	if bank := c.Bank(t, a.Node, a.Address); !bank.IsZero() {
		t.Fatalf("granter %s holds %s norama in its bank; the case needs an empty bank balance", a.Address, bank.String())
	}
	chain.RequireOK(t, "grant allowance", c.Submit(t, a, chain.TxOptions{}, grantMsg(a.Address, b.Address)))
	t.Cleanup(func() { c.CleanupSubmit(t, a, "revoke the allowance", revokeMsg(a.Address, b.Address)) })
	bEarnings := c.Earnings(t, b.Node, b.Address)
	r := c.Submit(t, b, chain.TxOptions{Granter: a.Address}, harmlessMsg(t, c, b))
	chain.RequireCode(t, "sponsored by a granter with only earnings", r, sdkSpace, codeInsufficientFun,
		"a fee granter cannot draw on the payer's earnings")
	if after := c.Earnings(t, b.Node, b.Address); after.Cmp(bEarnings) < 0 {
		t.Errorf("the grantee's earnings fell %s -> %s although its sponsored transaction was refused", bEarnings.String(), after.String())
	}
	c.RequireInvariants(t, "a refused sponsored transaction")
}

// TestFeegrant_noGrantRefused: naming a granter that granted nothing is
// refused by x/feegrant before any fee moves.
func TestFeegrant_noGrantRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	b := c.FundedValidator(t, 1, chain.Orama(1))
	stranger := c.Validator(t, c.Node(t, 2))
	r := c.Submit(t, b, chain.TxOptions{Granter: stranger.Address}, harmlessMsg(t, c, b))
	chain.RequireRefused(t, "granter without a grant", r, "does not allow paying fees for")
}

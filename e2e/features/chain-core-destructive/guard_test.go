//go:build e2e_fleet

package chaincoredestructive

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// Codespace and code of cosmos-sdk ErrInvalidRequest.
const (
	sdkSpace          = "sdk"
	sdkInvalidRequest = 18
)

// TestStaking_undelegateGuardLocksForceBond: while lambda < 1 a committee
// member cannot withdraw its own force-bonded self-bond
// (x/power/ante/undelegate_guard.go, docs/CHAIN.md "Force-bonding"). It is
// here, in the package that runs alone, because the attempt withdraws the
// WHOLE self-bond: were the guard broken, the validator would lose its
// power while other packages depend on the chain.
func TestStaking_undelegateGuardLocksForceBond(t *testing.T) {
	c := chain.New(t)
	a := c.FundedValidator(t, 1, chain.Orama(1))
	var lambda struct {
		Lambda chain.Dec `json:"lambda"`
	}
	c.Query(t, a.Node, &lambda, "power", "lambda")
	if lambda.Lambda.Float() >= 1 {
		t.Fatalf("lambda is %v: the guard only applies before the hand-over completes; a fresh run chain cannot reach 1", lambda.Lambda.Float())
	}
	own := c.Valoper(t, a)
	var d struct {
		DelegationResponse struct {
			Balance struct {
				Amount chain.Int `json:"amount"`
			} `json:"balance"`
		} `json:"delegation_response"`
	}
	c.Query(t, a.Node, &d, "staking", "delegation", a.Address, own)
	self := d.DelegationResponse.Balance.Amount
	if self.IsZero() {
		t.Fatalf("%s has no self-bond yet: force-bonding starts with the first epoch reward", own)
	}
	r := c.Submit(t, a, chain.TxOptions{}, chain.NewMsg("/cosmos.staking.v1beta1.MsgUndelegate", map[string]any{
		"delegator_address": a.Address, "validator_address": own,
		"amount": map[string]any{"denom": chain.Denom, "amount": self.String()},
	}))
	chain.RequireCode(t, "withdraw the whole force-bonded self-bond", r, sdkSpace, sdkInvalidRequest, "is force-bonded and locked until lambda reaches 1")
	c.RequireInvariants(t, "a refused self-bond withdrawal")
}

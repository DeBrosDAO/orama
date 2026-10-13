//go:build e2e_fleet

package chaincore

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// doNotModify is x/staking's sentinel for "leave this description field".
const doNotModify = "[do-not-modify]"

// harmlessMsg is a MsgEditValidator that changes nothing on k's own
// validator: a transaction that always succeeds and leaves no state behind,
// used where a test needs a real, delivered transaction.
func harmlessMsg(t *testing.T, c *chain.Chain, k chain.Key) chain.Msg {
	t.Helper()
	return chain.NewMsg("/cosmos.staking.v1beta1.MsgEditValidator", map[string]any{
		"description": map[string]any{
			"moniker": doNotModify, "identity": doNotModify, "website": doNotModify,
			"security_contact": doNotModify, "details": doNotModify,
		},
		"validator_address":   c.Valoper(t, k),
		"commission_rate":     nil,
		"min_self_delegation": nil,
	})
}

func coin(amount chain.Int) map[string]any {
	return map[string]any{"denom": chain.Denom, "amount": amount.String()}
}

func delegateMsg(delegator, valoper string, amount chain.Int) chain.Msg {
	return chain.NewMsg("/cosmos.staking.v1beta1.MsgDelegate", map[string]any{
		"delegator_address": delegator, "validator_address": valoper, "amount": coin(amount),
	})
}

func undelegateMsg(delegator, valoper string, amount chain.Int) chain.Msg {
	return chain.NewMsg("/cosmos.staking.v1beta1.MsgUndelegate", map[string]any{
		"delegator_address": delegator, "validator_address": valoper, "amount": coin(amount),
	})
}

func sendMsg(from, to string, amount chain.Int) chain.Msg {
	return chain.NewMsg("/cosmos.bank.v1beta1.MsgSend", map[string]any{
		"from_address": from, "to_address": to, "amount": []any{coin(amount)},
	})
}

// createValidatorMsg is a well-formed MsgCreateValidator for valoper with a
// fresh consensus key.
func createValidatorMsg(t *testing.T, valoper string, value chain.Int) chain.Msg {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate a consensus key: %v", err)
	}
	return chain.NewMsg("/cosmos.staking.v1beta1.MsgCreateValidator", map[string]any{
		"description":         map[string]any{"moniker": "e2e-outsider"},
		"commission":          map[string]any{"rate": "0.1", "max_rate": "0.2", "max_change_rate": "0.01"},
		"min_self_delegation": "1",
		"delegator_address":   "",
		"validator_address":   valoper,
		"pubkey":              map[string]any{"@type": "/cosmos.crypto.ed25519.PubKey", "key": base64.StdEncoding.EncodeToString(pub)},
		"value":               coin(value),
	})
}

// delegation is `oramad query staking delegation`.
type delegation struct {
	DelegationResponse struct {
		Balance struct {
			Amount chain.Int `json:"amount"`
		} `json:"balance"`
	} `json:"delegation_response"`
}

// delegated returns the tokens delegator has with valoper (0 when none).
func delegated(t *testing.T, c *chain.Chain, k chain.Key, delegator, valoper string) chain.Int {
	t.Helper()
	out := c.QueryOut(t, k.Node, "staking", "delegation", delegator, valoper)
	if out.Exit != 0 {
		return chain.NewInt(0)
	}
	var d delegation
	c.Query(t, k.Node, &d, "staking", "delegation", delegator, valoper)
	return d.DelegationResponse.Balance.Amount
}

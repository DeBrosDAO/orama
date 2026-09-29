//go:build e2e_fleet

package chaincore

import (
	"regexp"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// authorityRefusal is cosmos-sdk types/authority.go ValidateAuthority.
var authorityRefusal = regexp.MustCompile(`invalid authority: expected (orama1[02-9ac-hj-np-z]{38}), got (orama1[02-9ac-hj-np-z]{38})`)

// authorityMsgs are the authority-gated messages of the stock modules the app
// wires (docs/CHAIN.md "Modules wired": every one is given
// app.UnreachableAuthority()). Every handler checks the authority before
// anything else (cosmos-sdk types/authority.go ValidateAuthority is the first
// call of each), so the bodies are left empty: a refusal can only come from
// the authority.
func authorityMsgs(authority string) map[string]chain.Msg {
	params := map[string]any{"authority": authority, "params": map[string]any{}}
	return map[string]chain.Msg{
		"bank":           chain.NewMsg("/cosmos.bank.v1beta1.MsgUpdateParams", params),
		"staking":        chain.NewMsg("/cosmos.staking.v1beta1.MsgUpdateParams", params),
		"slashing":       chain.NewMsg("/cosmos.slashing.v1beta1.MsgUpdateParams", params),
		"distribution":   chain.NewMsg("/cosmos.distribution.v1beta1.MsgUpdateParams", params),
		"auth":           chain.NewMsg("/cosmos.auth.v1beta1.MsgUpdateParams", params),
		"consensus":      chain.NewMsg("/cosmos.consensus.v1.MsgUpdateParams", map[string]any{"authority": authority}),
		"upgrade-cancel": chain.NewMsg("/cosmos.upgrade.v1beta1.MsgCancelUpgrade", map[string]any{"authority": authority}),
		"upgrade": chain.NewMsg("/cosmos.upgrade.v1beta1.MsgSoftwareUpgrade", map[string]any{
			"authority": authority, "plan": map[string]any{"name": "e2e-never", "height": "999999999", "info": ""}}),
	}
}

// TestAuthority_gatedMsgsRefusedForAnySigner: every authority-gated message
// (MsgUpdateParams of bank, staking, slashing, distribution, auth and
// consensus; MsgSoftwareUpgrade; MsgCancelUpgrade) signed by a validator
// naming itself as authority is refused with ErrUnauthorized, and every
// module names the SAME expected authority: the module address of
// "orama/no-authority", which no key can sign for (docs/CHAIN.md, "x/houses
// is registered, and it is not the SDK x/gov authority"). No parameter moves.
func TestAuthority_gatedMsgsRefusedForAnySigner(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	unreachable := chain.ModuleAddress(chain.UnreachableAuthorityName)
	for name, msg := range authorityMsgs(k.Address) {
		r := c.Submit(t, k, chain.TxOptions{}, msg)
		chain.RequireCode(t, name, r, sdkSpace, codeUnauthorized, "invalid authority")
		m := authorityRefusal.FindStringSubmatch(r.Log)
		if m == nil {
			t.Errorf("%s: refusal does not name the expected authority: %s", name, r.Log)
			continue
		}
		if m[1] != unreachable || m[2] != k.Address {
			t.Errorf("%s: expected authority %s (want %s), got %s (want the signer %s)", name, m[1], unreachable, m[2], k.Address)
		}
	}
	c.RequireInvariants(t, "refused authority-gated messages")
}

// TestAuthority_noKeySignsForTheUnreachableAuthority: a message naming the
// real authority cannot be signed by any key the node holds: the client
// refuses to sign a transaction whose required signer is not the key
// (x/auth/client/tx.go), and nothing else could produce that signature.
func TestAuthority_noKeySignsForTheUnreachableAuthority(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.Validator(t, c.Node(t, 0))
	msg := authorityMsgs(chain.ModuleAddress(chain.UnreachableAuthorityName))["upgrade"]
	if e := c.SignExpectRefused(t, k, chain.TxOptions{}, msg); e == "" {
		t.Fatal("the client refused without saying why")
	}
}

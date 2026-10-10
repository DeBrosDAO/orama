//go:build e2e_fleet

package chainglobal

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// Type URLs of the x/slashing and x/staking messages the validator commands build.
const (
	unjailType = "/cosmos.slashing.v1beta1.MsgUnjail"
	editType   = "/cosmos.staking.v1beta1.MsgEditValidator"
	// doNotModify is x/staking's marker for a description field an edit leaves.
	doNotModify = "[do-not-modify]"
)

// Field numbers of MsgUnjail, MsgEditValidator and staking Description.
const (
	unjailValidator                                     = 1
	editDescription, editValidator, editCommission      = 1, 2, 3
	descMoniker, descIdentity, descWebsite, descContact = 1, 2, 3, 4
	descDetails                                         = 5
)

// validatorDetails is the details field of the validator's staking record.
func validatorDetails(t *testing.T, c *chain.Chain, s signer) string {
	t.Helper()
	var rec any
	c.Query(t, s.k.Node, &rec, "staking", "validator", c.Valoper(t, s.k))
	if v, ok := findMember(rec, "details"); ok {
		return v
	}
	return ""
}

// findMember is the first string member named key at any depth.
func findMember(v any, key string) (string, bool) {
	switch x := v.(type) {
	case map[string]any:
		if s, ok := x[key].(string); ok {
			return s, true
		}
		for _, child := range x {
			if s, ok := findMember(child, key); ok {
				return s, true
			}
		}
	case []any:
		for _, child := range x {
			if s, ok := findMember(child, key); ok {
				return s, true
			}
		}
	}
	return "", false
}

// TestValidatorUnjail_documentExecutesAndIsRefusedForABondedValidator:
// `orama maint global validator unjail` prints the sign document of x/slashing's
// MsgUnjail for the validator whose operator account is --operator (its
// oramavaloper address); the chain decodes and runs exactly that document and
// x/slashing refuses it: the validator is not jailed (or, checked before that,
// has no self-delegation yet, which every refusal of the handler says is a
// reason it cannot be unjailed).
func TestValidatorUnjail_documentExecutesAndIsRefusedForABondedValidator(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	s := newSigner(t, c, chain.NonOperatorNode)
	d := doc(t, c, s, "maint", "global", "validator", "unjail", "--operator", s.k.Address)
	requireDoc(t, "unjail", d, c.ID, s.account, s.sequence, unjailType)
	if got, want := d.msg.Str(unjailValidator), c.Valoper(t, s.k); got != want {
		t.Errorf("the unjail document names validator %q, want %q", got, want)
	}
	chain.RequireRefused(t, "unjail of a validator that is not jailed", execute(t, c, s, d), "cannot be unjailed")
	res := infra.Run(t, harness.CLI(t), append([]string{"maint", "global", "validator", "unjail"}, s.flags(c)...)...)
	infra.ExpectExit(t, res, infra.ExitUsage, "operator")
	bad := infra.Run(t, harness.CLI(t), append([]string{"maint", "global", "validator", "unjail", "--operator", "not-an-address"}, s.flags(c)...)...)
	infra.ExpectExit(t, bad, infra.ExitUsage)
}

// TestValidatorEdit_documentExecutesAndRestores: `orama maint global validator
// edit` builds x/staking's MsgEditValidator changing only the flags given
// (every other description field is [do-not-modify]); the chain runs the
// document and the validator's details change, and a second document restores
// them. A commission rate is encoded as LegacyDec's scaled integer (0.05 is
// 50000000000000000) and is not executed: x/staking allows one commission
// change per 24 hours. An edit that changes nothing, or a rate above 1, is a
// usage error.
func TestValidatorEdit_documentExecutesAndRestores(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	s := newSigner(t, c, 1)
	before := validatorDetails(t, c, s)
	t.Cleanup(func() { restoreDetails(t, c, s, before) })
	changed := "e2e " + chain.UniqueID(t, "edit-")
	d := doc(t, c, s, "maint", "global", "validator", "edit", "--operator", s.k.Address, "--details", changed)
	requireDoc(t, "edit", d, c.ID, s.account, s.sequence, editType)
	desc, _ := d.msg.Msg(editDescription)
	if desc.Str(descDetails) != changed || desc.Str(descMoniker) != doNotModify || desc.Str(descIdentity) != doNotModify ||
		desc.Str(descWebsite) != doNotModify || desc.Str(descContact) != doNotModify {
		t.Errorf("the edit document's description is not [do-not-modify] but for the details: details %q moniker %q", desc.Str(descDetails), desc.Str(descMoniker))
	}
	if d.msg.Str(editValidator) != c.Valoper(t, s.k) || d.msg.Str(editCommission) != "" {
		t.Errorf("the edit document names validator %q with commission %q", d.msg.Str(editValidator), d.msg.Str(editCommission))
	}
	chain.RequireOK(t, "the CLI's validator edit", execute(t, c, s, d))
	if got := validatorDetails(t, c, s); got != changed {
		t.Errorf("the validator's details are %q after the edit, want %q", got, changed)
	}
	back := doc(t, c, s, "maint", "global", "validator", "edit", "--operator", s.k.Address, "--details", before)
	chain.RequireOK(t, "the CLI's edit restoring the details", execute(t, c, s, back))
	if got := validatorDetails(t, c, s); got != before {
		t.Errorf("the validator's details are %q after the restore, want %q", got, before)
	}
	rate := doc(t, c, s, "maint", "global", "validator", "edit", "--operator", s.k.Address, "--commission-rate", "0.05")
	if got := rate.msg.Str(editCommission); got != "50000000000000000" {
		t.Errorf("a commission rate of 0.05 is encoded %q, want 50000000000000000", got)
	}
	c.RequireInvariants(t, "a validator edit")
}

// TestValidatorEdit_usageErrors: the edit command refuses an edit that
// changes nothing, a commission rate that is not a decimal from 0 to 1, and a
// missing operator, before it builds anything.
func TestValidatorEdit_usageErrors(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	s := newSigner(t, c, 2)
	cases := map[string][]string{
		"nothing to change":        {"--operator", s.k.Address},
		"a rate above 1":           {"--operator", s.k.Address, "--commission-rate", "1.5"},
		"a rate that is no number": {"--operator", s.k.Address, "--commission-rate", "high"},
		"no operator":              {"--moniker", "e2e"},
	}
	for name, args := range cases {
		res := infra.Run(t, harness.CLI(t), append(append([]string{"maint", "global", "validator", "edit"}, args...), s.flags(c)...)...)
		infra.ExpectExit(t, res, infra.ExitUsage)
		if strings.Contains(res.Stdout, signDocMarker) {
			t.Errorf("%s: a sign document was printed for a refused edit", name)
		}
	}
}

// restoreDetails puts the validator's details back when the test left them changed.
func restoreDetails(t *testing.T, c *chain.Chain, s signer, want string) {
	t.Helper()
	if validatorDetails(t, c, s) == want {
		return
	}
	msg := chain.NewMsg(editType, map[string]any{"validator_address": c.Valoper(t, s.k), "description": map[string]any{
		"moniker": doNotModify, "identity": doNotModify, "website": doNotModify, "security_contact": doNotModify, "details": want}})
	c.CleanupSubmit(t, s.k, fmt.Sprintf("restore the details of %s", s.k.Address), msg)
}

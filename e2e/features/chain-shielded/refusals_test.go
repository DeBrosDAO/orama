//go:build e2e_fleet

package chainshielded

import (
	"encoding/binary"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// shieldedGas is the gas limit of a signed shielded transaction: the size gas
// of a bundle of a few kilobytes is far above the default limit's headroom
// for a bundle of the maximum size.
const shieldedGas = 2_000_000

var signedOpts = chain.TxOptions{Gas: shieldedGas}

// signedKinds builds each signed message kind around one bundle.
var signedKinds = map[string]func(signer string, bundle []byte) chain.Msg{
	"MsgShield":         shieldMsg,
	"MsgShieldEarnings": shieldEarningsMsg,
	"MsgUnshield":       func(signer string, bundle []byte) chain.Msg { return unshieldMsg(signer, bundle, nil) },
}

// TestShield_valueBalanceSignRefused: a shield (from the bank balance or from
// earnings) needs a negative value balance and an unshield a positive one; a
// zero or wrong-signed balance is refused before any proof is looked at
// (docs/CHAIN.md "x/shielded": the value balance is the bundle's own).
func TestShield_valueBalanceSignRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	before := queryPool(t, c)
	cases := []struct {
		name    string
		msg     chain.Msg
		want    string
		balance int64
	}{
		{"MsgShield with a positive balance", shieldMsg(k.Address, nil), wantShieldSign, bigBalance},
		{"MsgShield with a zero balance", shieldMsg(k.Address, nil), wantShieldSign, 0},
		{"MsgShieldEarnings with a positive balance", shieldEarningsMsg(k.Address, nil), wantShieldSign, bigBalance},
		{"MsgShieldEarnings with a zero balance", shieldEarningsMsg(k.Address, nil), wantShieldSign, 0},
		{"MsgUnshield with a negative balance", unshieldMsg(k.Address, nil, nil), wantUnshieldSign, -bigBalance},
		{"MsgUnshield with a zero balance", unshieldMsg(k.Address, nil, nil), wantUnshieldSign, 0},
	}
	for _, tc := range cases {
		msg := tc.msg
		msg["bundle"] = b64(newBundle(t, bundleSpec{actions: 1, valueBalance: tc.balance}))
		chain.RequireRefused(t, tc.name, c.Submit(t, k, signedOpts, msg), tc.want)
	}
	chain.RequireRefused(t, "MsgUnshield of no more than the nullifier fee",
		c.Submit(t, k, signedOpts, unshieldMsg(k.Address, newBundle(t, bundleSpec{actions: 1, valueBalance: 1}), nil)), wantAmountSmall)
	requirePoolUntouched(t, c, before, "refused value balances")
}

// proofLengthAt is where a one-action bundle's CompactSize of the proof
// length starts: the action count, the action, flags, value balance, anchor.
const proofLengthAt = 1 + actionBytes + 1 + 8 + fieldBytes

// malformed are bundles the chain's framing check refuses whatever the
// message: none is a canonical Ironwood bundle.
func malformed(t *testing.T, maxActions uint64) map[string][]byte {
	t.Helper()
	good := newBundle(t, bundleSpec{actions: 1, valueBalance: -bigBalance})
	wrongProof := append([]byte(nil), good...)
	binary.LittleEndian.PutUint16(wrongProof[proofLengthAt+1:], binary.LittleEndian.Uint16(wrongProof[proofLengthAt+1:])+1)
	return map[string][]byte{
		"an empty bundle":                   nil,
		"random bytes":                      randomBytes(t, 100),
		"zero actions":                      {0},
		"a bundle cut one byte short":       good[:len(good)-1],
		"a bundle with a trailing byte":     append(append([]byte(nil), good...), 0),
		"a proof length not canonical":      wrongProof,
		"more actions than the chain takes": newBundle(t, bundleSpec{actions: int(maxActions) + 1, valueBalance: -bigBalance}),
	}
}

// TestShield_malformedBundlesRefused: every signed message refuses a bundle
// whose framing is not canonical (empty, cut, padded, a proof of the wrong
// length, more actions than max_actions_per_bundle), and nothing enters the
// pool.
func TestShield_malformedBundlesRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	before := queryPool(t, c)
	for kind, build := range signedKinds {
		for name, bundle := range malformed(t, queryParams(t, c).MaxActions) {
			chain.RequireRefused(t, kind+" with "+name, c.Submit(t, k, signedOpts, build(k.Address, bundle)), wantBundleSize)
		}
	}
	requirePoolUntouched(t, c, before, "malformed bundles")
}

// TestUnshield_targetsRefused: an unshield goes only to a target its signer
// owns and that has its fields: unspecified, a bond without a validator, a
// node bond without a node id or a role, and the two targets that are not
// linked (deposit, contract) are refused before the bundle is read.
func TestUnshield_targetsRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 2, chain.Orama(1))
	before := queryPool(t, c)
	bundle := newBundle(t, bundleSpec{actions: 1, valueBalance: bigBalance})
	cases := []struct {
		name   string
		fields map[string]any
		want   string
	}{
		{"an unspecified target", map[string]any{"target": "UNSHIELD_TARGET_UNSPECIFIED"}, "unshield target is not valid"},
		{"a bond target with no validator", map[string]any{"target": "UNSHIELD_TARGET_BOND"}, "bond target needs a validator operator address"},
		{"a bond target with a bad validator", map[string]any{"target": "UNSHIELD_TARGET_BOND", "validator": k.Address}, "bond target needs a validator operator address"},
		{"a node bond target with no node id", map[string]any{"target": "UNSHIELD_TARGET_NODE_BOND", "role": chain.RoleStorage}, "node bond target"},
		{"a node bond target with no role", map[string]any{"target": "UNSHIELD_TARGET_NODE_BOND", "node_id": "e2e-node"}, "node bond target needs a role"},
		{"a deposit target", map[string]any{"target": "UNSHIELD_TARGET_DEPOSIT"}, "unshield target is not linked"},
		{"a contract target", map[string]any{"target": "UNSHIELD_TARGET_CONTRACT"}, "unshield target is not linked"},
	}
	for _, tc := range cases {
		chain.RequireRefused(t, tc.name, c.Submit(t, k, signedOpts, unshieldMsg(k.Address, bundle, tc.fields)), tc.want)
	}
	requirePoolUntouched(t, c, before, "refused unshield targets")
}

// TestShield_aShieldedMessageIsAloneInItsTx: a signed shielded message beside
// another message (a second shielded message, or any other) is refused before
// any fee is taken (ante.ShapeDecorator).
func TestShield_aShieldedMessageIsAloneInItsTx(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	before := queryPool(t, c)
	bundle := newBundle(t, bundleSpec{actions: 1, valueBalance: -bigBalance})
	pairs := map[string][]chain.Msg{
		"two shields":                     {shieldMsg(k.Address, bundle), shieldEarningsMsg(k.Address, bundle)},
		"a shield and a nodes message":    {shieldMsg(k.Address, bundle), chain.RegisterOperatorMsg(k.Address)},
		"a nodes message and an unshield": {chain.RegisterOperatorMsg(k.Address), unshieldMsg(k.Address, bundle, nil)},
	}
	for name, msgs := range pairs {
		chain.RequireRefused(t, name, c.Submit(t, k, signedOpts, msgs...), wantOnlyMessage)
	}
	requirePoolUntouched(t, c, before, "shielded messages beside others")
}

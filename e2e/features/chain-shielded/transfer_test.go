//go:build e2e_fleet

package chainshielded

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// transferGas is the gas a signer-less transfer of actions actions must
// declare: exactly action_gas x actions (docs/whitepaper/technical-reference/vol2/43-the-shielded-pool.md "x/shielded").
func transferGas(p shieldedParams, actions int) uint64 { return p.ActionGas * uint64(actions) }

// duplicateNullifier is one nullifier used by two actions of a bundle.
var duplicateNullifier = [fieldBytes]byte{0xd0, 0x0b, 0x1e}

// TestShieldedTransfer_signerlessShapeRefused: a signer-less transfer names
// the protocol's fixed signer, carries no memo and no fee, and declares
// exactly its gas; each deviation is refused at the mempool before any proof
// work (ante.SignerlessDecorator).
func TestShieldedTransfer_signerlessShapeRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.Validator(t, c.Node(t, 0))
	n := c.Node(t, 1)
	p := queryParams(t, c)
	before := queryPool(t, c)
	bundle := newBundle(t, bundleSpec{actions: 1, valueBalance: bigBalance})
	gas := chain.TxOptions{Gas: transferGas(p, 1)}
	cases := []struct {
		name string
		msg  chain.Msg
		opts chain.TxOptions
		want string
	}{
		{"a signer that is not the protocol address", transferMsg(k.Address, bundle), gas, "must name the protocol signer address"},
		{"an empty bundle", transferMsg(signerless(), nil), gas, wantBundleSize},
		{"a memo", transferMsg(signerless(), bundle), chain.TxOptions{Gas: gas.Gas, Memo: "e2e"}, "carries no memo"},
		{"a fee", transferMsg(signerless(), bundle), chain.TxOptions{Gas: gas.Gas, Mode: chain.FeeAbsolute, FeeAmount: "1"}, "declares no fee"},
	}
	for _, tc := range cases {
		chain.RequireRefused(t, tc.name, c.BroadcastUnsigned(t, n, tc.opts, tc.msg), tc.want)
	}
	requirePoolUntouched(t, c, before, "refused signer-less shapes")
}

// TestShieldedTransfer_feeAndNullifierRules: a transfer's value balance is
// its fee, so it cannot be negative and must cover the base fee of its gas
// plus the nullifier fee of every action; a bundle that spends the same
// nullifier twice is refused.
func TestShieldedTransfer_feeAndNullifierRules(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 2)
	p := queryParams(t, c)
	before := queryPool(t, c)
	cases := []struct {
		name    string
		spec    bundleSpec
		want    string
		actions int
	}{
		{"a negative value balance", bundleSpec{actions: 1, valueBalance: -bigBalance}, wantTransferSign, 1},
		{"a fee of one norama", bundleSpec{actions: 1, valueBalance: 1}, wantFeeTooLow, 1},
		{"a zero fee", bundleSpec{actions: 1, valueBalance: 0}, wantFeeTooLow, 1},
		{"a nullifier used twice", bundleSpec{actions: 2, valueBalance: bigBalance,
			nullifiers: [][fieldBytes]byte{duplicateNullifier, duplicateNullifier}}, wantDuplicateNf, 2},
	}
	for _, tc := range cases {
		opts := chain.TxOptions{Gas: transferGas(p, tc.actions)}
		msg := transferMsg(signerless(), newBundle(t, tc.spec))
		chain.RequireRefused(t, tc.name, c.BroadcastUnsigned(t, n, opts, msg), tc.want)
	}
	requirePoolUntouched(t, c, before, "refused transfer fees")
}

// TestShield_wellFormedBundlesRefusedAtTheAnchorGate: a bundle that passes
// every cheap rule (framing, value balance, fee, nullifiers) is stopped at
// the anchor gate, which Admit runs before any verifier: a node built without
// the Orchard library cannot hash the note tree at all (tree stub: "not
// linked"), and one with it finds no anchor the bundle's random one matches.
// Shield, shield from earnings, unshield and a signer-less transfer are each
// refused there, the pool stays empty and the nullifiers stay unspent. The
// verifiers themselves are not reached: an empty tree has no real anchor to
// build against, so their refusal (and every success path) needs a run chain
// built with the Orchard library and a wallet-built bundle.
func TestShield_wellFormedBundlesRefusedAtTheAnchorGate(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	p := queryParams(t, c)
	before := queryPool(t, c)
	nullifier := randomBytes(t, fieldBytes)
	var nf [fieldBytes]byte
	copy(nf[:], nullifier)
	shieldB := newBundle(t, bundleSpec{actions: 1, valueBalance: -bigBalance, nullifiers: [][fieldBytes]byte{nf}})
	unshieldB := newBundle(t, bundleSpec{actions: 1, valueBalance: bigBalance, nullifiers: [][fieldBytes]byte{nf}})
	requireAnchorGate(t, "MsgShield", c.Submit(t, k, signedOpts, shieldMsg(k.Address, shieldB)))
	requireAnchorGate(t, "MsgShieldEarnings", c.Submit(t, k, signedOpts, shieldEarningsMsg(k.Address, shieldB)))
	requireAnchorGate(t, "MsgUnshield", c.Submit(t, k, signedOpts, unshieldMsg(k.Address, unshieldB, nil)))
	requireAnchorGate(t, "MsgShieldedTransfer", c.BroadcastUnsigned(t, c.Node(t, 0),
		chain.TxOptions{Gas: transferGas(p, 1)}, transferMsg(signerless(), unshieldB)))
	a := c.ABCIQuery(t, c.Node(t, 0), shieldedQuery+"NullifierSpent", chain.PB{}.Bytes(1, nullifier))
	if f, err := chain.DecodePB(a.Value); a.Code != 0 || err != nil || f.Varint(1) != 0 {
		t.Errorf("the nullifier of a refused bundle is spent: code %d %x (%v)", a.Code, a.Value, err)
	}
	requirePoolUntouched(t, c, before, "well-formed bundles refused at the anchor gate")
}

//go:build e2e_fleet

package chainshielded

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Sizes of a canonical Ironwood (Zcash v6) Orchard bundle
// (chain/x/shielded/bundle): an action is cv, nf, rk, cmx and epk (32 bytes
// each), the encrypted note (580) and the out ciphertext (80); the effecting
// data ends with flags, the value balance and the anchor; the proof is
// 2720 + 2272 per action bytes; every action has a spend authorization
// signature and the bundle has a binding signature (64 bytes each).
const (
	fieldBytes     = 32
	encBytes       = 580
	outBytes       = 80
	actionBytes    = 5*fieldBytes + encBytes + outBytes
	nullifierAt    = fieldBytes
	flagsByte      = 0x03
	proofBase      = 2720
	proofPerAction = 2272
	sigBytes       = 64
	// compactTwoByte introduces a CompactSize held in the next two bytes.
	compactTwoByte = 0xfd
)

// Type URLs of x/shielded's messages.
const (
	typeShield           = "/orama.shielded.v1.MsgShield"
	typeShieldEarnings   = "/orama.shielded.v1.MsgShieldEarnings"
	typeUnshield         = "/orama.shielded.v1.MsgUnshield"
	typeShieldedTransfer = "/orama.shielded.v1.MsgShieldedTransfer"
)

// Fee top-up is the one unshield target that needs no other field.
const targetFeeTopup = "UNSHIELD_TARGET_FEE_TOPUP"

// Refusal texts (chain/x/shielded/types/errors.go and verify/verify.go).
const (
	wantBundleSize   = "shielded bundle has an unacceptable size"
	wantShieldSign   = "shielding needs a negative value balance"
	wantUnshieldSign = "unshielding needs a positive value balance"
	wantTransferSign = "cannot be negative"
	wantFeeTooLow    = "below the required fee"
	wantDuplicateNf  = "bundle repeats a nullifier"
	wantAmountSmall  = "does not cover the nullifier fees"
	wantOnlyMessage  = "must be the only message in its tx"
	wantNotLinked    = "shielded proof verifier is not linked"
	wantAnchor       = "bundle anchor is not in the anchor window"
)

// bigBalance is a value balance (norama) above every fee floor of a default
// chain: 1000 ORAMA.
const bigBalance = 1000 * chain.NoramaPerOrama

// bundleSpec shapes a structurally canonical bundle. Its proof and
// signatures are zeros and its notes are random bytes: the chain reads the
// framing, the value balance, the nullifiers and the anchor before it runs a
// verifier, and no verifier would accept it.
type bundleSpec struct {
	actions      int
	valueBalance int64
	// nullifiers are placed in the first actions; the others are random.
	nullifiers [][fieldBytes]byte
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("failed to read randomness: %v", err)
	}
	return b
}

func newBundle(t *testing.T, s bundleSpec) []byte {
	t.Helper()
	if s.actions < 1 || s.actions >= compactTwoByte {
		t.Fatalf("a bundle of %d actions cannot be framed here", s.actions)
	}
	b := []byte{byte(s.actions)}
	for i := 0; i < s.actions; i++ {
		action := randomBytes(t, actionBytes)
		if i < len(s.nullifiers) {
			copy(action[nullifierAt:], s.nullifiers[i][:])
		}
		b = append(b, action...)
	}
	b = append(b, flagsByte)
	b = binary.LittleEndian.AppendUint64(b, uint64(s.valueBalance))
	b = append(b, randomBytes(t, fieldBytes)...)
	proof := proofBase + proofPerAction*s.actions
	if proof > 0xffff {
		t.Fatalf("a proof of %d bytes needs a wider CompactSize than this builder writes", proof)
	}
	b = append(b, compactTwoByte)
	b = binary.LittleEndian.AppendUint16(b, uint16(proof))
	return append(b, make([]byte, proof+(s.actions+1)*sigBytes)...)
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func shieldMsg(signer string, bundle []byte) chain.Msg {
	return chain.NewMsg(typeShield, map[string]any{"signer": signer, "bundle": b64(bundle)})
}

func shieldEarningsMsg(signer string, bundle []byte) chain.Msg {
	return chain.NewMsg(typeShieldEarnings, map[string]any{"signer": signer, "bundle": b64(bundle)})
}

func unshieldMsg(signer string, bundle []byte, fields map[string]any) chain.Msg {
	m := map[string]any{"signer": signer, "bundle": b64(bundle), "target": targetFeeTopup}
	for k, v := range fields {
		m[k] = v
	}
	return chain.NewMsg(typeUnshield, m)
}

func transferMsg(signer string, bundle []byte) chain.Msg {
	return chain.NewMsg(typeShieldedTransfer, map[string]any{"signer": signer, "bundle": b64(bundle)})
}

// signerless is the address a signer-less transfer names: the module
// address of "shielded" (types.SignerlessAddress).
func signerless() string { return chain.ModuleAddress("shielded") }

// anyOf is true when log mentions one of wants.
func anyOf(log string, wants ...string) bool {
	for _, w := range wants {
		if strings.Contains(log, w) {
			return true
		}
	}
	return false
}

// requireAnchorGate checks the refusal of a well-formed bundle at the anchor
// gate (keeper.Admit checks the anchor before any verifier): a node built
// without the Orchard library cannot compute the tree root the anchor is
// compared with (ErrVerifierNotLinked from the tree stub); one with it
// refuses an anchor the tree never had.
func requireAnchorGate(t *testing.T, what string, r chain.Result) {
	t.Helper()
	chain.RequireRefused(t, what, r)
	if !anyOf(r.Log, wantNotLinked, wantAnchor) {
		t.Fatalf("%s: refusal is neither %q nor %q: %s", what, wantNotLinked, wantAnchor, r)
	}
}

// shieldedParams is orama.shielded.v1.Params.
type shieldedParams struct {
	AnchorWindow, ActionGas, MaxActions, MaxSignerless uint64
	NullifierFee, UnshieldFloor, MaxFeeTopup, QueueCap string
}

const shieldedQuery = "/orama.shielded.v1.Query/"

func queryFields(t *testing.T, c *chain.Chain, method string, req chain.PB) chain.Fields {
	t.Helper()
	return queryFieldsOn(t, c, c.Node(t, 0), method, req)
}

// queryFieldsOn asks one validator.
func queryFieldsOn(t *testing.T, c *chain.Chain, n fleet.Node, method string, req chain.PB) chain.Fields {
	t.Helper()
	a := c.ABCIQuery(t, n, shieldedQuery+method, req)
	if a.Code != 0 {
		t.Fatalf("shielded %s: code %d: %s", method, a.Code, a.Log)
	}
	f, err := chain.DecodePB(a.Value)
	if err != nil {
		t.Fatalf("shielded %s: %v", method, err)
	}
	return f
}

func queryParams(t *testing.T, c *chain.Chain) shieldedParams {
	t.Helper()
	p, ok := queryFields(t, c, "Params", chain.PB{}).Msg(1)
	if !ok {
		t.Fatalf("shielded Params carries no params")
	}
	return shieldedParams{AnchorWindow: p.Varint(1), NullifierFee: p.Str(2), ActionGas: p.Varint(3), MaxActions: p.Varint(4),
		UnshieldFloor: p.Str(5), MaxFeeTopup: p.Str(6), QueueCap: p.Str(7), MaxSignerless: p.Varint(8)}
}

// poolState is the pool and the nullifier set as the queries report them.
type poolState struct {
	pools, queued int
	treeSize      uint64
	nullifiers    uint64
	accumulator   string
	anchors       uint64
}

func (p poolState) String() string {
	return fmt.Sprintf("%d pools, %d queued, %d notes, %d nullifiers, accumulator %s", p.pools, p.queued, p.treeSize, p.nullifiers, p.accumulator)
}

func queryPool(t *testing.T, c *chain.Chain) poolState {
	t.Helper()
	pools := queryFields(t, c, "Pools", chain.PB{})
	tree := queryFields(t, c, "TreeState", chain.PB{})
	acc := ""
	if v, ok := tree[4]; ok {
		acc = fmt.Sprintf("%x", v[0])
	}
	return poolState{pools: len(pools[1]), queued: len(pools[2]), treeSize: tree.Varint(1), anchors: tree.Varint(3),
		nullifiers: tree.Varint(5), accumulator: acc}
}

// requirePoolUntouched fails when the pool state is not what it was before.
func requirePoolUntouched(t *testing.T, c *chain.Chain, before poolState, after string) {
	t.Helper()
	if got := queryPool(t, c); got.String() != before.String() {
		t.Fatalf("the pool changed after %s: before %s, now %s", after, before, got)
	}
	c.RequireInvariants(t, after)
}

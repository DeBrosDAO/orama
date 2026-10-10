//go:build e2e_fleet

package chainshielded

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// wantNullifierLen is the length of a nullifier (chain/x/shielded/bundle NodeLen).
const wantNullifierLen = 32

// TestShieldedQueries_paramsAreGenesisSetAndPositive: x/shielded's
// parameters are set at genesis (no message changes them, docs/whitepaper/technical-reference/vol2/43-the-shielded-pool.md
// "x/shielded"): every one is present and positive, and the biggest bundle
// (max_actions x action_gas) fits the gas arithmetic. The same answer comes
// from every validator.
func TestShieldedQueries_paramsAreGenesisSetAndPositive(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	p := queryParams(t, c)
	for name, v := range map[string]uint64{"anchor_window_blocks": p.AnchorWindow, "action_gas": p.ActionGas,
		"max_actions_per_bundle": p.MaxActions, "max_signerless_per_block": p.MaxSignerless} {
		if v == 0 {
			t.Errorf("shielded param %s is zero: %+v", name, p)
		}
	}
	for name, v := range map[string]string{"nullifier_fee": p.NullifierFee, "unshield_floor": p.UnshieldFloor,
		"max_fee_topup": p.MaxFeeTopup, "queue_per_address_cap": p.QueueCap} {
		if v == "" || v == "0" || strings.HasPrefix(v, "-") {
			t.Errorf("shielded param %s is %q, want a positive amount: %+v", name, v, p)
		}
	}
	for _, n := range c.Nodes()[1:] {
		a := c.ABCIQuery(t, n, shieldedQuery+"Params", chain.PB{})
		first := c.ABCIQuery(t, c.Node(t, 0), shieldedQuery+"Params", chain.PB{})
		if a.Code != 0 || string(a.Value) != string(first.Value) {
			t.Errorf("%s answers Params differently from %s (code %d)", n.Name, c.Node(t, 0).Name, a.Code)
		}
	}
}

// TestShieldedQueries_poolStartsEmptyAndInvariantsHold: with no bundle ever
// accepted (the run's chain holds no bank balance and takes no proof, see
// TestShield_wellFormedBundlesRefusedAtTheAnchorGate) the pool has no balance, no
// queued unshield, no note and no nullifier; the module's invariants hold on
// every validator (module account equals pools plus queue, no negative
// pool, the nullifier database folds to the accumulator).
func TestShieldedQueries_poolStartsEmptyAndInvariantsHold(t *testing.T) {
	t.Parallel()
	chain.RequireFreshChain(t)
	c := chain.New(t)
	got := queryPool(t, c)
	if got.pools != 0 || got.queued != 0 || got.treeSize != 0 || got.nullifiers != 0 {
		t.Errorf("the pool of a run chain is not empty: %s", got)
	}
	for _, n := range c.Nodes() {
		f := queryFieldsOn(t, c, n, "Invariants", chain.PB{})
		if f.Varint(1) != 1 || f.Varint(2) != 1 || f.Varint(3) != 1 {
			t.Errorf("%s: shielded invariants balance_matches=%d pools_non_negative=%d accumulator_matches=%d (%s)",
				n.Name, f.Varint(1), f.Varint(2), f.Varint(3), f.Str(4))
		}
	}
}

// TestShieldedQueries_nullifierSpent: a nullifier no bundle inserted is not
// spent, and a nullifier that is not 32 bytes is an InvalidArgument query
// error, not "not spent".
func TestShieldedQueries_nullifierSpent(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	fresh := randomBytes(t, wantNullifierLen)
	a := c.ABCIQuery(t, c.Node(t, 0), shieldedQuery+"NullifierSpent", chain.PB{}.Bytes(1, fresh))
	if a.Code != 0 {
		t.Fatalf("NullifierSpent of a fresh nullifier: code %d: %s", a.Code, a.Log)
	}
	if f, err := chain.DecodePB(a.Value); err != nil || f.Varint(1) != 0 {
		t.Errorf("a fresh nullifier is reported spent: %x (%v)", a.Value, err)
	}
	for name, bad := range map[string][]byte{"31 bytes": fresh[:wantNullifierLen-1], "33 bytes": append(fresh, 0), "empty": nil} {
		r := c.ABCIQuery(t, c.Node(t, 0), shieldedQuery+"NullifierSpent", chain.PB{}.Bytes(1, bad))
		if r.Code == 0 || !strings.Contains(r.Log, "nullifier must be 32 bytes") {
			t.Errorf("NullifierSpent of %s: code %d %q, want the 32-byte refusal", name, r.Code, r.Log)
		}
	}
}

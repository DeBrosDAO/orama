//go:build e2e_fleet

package chainservices

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// grantSpec is a MsgGrantDealAuthorization body.
type grantSpec struct {
	limit              int64
	maxPiece, maxEpoch uint64
	replicas           uint32
	expiry             uint64
}

func grantMsg(granter, grantee string, g grantSpec) chain.Msg {
	return chain.NewMsg("/orama.storage.v1.MsgGrantDealAuthorization", map[string]any{
		"signer": granter, "grantee": grantee, "spend_limit": fmt.Sprint(g.limit), "period_epochs": "0",
		"max_piece_bytes": fmt.Sprint(g.maxPiece), "max_duration_epochs": fmt.Sprint(g.maxEpoch),
		"replicas": g.replicas, "expiry_epoch": fmt.Sprint(g.expiry),
	})
}

func revokeGrantMsg(granter, grantee string) chain.Msg {
	return chain.NewMsg("/orama.storage.v1.MsgRevokeDealAuthorization", map[string]any{"signer": granter, "grantee": grantee})
}

// TestStorageGrant_capsSpendPieceDurationAndReplicas: a deal allowance (not
// SDK authz: docs/CHAIN.md "orama storage grant") caps spend, piece size,
// duration and replica count; a deal beyond any cap is refused before any
// coin moves, and one inside every cap is charged to the GRANTER's bank
// balance (refused here: the granter holds only earnings). The Authorization
// query shows the grant with nothing spent (the refused deal reverted its
// spend). Revoke removes it, a second revoke and a deal on it are refused,
// and an expired grant is refused.
func TestStorageGrant_capsSpendPieceDurationAndReplicas(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	granter := c.FundedValidator(t, 0, chain.Orama(1))
	grantee := c.FundedValidator(t, 2, chain.Orama(1))
	t.Cleanup(func() { revokeAtCleanup(t, c, granter, grantee.Address) })
	g := grantSpec{limit: 50_000_000, maxPiece: 2 * minDealBytes, maxEpoch: 5, replicas: minReplicas}
	chain.RequireOK(t, "grant", c.Submit(t, granter, chain.TxOptions{}, grantMsg(granter.Address, grantee.Address, g)))
	deal := func(mut func(*dealSpec)) chain.Result {
		d := publicPin(t, grantee.Address)
		d.granter = granter.Address
		mut(&d)
		return c.Submit(t, grantee, chain.TxOptions{}, d.msg(t))
	}
	chain.RequireRefused(t, "over the spend limit", deal(func(d *dealSpec) { d.price = "100000000" }), "exceeds remaining limit")
	chain.RequireRefused(t, "other replica count", deal(func(d *dealSpec) { d.replicas = minReplicas + 1 }), "do not match the grant's fixed count")
	chain.RequireRefused(t, "longer than the grant", deal(func(d *dealSpec) { d.duration = g.maxEpoch + 1 }), "exceeds the grant maximum")
	chain.RequireRefused(t, "bigger piece than the grant", deal(func(d *dealSpec) { d.pieces = []map[string]any{piece(t, g.maxPiece+1)} }), "grant maximum is")
	chain.RequireRefused(t, "inside the grant, granter unfunded", deal(func(*dealSpec) {}), "insufficient funds")
	requireAuthorization(t, c, granter, grantee.Address, g.limit)
	chain.RequireOK(t, "revoke", c.Submit(t, granter, chain.TxOptions{}, revokeGrantMsg(granter.Address, grantee.Address)))
	chain.RequireRefused(t, "revoke twice", c.Submit(t, granter, chain.TxOptions{}, revokeGrantMsg(granter.Address, grantee.Address)), "no deal authorization")
	chain.RequireRefused(t, "deal on a revoked grant", deal(func(*dealSpec) {}), "no deal authorization")
	g.expiry = uint64(c.Epoch(t, granter.Node, 0).CurrentEpoch.Int64())
	chain.RequireOK(t, "grant expiring now", c.Submit(t, granter, chain.TxOptions{}, grantMsg(granter.Address, grantee.Address, g)))
	chain.RequireRefused(t, "deal on an expired grant", deal(func(*dealSpec) {}), "expired at epoch")
	c.RequireInvariants(t, "deal authorizations")
}

// TestStorageGrant_shapeRefusals: a grant to yourself, a zero limit, a zero
// piece cap, a duration cap of 0 or over 1e6 and a replica count outside
// [3, 32] are refused (x/storage/types/msgs.go).
func TestStorageGrant_shapeRefusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	other := c.Validator(t, c.Node(t, 1)).Address
	ok := grantSpec{limit: 1, maxPiece: 1, maxEpoch: 1, replicas: minReplicas}
	cases := map[string]struct {
		grantee string
		g       grantSpec
		want    string
	}{
		"grant to self":      {k.Address, ok, "granter and grantee must differ"},
		"zero limit":         {other, grantSpec{0, 1, 1, minReplicas, 0}, "spend_limit must be positive"},
		"zero piece cap":     {other, grantSpec{1, 0, 1, minReplicas, 0}, "max_piece_bytes must be positive"},
		"zero duration cap":  {other, grantSpec{1, 1, 0, minReplicas, 0}, "max_duration_epochs must be in"},
		"duration cap > 1e6": {other, grantSpec{1, 1, maxDuration + 1, minReplicas, 0}, "max_duration_epochs must be in"},
		"two replicas":       {other, grantSpec{1, 1, 1, minReplicas - 1, 0}, "grant replicas must be in"},
		"33 replicas":        {other, grantSpec{1, 1, 1, maxReplicas + 1, 0}, "grant replicas must be in"},
	}
	for name, tc := range cases {
		chain.RequireRefused(t, name, c.Submit(t, k, chain.TxOptions{}, grantMsg(k.Address, tc.grantee, tc.g)), tc.want)
	}
}

func requireAuthorization(t *testing.T, c *chain.Chain, granter chain.Key, grantee string, limit int64) {
	t.Helper()
	a := c.ABCIQuery(t, granter.Node, "/orama.storage.v1.Query/Authorization", chain.PB{}.Text(1, granter.Address).Text(2, grantee))
	if a.Code != 0 {
		t.Fatalf("Authorization: code %d %s", a.Code, a.Log)
	}
	f, err := chain.DecodePB(a.Value)
	if err != nil {
		t.Fatal(err)
	}
	auth, ok := f.Msg(1)
	if !ok || auth.Str(1) != granter.Address || auth.Str(2) != grantee || auth.Str(3) != fmt.Sprint(limit) {
		t.Fatalf("authorization %v, want %s -> %s limit %d", auth, granter.Address, grantee, limit)
	}
	if spent := auth.Str(4); spent != "0" && spent != "" {
		t.Errorf("authorization spent %s after only refused deals", spent)
	}
}

func revokeAtCleanup(t *testing.T, c *chain.Chain, granter chain.Key, grantee string) {
	a := c.ABCIQuery(t, granter.Node, "/orama.storage.v1.Query/Authorization", chain.PB{}.Text(1, granter.Address).Text(2, grantee))
	if a.Code != 0 {
		return
	}
	c.CleanupSubmit(t, granter, "revoke the deal authorization", revokeGrantMsg(granter.Address, grantee))
}

// TestStorageHotKey_onlyTheHotKeyAnswersASlot: MsgAcceptDeal, MsgDeclineDeal,
// MsgSubmitProofs and MsgReleaseReplica are signed by the node's HOT key
// (docs/CHAIN.md: "The signer of those two is the node's hot key"): the
// operator is refused, and the hot key gets past the check to the slot,
// which does not exist (no deal can be funded on the run chain, so accepting
// a real slot, a bad proof on a real challenge and the release rate limit
// are blocked). A proof leaf of the wrong size and a release reason other
// than LEGAL are refused before any state is read.
func TestStorageHotKey_onlyTheHotKeyAnswersASlot(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, op)
	hot := c.FundedValidator(t, 1, chain.Orama(1))
	id, _ := c.RegisterNodeWithHotKey(t, op, hot.Address, []string{chain.RoleStorage}, "storage")
	const deal = "987654321"
	leaf := base64.StdEncoding.EncodeToString(make([]byte, leafSize))
	msgs := func(signer string) map[string]chain.Msg {
		return map[string]chain.Msg{
			"accept":  chain.NewMsg("/orama.storage.v1.MsgAcceptDeal", map[string]any{"signer": signer, "node_id": id, "deal_id": deal, "slot": 0}),
			"decline": chain.NewMsg("/orama.storage.v1.MsgDeclineDeal", map[string]any{"signer": signer, "node_id": id, "deal_id": deal, "slot": 0, "reason": "e2e"}),
			"prove": chain.NewMsg("/orama.storage.v1.MsgSubmitProofs", map[string]any{"signer": signer, "node_id": id,
				"proofs": []any{map[string]any{"deal_id": deal, "slot": 0, "leaf_index": "0", "leaf": leaf, "siblings": []any{}}}}),
			"release": chain.NewMsg("/orama.storage.v1.MsgReleaseReplica", map[string]any{"signer": signer, "node_id": id, "deal_id": deal, "slot": 0, "reason": "RELEASE_REASON_LEGAL"}),
		}
	}
	for name, m := range msgs(op.Address) {
		chain.RequireRefused(t, name+" by the operator", c.Submit(t, op, chain.TxOptions{}, m), "is not the hot key of node "+id)
	}
	for name, m := range msgs(hot.Address) {
		chain.RequireRefused(t, name+" by the hot key on no slot", c.Submit(t, hot, chain.TxOptions{}, m), "deal "+deal+" slot 0 does not exist")
	}
	short := chain.NewMsg("/orama.storage.v1.MsgSubmitProofs", map[string]any{"signer": hot.Address, "node_id": id,
		"proofs": []any{map[string]any{"deal_id": deal, "slot": 0, "leaf_index": "0", "leaf": leaf[:8]}}})
	chain.RequireRefused(t, "proof leaf of the wrong size", c.Submit(t, hot, chain.TxOptions{}, short), "leaf is")
	notLegal := chain.NewMsg("/orama.storage.v1.MsgReleaseReplica", map[string]any{"signer": hot.Address, "node_id": id, "deal_id": deal, "slot": 0, "reason": "RELEASE_REASON_UNSPECIFIED"})
	chain.RequireRefused(t, "release for no legal reason", c.Submit(t, hot, chain.TxOptions{}, notLegal), "release reason must be LEGAL")
	unknown := chain.NewMsg("/orama.storage.v1.MsgAcceptDeal", map[string]any{"signer": hot.Address, "node_id": "e2e-no-such-node", "deal_id": deal, "slot": 0})
	chain.RequireRefused(t, "accept for an unknown node", c.Submit(t, hot, chain.TxOptions{}, unknown), "failed to read hot key of e2e-no-such-node")
	requireNoSlotOrChallenge(t, c, id)
}

// requireNoSlotOrChallenge asks the Slot and Challenges queries (no CLI
// command: abci_query) about the node: no slot 987654321/0, and no challenge
// in the current epoch.
func requireNoSlotOrChallenge(t *testing.T, c *chain.Chain, nodeID string) {
	t.Helper()
	n := c.Node(t, 0)
	s := c.ABCIQuery(t, n, "/orama.storage.v1.Query/Slot", chain.PB{}.Uint(1, 987654321))
	if s.Code == 0 || !strings.Contains(s.Log, "does not exist") {
		t.Errorf("Slot of an unknown deal: code %d %q", s.Code, s.Log)
	}
	epoch := uint64(c.Epoch(t, n, 0).CurrentEpoch.Int64())
	ch := c.ABCIQuery(t, n, "/orama.storage.v1.Query/Challenges", chain.PB{}.Uint(1, epoch).Text(2, nodeID))
	if ch.Code != 0 {
		t.Fatalf("Challenges: code %d %s", ch.Code, ch.Log)
	}
	if f, err := chain.DecodePB(ch.Value); err != nil || len(f[1]) != 0 {
		t.Errorf("node %s has challenges without any slot: %v %v", nodeID, f, err)
	}
}

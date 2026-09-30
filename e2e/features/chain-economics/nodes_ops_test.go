//go:build e2e_fleet

package chaineconomics

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// gib is one GiB, the default probation capacity (docs/CHAIN.md "Genesis
// defaults": probation_capacity_bytes is 1 GiB).
const gib = 1 << 30

func nodeMsg(typ string, fields map[string]any) chain.Msg {
	return chain.NewMsg("/orama.nodes.v1."+typ, fields)
}

// TestNodesUpdate_rotatesHotKeyBindingsAndEndpoints: MsgUpdateNode rotates the
// hot key, replaces the whole binding set (the replaced pubkey is revoked for
// good) and the endpoints; an update that changes nothing, a hot key equal
// to the operator or a new hot key that did not sign its own binding, and an
// update by another account are refused
// (docs/CHAIN.md "x/nodes" Messages).
func TestNodesUpdate_rotatesHotKeyBindingsAndEndpoints(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := operator(t, c)
	id, old := c.RegisterTestNode(t, k, []string{chain.RoleRelay}, "relay")
	newHot, newProof := chain.HotKeyBinding(t, c.ID, k.Address)
	fresh := chain.Ed25519Binding(t, c.ID, k.Address, "relay")
	update := nodeMsg("MsgUpdateNode", map[string]any{"operator": k.Address, "node_id": id, "hot_key": newHot,
		"bindings": []any{fresh.JSON(), newProof.JSON()}, "set_endpoints": true, "endpoints": []string{"relay-new.example.com:443"},
		"set_region_hint": true, "region_hint": "eu-west"})
	chain.RequireOK(t, "update node", c.Submit(t, k, chain.TxOptions{}, update))
	v := queryNode(t, c, id)
	if v.Node.HotKey != newHot || len(v.Node.Endpoints) != 1 || v.Node.Endpoints[0] != "relay-new.example.com:443" || v.Node.RegionHint != "eu-west" {
		t.Errorf("node after update %+v", v.Node)
	}
	nothing := nodeMsg("MsgUpdateNode", map[string]any{"operator": k.Address, "node_id": id})
	chain.RequireRefused(t, "update that changes nothing", c.Submit(t, k, chain.TxOptions{}, nothing), "update changes nothing")
	selfHot := nodeMsg("MsgUpdateNode", map[string]any{"operator": k.Address, "node_id": id, "hot_key": k.Address})
	chain.RequireRefused(t, "hot key = operator", c.Submit(t, k, chain.TxOptions{}, selfHot), "hot-key")
	unproven, _ := chain.HotKeyBinding(t, c.ID, k.Address)
	noProof := nodeMsg("MsgUpdateNode", map[string]any{"operator": k.Address, "node_id": id, "hot_key": unproven})
	chain.RequireRefused(t, "a new hot key that proved nothing", c.Submit(t, k, chain.TxOptions{}, noProof), "hot-key")
	other := c.FundedValidator(t, 1, chain.Orama(1))
	foreign := nodeMsg("MsgUpdateNode", map[string]any{"operator": other.Address, "node_id": id, "set_region_hint": true, "region_hint": "x"})
	chain.RequireRefused(t, "update by another account", c.Submit(t, other, chain.TxOptions{}, foreign), "signer is not the operator")
	hot2, proof2 := chain.HotKeyBinding(t, c.ID, k.Address)
	stale := chain.NodeSpec{Operator: k.Address, NodeID: chain.UniqueID(t, "e2e-node-"), Roles: []string{chain.RoleRelay},
		HotKey: hot2, Bindings: append(append([]chain.Binding{}, old...), proof2)}
	chain.RequireRefused(t, "rotated-out pubkey on a new node", c.Submit(t, k, chain.TxOptions{}, chain.RegisterNodeMsg(stale)), "cannot be reused")
	c.RequireInvariants(t, "a node update")
}

// TestNodesCapacity_probationCapWithoutBond: a STORAGE node with no bond may
// declare up to probation_capacity_bytes (1 GiB) and not one byte more; a
// node without the STORAGE role cannot declare; an unknown node is not found
// (docs/CHAIN.md "MsgDeclareCapacity"; x/nodes BackedCapacity).
func TestNodesCapacity_probationCapWithoutBond(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := operator(t, c)
	storage, _ := c.RegisterTestNode(t, k, []string{chain.RoleStorage}, "storage")
	relay, _ := c.RegisterTestNode(t, k, []string{chain.RoleRelay}, "relay")
	declare := func(id string, bytes uint64) chain.Result {
		return c.Submit(t, k, chain.TxOptions{}, nodeMsg("MsgDeclareCapacity", map[string]any{
			"operator": k.Address, "node_id": id, "capacity_bytes": fmt.Sprint(bytes)}))
	}
	chain.RequireOK(t, "declare exactly 1 GiB", declare(storage, gib))
	var v struct {
		Node struct {
			Declared chain.Int `json:"declared_capacity_bytes"`
		} `json:"node"`
	}
	c.Query(t, k.Node, &v, "nodes", "node", storage)
	if v.Node.Declared.Int64() != gib {
		t.Errorf("declared %d, want %d", v.Node.Declared.Int64(), gib)
	}
	chain.RequireRefused(t, "declare 1 GiB + 1", declare(storage, gib+1), "declared capacity exceeds backed capacity")
	chain.RequireRefused(t, "declare on a relay node", declare(relay, 1), "does not have the STORAGE role")
	chain.RequireRefused(t, "declare on an unknown node", declare("e2e-no-such-node", 1), "not found")
	chain.RequireOK(t, "declare 0", declare(storage, 0))
	c.RequireInvariants(t, "capacity declarations")
}

// TestNodesBond_bankOnlyAndQueueRules: MsgBondNode moves norama from the
// operator's BANK balance (x/nodes/keeper/msg.go BondNode), which no run
// account holds (features/internal/chain funds.go), so a bond is refused
// with insufficient funds, while every check before the transfer still
// answers: a role the node lacks, an unknown node, and a non-positive
// amount. MsgUnbondNode above the (zero) bond is refused, and the node's
// unbonding queue stays empty.
func TestNodesBond_bankOnlyAndQueueRules(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := operator(t, c)
	id, _ := c.RegisterTestNode(t, k, []string{chain.RoleStorage}, "storage")
	bond := func(typ, node, role, amount string) chain.Result {
		return c.Submit(t, k, chain.TxOptions{}, nodeMsg(typ, map[string]any{
			"operator": k.Address, "node_id": node, "role": role, "amount": amount}))
	}
	one := chain.Orama(1).String()
	chain.RequireRefused(t, "bond from an empty bank", bond("MsgBondNode", id, chain.RoleStorage, one), "insufficient funds")
	chain.RequireRefused(t, "bond a role the node lacks", bond("MsgBondNode", id, chain.RoleRelay, one), "does not have role")
	chain.RequireRefused(t, "bond an unknown node", bond("MsgBondNode", "e2e-no-such-node", chain.RoleStorage, one), "not found")
	chain.RequireRefused(t, "bond zero", bond("MsgBondNode", id, chain.RoleStorage, "0"), "bond node")
	chain.RequireRefused(t, "unbond above the bond", bond("MsgUnbondNode", id, chain.RoleStorage, one), "exceeds")
	chain.RequireRefused(t, "unbond zero", bond("MsgUnbondNode", id, chain.RoleStorage, "0"), "unbond node")
	var q struct {
		Unbondings []any `json:"unbondings"`
	}
	c.Query(t, k.Node, &q, "nodes", "unbondings", id)
	if len(q.Unbondings) != 0 {
		t.Errorf("node %s has %d unbonding entries, want none", id, len(q.Unbondings))
	}
	if st := queryNode(t, c, id).Node.Status; st != "NODE_STATUS_REGISTERED" {
		t.Errorf("an unbonded node is %s, want REGISTERED (active needs a role at min_bond)", st)
	}
	c.RequireInvariants(t, "refused bonds")
}

// TestNodesWrongSigner_everyOwnerMessageRefused: MsgUpdateNode, MsgBondNode,
// MsgUnbondNode, MsgDeclareCapacity and MsgRetireNode naming someone else's
// node are refused (docs/CHAIN.md: "fail when the signer is not that
// operator"), and the node is untouched.
func TestNodesWrongSigner_everyOwnerMessageRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := operator(t, c)
	id, _ := c.RegisterTestNode(t, k, []string{chain.RoleStorage}, "storage")
	other := c.FundedValidator(t, 1, chain.Orama(1))
	msgs := map[string]chain.Msg{
		"retire":   chain.RetireNodeMsg(other.Address, id),
		"capacity": nodeMsg("MsgDeclareCapacity", map[string]any{"operator": other.Address, "node_id": id, "capacity_bytes": "1"}),
		"bond":     nodeMsg("MsgBondNode", map[string]any{"operator": other.Address, "node_id": id, "role": chain.RoleStorage, "amount": "1"}),
		"unbond":   nodeMsg("MsgUnbondNode", map[string]any{"operator": other.Address, "node_id": id, "role": chain.RoleStorage, "amount": "1"}),
	}
	for name, m := range msgs {
		chain.RequireRefused(t, name+" by another account", c.Submit(t, other, chain.TxOptions{}, m), "signer is not the operator")
	}
	if st := queryNode(t, c, id).Node.Status; st != "NODE_STATUS_REGISTERED" {
		t.Errorf("node %s is %s after refused foreign messages", id, st)
	}
}

// nodesParams is orama.nodes.v1.Params.
type nodesParams struct {
	Params struct {
		MinBond []struct {
			Role   string    `json:"role"`
			Amount chain.Int `json:"amount"`
		} `json:"min_bond"`
		BondPerGib             chain.Int `json:"bond_per_gib"`
		UnbondingSeconds       chain.Int `json:"unbonding_seconds"`
		DepositPerByte         chain.Int `json:"deposit_per_byte"`
		ProbationCapacityBytes chain.Int `json:"probation_capacity_bytes"`
		MinServiceVolumeBytes  chain.Int `json:"min_service_volume_bytes"`
		MaxEndpoints           chain.Int `json:"max_endpoints"`
		MaxBindings            chain.Int `json:"max_bindings"`
	} `json:"params"`
}

// TestNodesParams_documentedDefaults: docs/CHAIN.md "x/nodes" Genesis
// defaults: 1 ORAMA minimum for every role, 1 ORAMA per GiB, 21 days
// unbonding, 68359 norama per deposit byte, 1 GiB probation, 1 byte of
// service volume, at most 8 endpoints and 8 bindings.
func TestNodesParams_documentedDefaults(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	var p nodesParams
	c.Query(t, c.Node(t, 0), &p, "nodes", "params")
	q := p.Params
	if len(q.MinBond) != 6 {
		t.Errorf("min_bond lists %d roles, want the 6 roles", len(q.MinBond))
	}
	for _, b := range q.MinBond {
		if b.Amount.Cmp(chain.Orama(1)) != 0 {
			t.Errorf("min_bond[%s] = %s, want 1 ORAMA", b.Role, b.Amount.String())
		}
	}
	want := map[string][2]chain.Int{
		"bond_per_gib":             {q.BondPerGib, chain.Orama(1)},
		"unbonding_seconds":        {q.UnbondingSeconds, chain.NewInt(21 * 24 * 3600)},
		"deposit_per_byte":         {q.DepositPerByte, chain.NewInt(68359)},
		"probation_capacity_bytes": {q.ProbationCapacityBytes, chain.NewInt(gib)},
		"min_service_volume_bytes": {q.MinServiceVolumeBytes, chain.NewInt(1)},
		"max_endpoints":            {q.MaxEndpoints, chain.NewInt(8)},
		"max_bindings":             {q.MaxBindings, chain.NewInt(8)},
	}
	for name, v := range want {
		if v[0].Cmp(v[1]) != 0 {
			t.Errorf("%s = %s, want %s", name, v[0].String(), v[1].String())
		}
	}
}

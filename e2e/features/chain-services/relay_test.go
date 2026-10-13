//go:build e2e_fleet

package chainservices

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// TestRelayRegister_crossCertifiedRelay: an operator registers its node's Tor
// identity: the RSA fingerprint cross-signed by the node's ed25519 "relay"
// binding key and proved by the RSA identity key itself. The registration is
// stored and readable by fingerprint; the same fingerprint again, and the same
// node with another fingerprint, are refused. (x/relay has no message that
// removes a relay, so the record stays for the disposable run chain; the node
// itself is retired at cleanup.)
func TestRelayRegister_crossCertifiedRelay(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, op)
	id, bindings := c.RegisterTestNode(t, op, []string{chain.RoleRelay}, "relay")
	rsaID := chain.NewRelayRSAIdentity(t)
	reg := chain.ValidRelay(t, op.Address, id, rsaID, bindings[0].Priv)
	chain.RequireOK(t, "register a cross-certified relay", c.Submit(t, op, chain.TxOptions{}, reg.Register()))
	var v struct {
		Relay struct {
			NodeID   string `json:"node_id"`
			Operator string `json:"operator"`
		} `json:"relay"`
	}
	c.Query(t, op.Node, &v, "relay", "relay", hex.EncodeToString(rsaID.Fingerprint))
	if v.Relay.NodeID != id || v.Relay.Operator != op.Address {
		t.Errorf("relay record %+v", v.Relay)
	}
	chain.RequireRefused(t, "same fingerprint", c.Submit(t, op, chain.TxOptions{}, reg.Register()), "fingerprint already registered")
	second := chain.ValidRelay(t, op.Address, id, chain.NewRelayRSAIdentity(t), bindings[0].Priv)
	chain.RequireRefused(t, "same node, new fingerprint", c.Submit(t, op, chain.TxOptions{}, second.Register()), "is already registered")
	c.RequireInvariants(t, "a relay registration")
}

// TestRelayRegister_refusals: a cross-certificate by another key, over
// another fingerprint or node id, a fingerprint of the wrong size, an RSA key
// that is not the fingerprint's or a proof by another key, an operator that
// does not own the node, and a node that does not exist are refused before
// anything is stored.
func TestRelayRegister_refusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, op)
	id, bindings := c.RegisterTestNode(t, op, []string{chain.RoleRelay}, "relay")
	priv := bindings[0].Priv
	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaID, strangerRSA := chain.NewRelayRSAIdentity(t), chain.NewRelayRSAIdentity(t)
	other := c.FundedValidator(t, 1, chain.Orama(1))
	edit := func(f chain.RelayRegistration, with func(*chain.RelayRegistration)) chain.RelayRegistration {
		with(&f)
		return f
	}
	good := chain.ValidRelay(t, op.Address, id, rsaID, priv)
	cases := map[string]struct {
		k    chain.Key
		msg  chain.RelayRegistration
		want string
	}{
		"signed by another key": {op, edit(good, func(f *chain.RelayRegistration) {
			f.Ed25519Sig = chain.RelayCrossCert(stranger, id, rsaID.Fingerprint, false)
		}), "does not match the ed25519 cross-signature"},
		"over another fingerprint": {op, edit(good, func(f *chain.RelayRegistration) {
			f.Ed25519Sig = chain.RelayCrossCert(priv, id, chain.NewRelayRSAIdentity(t).Fingerprint, false)
		}), "does not match the ed25519 cross-signature"},
		"over another node id": {op, edit(good, func(f *chain.RelayRegistration) {
			f.Ed25519Sig = chain.RelayCrossCert(priv, id+"x", rsaID.Fingerprint, false)
		}), "does not match the ed25519 cross-signature"},
		"19-byte fingerprint":                           {op, edit(good, func(f *chain.RelayRegistration) { f.Fingerprint = rsaID.Fingerprint[:19] }), "rsa fingerprint must be 20 bytes"},
		"a non-exit relay's proofs replayed as an exit": {op, edit(good, func(f *chain.RelayRegistration) { f.Exit = true }), "does not match the ed25519 cross-signature"},
		"an exit cross-cert with a non-exit rsa proof":  {op, edit(chain.ValidRelayExit(t, op.Address, id, rsaID, priv, true), func(f *chain.RelayRegistration) { f.RSASig = good.RSASig }), "rsa identity does not prove the fingerprint"},
		"not the node's operator":                       {other, chain.ValidRelay(t, other.Address, id, rsaID, priv), "operator does not own node"},
		"unknown node":                                  {op, chain.ValidRelay(t, op.Address, "e2e-no-such-node", rsaID, priv), "e2e-no-such-node"},
		"empty node id":                                 {op, chain.ValidRelay(t, op.Address, "", rsaID, priv), "node id is empty"},
		"another key's fingerprint, the squatter's own key": {op, edit(good, func(f *chain.RelayRegistration) {
			f.Fingerprint = strangerRSA.Fingerprint
			f.Ed25519Sig = chain.RelayCrossCert(priv, id, strangerRSA.Fingerprint, false)
		}), "rsa identity does not prove the fingerprint"},
		"the right key, another key's signature": {op, edit(good, func(f *chain.RelayRegistration) {
			f.RSASig = strangerRSA.Proof(t, id, priv.Public().(ed25519.PublicKey), false)
		}), "rsa identity does not prove the fingerprint"},
		"a proof made for another node": {op, edit(good, func(f *chain.RelayRegistration) {
			f.RSASig = rsaID.Proof(t, id+"x", priv.Public().(ed25519.PublicKey), false)
		}), "rsa identity does not prove the fingerprint"},
		"no rsa key": {op, edit(good, func(f *chain.RelayRegistration) { f.RSAPublicKey, f.RSASig = nil, nil }), "rsa identity does not prove the fingerprint"},
	}
	for name, tc := range cases {
		chain.RequireRefused(t, name, c.Submit(t, tc.k, chain.TxOptions{}, tc.msg.Register()), tc.want)
	}
	if out := c.QueryFails(t, op.Node, "relay", "relay", hex.EncodeToString(rsaID.Fingerprint)); !chain.NotFound(out) {
		t.Errorf("a refused fingerprint is registered: %s", out)
	}
}

// TestRelayRegister_needsTheRelayRoleAndTheRelayKey: a node without ROLE_RELAY
// is refused (a node's roles are fixed at registration, so there is no way to
// add it), and so is a relay node whose ed25519 key is bound under another
// service than "relay": x/relay reads the Tor identity from that binding only,
// never from another key of the node.
func TestRelayRegister_needsTheRelayRoleAndTheRelayKey(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, op)
	rsaID := chain.NewRelayRSAIdentity(t)
	storageID, storageBindings := c.RegisterTestNode(t, op, []string{chain.RoleStorage}, "relay")
	storage := chain.ValidRelay(t, op.Address, storageID, rsaID, storageBindings[0].Priv)
	chain.RequireRefused(t, "a storage node", c.Submit(t, op, chain.TxOptions{}, storage.Register()), "does not have role ROLE_RELAY")

	otherID, otherBindings := c.RegisterTestNode(t, op, []string{chain.RoleRelay}, "tor")
	noKey := chain.ValidRelay(t, op.Address, otherID, rsaID, otherBindings[0].Priv)
	chain.RequireRefused(t, "a relay node with no relay key", c.Submit(t, op, chain.TxOptions{}, noKey.Register()), `binds no ed25519 key for service "relay"`)
	if out := c.QueryFails(t, op.Node, "relay", "relay", hex.EncodeToString(rsaID.Fingerprint)); !chain.NotFound(out) {
		t.Errorf("a refused fingerprint is registered: %s", out)
	}
}

// TestRelayUpdate_rotatesAFingerprint: MsgUpdateRelay replaces the fingerprint
// of the relay registered on a node (a Tor RSA key rotation) and its exit
// declaration; the old fingerprint stops being a registered relay, the new one
// is, and the operator's relay list follows. It is refused for a node with no
// relay, a fingerprint another relay holds, a cross-signature by another key,
// an RSA identity that cannot prove the new fingerprint, and an operator that
// does not own the node.
func TestRelayUpdate_rotatesAFingerprint(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, op)
	other := c.FundedValidator(t, 1, chain.Orama(1))
	c.EnsureOperator(t, other)
	id, bindings := c.RegisterTestNode(t, op, []string{chain.RoleRelay}, "relay")
	bareID, bareBindings := c.RegisterTestNode(t, op, []string{chain.RoleRelay}, "relay")
	foreignID, foreignBindings := c.RegisterTestNode(t, other, []string{chain.RoleRelay}, "relay")
	priv := bindings[0].Priv
	first, foreign, rotated := chain.NewRelayRSAIdentity(t), chain.NewRelayRSAIdentity(t), chain.NewRelayRSAIdentity(t)
	chain.RequireOK(t, "register the relay", c.Submit(t, op, chain.TxOptions{}, chain.ValidRelay(t, op.Address, id, first, priv).Register()))
	chain.RequireOK(t, "register another operator's relay", c.Submit(t, other, chain.TxOptions{}, chain.ValidRelay(t, other.Address, foreignID, foreign, foreignBindings[0].Priv).Register()))

	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edit := func(f chain.RelayRegistration, with func(*chain.RelayRegistration)) chain.RelayRegistration {
		with(&f)
		return f
	}
	good := chain.ValidRelay(t, op.Address, id, rotated, priv)
	refusals := map[string]struct {
		k    chain.Key
		msg  chain.RelayRegistration
		want string
	}{
		"a node with no relay":            {op, chain.ValidRelay(t, op.Address, bareID, rotated, bareBindings[0].Priv), "no relay is registered on the node"},
		"a fingerprint another relay has": {op, chain.ValidRelay(t, op.Address, id, foreign, priv), "fingerprint already registered"},
		"signed by another key": {op, edit(good, func(f *chain.RelayRegistration) {
			f.Ed25519Sig = chain.RelayCrossCert(stranger, id, rotated.Fingerprint, false)
		}), "does not match the ed25519 cross-signature"},
		"not the node's operator":                  {other, chain.ValidRelay(t, other.Address, id, rotated, priv), "operator does not own node"},
		"19-byte fingerprint":                      {op, edit(good, func(f *chain.RelayRegistration) { f.Fingerprint = rotated.Fingerprint[:19] }), "rsa fingerprint must be 20 bytes"},
		"proofs made for no exit, sent as an exit": {op, edit(good, func(f *chain.RelayRegistration) { f.Exit = true }), "does not match the ed25519 cross-signature"},
		"an rsa key that cannot prove it": {op, edit(good, func(f *chain.RelayRegistration) {
			f.RSASig = first.Proof(t, id, priv.Public().(ed25519.PublicKey), false)
		}), "rsa identity does not prove the fingerprint"},
	}
	for name, tc := range refusals {
		chain.RequireRefused(t, name, c.Submit(t, tc.k, chain.TxOptions{}, tc.msg.Update()), tc.want)
	}
	if out := c.QueryOut(t, op.Node, "relay", "relay", hex.EncodeToString(first.Fingerprint)); out.Exit != 0 {
		t.Fatalf("a refused update removed the relay: %s", out.Stderr)
	}

	rotation := chain.ValidRelayExit(t, op.Address, id, rotated, priv, true)
	chain.RequireOK(t, "rotate the fingerprint", c.Submit(t, op, chain.TxOptions{}, rotation.Update()))
	if out := c.QueryFails(t, op.Node, "relay", "relay", hex.EncodeToString(first.Fingerprint)); !chain.NotFound(out) {
		t.Errorf("the replaced fingerprint is still registered: %s", out)
	}
	var v struct {
		Relay struct {
			NodeID string `json:"node_id"`
			Exit   bool   `json:"exit"`
		} `json:"relay"`
	}
	c.Query(t, op.Node, &v, "relay", "relay", hex.EncodeToString(rotated.Fingerprint))
	if v.Relay.NodeID != id || !v.Relay.Exit {
		t.Errorf("rotated relay %+v, want node %s declaring exit", v.Relay, id)
	}
	var list struct {
		Relays []struct {
			Relay struct {
				NodeID string `json:"node_id"`
			} `json:"relay"`
			LastPayoutEpoch  string `json:"last_payout_epoch"`
			LastPayoutAmount string `json:"last_payout_amount"`
		} `json:"relays"`
	}
	c.Query(t, op.Node, &list, "relay", "operator-relays", op.Address)
	var listed bool
	for _, r := range list.Relays {
		if r.Relay.NodeID == id {
			listed = true
			if r.LastPayoutEpoch != "" && r.LastPayoutEpoch != "0" || r.LastPayoutAmount != "0" {
				t.Errorf("a relay never paid lists a payout: %+v", r)
			}
		}
		if r.Relay.NodeID == foreignID {
			t.Errorf("operator %s lists the relay of node %s, which belongs to %s", op.Address, foreignID, other.Address)
		}
	}
	if !listed {
		t.Errorf("operator %s does not list its relay on node %s: %+v", op.Address, id, list.Relays)
	}
	c.RequireInvariants(t, "a relay rotation")
}

// TestRelayPayouts_anUnsettledEpochPaidNobody: the payouts of an epoch no relay
// was reported on are empty, not an error, and epoch 0 is refused. (The run
// chain has no reporter set, so no epoch pays; the paid case is checked on
// stagenet by relay-reporter.)
func TestRelayPayouts_anUnsettledEpochPaidNobody(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, chain.OperatorNode)
	var v struct {
		Payouts []any `json:"payouts"`
	}
	c.Query(t, n, &v, "relay", "payouts", "1")
	if len(v.Payouts) != 0 {
		t.Errorf("epoch 1 paid %v on a chain with no reporter", v.Payouts)
	}
	c.QueryFails(t, n, "relay", "payouts", "0")
}

// TestRelayReporters_setIsClosedToMessages: the reporter set changes only
// inside a passed structural proposal's execution (x/relay keeper
// reporters.go: the flag is never set by a message), so MsgUpdateReporters
// is refused for any signer; the run's reporter set is the genesis one
// (empty), so MsgReportEpoch from a validator is refused as not a reporter,
// and no epoch has a relay result (the relay end block writes none for an
// epoch nobody reported on, however many epochs the run chain has closed).
func TestRelayReporters_setIsClosedToMessages(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	upd := chain.NewMsg("/orama.relay.v1.MsgUpdateReporters", map[string]any{"signer": k.Address, "reporters": []string{k.Address}})
	chain.RequireRefused(t, "update reporters", c.Submit(t, k, chain.TxOptions{}, upd), "reporter set changes only when")
	var rep struct {
		Reporters []string `json:"reporters"`
	}
	c.Query(t, k.Node, &rep, "relay", "reporters")
	if len(rep.Reporters) != 0 {
		t.Fatalf("reporters %v on the run chain, want the empty genesis set", rep.Reporters)
	}
	report := chain.NewMsg("/orama.relay.v1.MsgReportEpoch", map[string]any{"reporter": k.Address, "epoch": "1",
		"chunk_index": 0, "chunk_count": 1, "entries": []any{}, "inputs_root": base64.StdEncoding.EncodeToString(make([]byte, chain.RelayInputsRootLen))})
	chain.RequireRefused(t, "report by a non-reporter", c.Submit(t, k, chain.TxOptions{}, report), "not a reporter")
	var params struct {
		Params map[string]any `json:"params"`
	}
	c.Query(t, k.Node, &params, "relay", "params")
	if len(params.Params) == 0 {
		t.Errorf("relay params are empty")
	}
	if out := c.QueryFails(t, k.Node, "relay", "epoch", "1"); !chain.NotFound(out) {
		t.Errorf("epoch 1 has a relay result on a chain with no reporter: %s", out)
	}
}

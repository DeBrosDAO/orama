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

// Relay identity sizes (x/relay/types/limits.go) and the cross-certificate
// domain (x/relay/types/crosscert.go).
const (
	rsaFingerprintLen = 20
	crossCertDomain   = "orama/relay/rsa-cross-cert/v1"
	inputsRootLen     = 32
)

func fingerprint(t *testing.T) []byte {
	t.Helper()
	fp := make([]byte, rsaFingerprintLen)
	if _, err := rand.Read(fp); err != nil {
		t.Fatal(err)
	}
	return fp
}

// crossCert is the relay's ed25519 signature over domain||0||node id||0||fp.
func crossCert(priv ed25519.PrivateKey, nodeID string, fp []byte) []byte {
	msg := append(append(append(append([]byte(crossCertDomain), 0), nodeID...), 0), fp...)
	return ed25519.Sign(priv, msg)
}

func registerRelayMsg(operator, nodeID string, fp, sig []byte) chain.Msg {
	return chain.NewMsg("/orama.relay.v1.MsgRegisterRelay", map[string]any{
		"operator": operator, "node_id": nodeID, "rsa_fingerprint": base64.StdEncoding.EncodeToString(fp),
		"exit": false, "ed25519_signature": base64.StdEncoding.EncodeToString(sig),
	})
}

// TestRelayRegister_crossCertifiedRelay: an operator registers its node's Tor
// identity: the RSA fingerprint cross-signed by the node's ed25519 "relay"
// binding key. The registration is stored and readable by fingerprint; the
// same fingerprint again, and the same node with another fingerprint, are
// refused. (x/relay has no message that removes a relay, so the record stays
// for the disposable run chain; the node itself is retired at cleanup.)
func TestRelayRegister_crossCertifiedRelay(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	op := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, op)
	id, bindings := c.RegisterTestNode(t, op, []string{chain.RoleRelay}, "relay")
	fp := fingerprint(t)
	r := c.Submit(t, op, chain.TxOptions{}, registerRelayMsg(op.Address, id, fp, crossCert(bindings[0].Priv, id, fp)))
	chain.RequireOK(t, "register a cross-certified relay", r)
	var v struct {
		Relay struct {
			NodeID   string `json:"node_id"`
			Operator string `json:"operator"`
		} `json:"relay"`
	}
	c.Query(t, op.Node, &v, "relay", "relay", hex.EncodeToString(fp))
	if v.Relay.NodeID != id || v.Relay.Operator != op.Address {
		t.Errorf("relay record %+v", v.Relay)
	}
	again := c.Submit(t, op, chain.TxOptions{}, registerRelayMsg(op.Address, id, fp, crossCert(bindings[0].Priv, id, fp)))
	chain.RequireRefused(t, "same fingerprint", again, "fingerprint already registered")
	fp2 := fingerprint(t)
	second := c.Submit(t, op, chain.TxOptions{}, registerRelayMsg(op.Address, id, fp2, crossCert(bindings[0].Priv, id, fp2)))
	chain.RequireRefused(t, "same node, new fingerprint", second, "is already registered")
	c.RequireInvariants(t, "a relay registration")
}

// TestRelayRegister_refusals: a cross-certificate by another key, over
// another fingerprint or node id, a fingerprint of the wrong size, an
// operator that does not own the node, and a node that does not exist are
// refused before anything is stored.
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
	fp := fingerprint(t)
	other := c.FundedValidator(t, 1, chain.Orama(1))
	cases := map[string]struct {
		k    chain.Key
		msg  chain.Msg
		want string
	}{
		"signed by another key":    {op, registerRelayMsg(op.Address, id, fp, crossCert(stranger, id, fp)), "does not match the ed25519 cross-signature"},
		"over another fingerprint": {op, registerRelayMsg(op.Address, id, fp, crossCert(priv, id, fingerprint(t))), "does not match the ed25519 cross-signature"},
		"over another node id":     {op, registerRelayMsg(op.Address, id, fp, crossCert(priv, id+"x", fp)), "does not match the ed25519 cross-signature"},
		"19-byte fingerprint":      {op, registerRelayMsg(op.Address, id, fp[:19], crossCert(priv, id, fp[:19])), "rsa fingerprint must be 20 bytes"},
		"not the node's operator":  {other, registerRelayMsg(other.Address, id, fp, crossCert(priv, id, fp)), "operator does not own node"},
		"unknown node":             {op, registerRelayMsg(op.Address, "e2e-no-such-node", fp, crossCert(priv, "e2e-no-such-node", fp)), "e2e-no-such-node"},
		"empty node id":            {op, registerRelayMsg(op.Address, "", fp, crossCert(priv, "", fp)), "node id is empty"},
	}
	for name, tc := range cases {
		chain.RequireRefused(t, name, c.Submit(t, tc.k, chain.TxOptions{}, tc.msg), tc.want)
	}
	if out := c.QueryFails(t, op.Node, "relay", "relay", hex.EncodeToString(fp)); !chain.NotFound(out) {
		t.Errorf("a refused fingerprint is registered: %s", out)
	}
}

// TestRelayReporters_setIsClosedToMessages: the reporter set changes only
// inside a passed structural proposal's execution (x/relay keeper
// reporters.go: the flag is never set by a message), so MsgUpdateReporters
// is refused for any signer; the run's reporter set is the genesis one
// (empty), so MsgReportEpoch from a validator is refused as not a reporter,
// and no epoch has a relay result.
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
		"chunk_index": 0, "chunk_count": 1, "entries": []any{}, "inputs_root": base64.StdEncoding.EncodeToString(make([]byte, inputsRootLen))})
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

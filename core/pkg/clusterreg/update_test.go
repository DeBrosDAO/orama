package clusterreg

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

const updateOperator = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"

func consensusBinding() NodeBinding {
	return NodeBinding{Service: ConsensusService, KeyType: "ed25519", Pubkey: bytes.Repeat([]byte{0x07}, 32), Signature: bytes.Repeat([]byte{0xcd}, 64)}
}

func TestEncodeUpdateNode_matchesChainMarshal(t *testing.T) {
	// field 1 operator, field 2 node id, field 4 binding {service, key type 2 (ed25519), pubkey, signature}.
	want := "0a2c" + hex.EncodeToString([]byte(updateOperator)) +
		"1206" + hex.EncodeToString([]byte("node-a")) +
		"2271" + "0a09" + hex.EncodeToString([]byte("consensus")) + "1002" +
		"1a20" + strings.Repeat("07", 32) + "2240" + strings.Repeat("cd", 64)
	got := EncodeUpdateNode(NodeUpdate{Operator: updateOperator, NodeID: "node-a", Bindings: []NodeBinding{consensusBinding()}})
	if hex.EncodeToString(got) != want {
		t.Fatalf("wire %x\nwant %s", got, want)
	}
}

func TestValidateNodeUpdate_acceptsTheConsensusBinding(t *testing.T) {
	if err := ValidateNodeUpdate(NodeUpdate{Operator: updateOperator, NodeID: "node-a", Bindings: []NodeBinding{consensusBinding()}}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateNodeUpdate_refusals(t *testing.T) {
	short := consensusBinding()
	short.Pubkey = short.Pubkey[:31]
	dup := []NodeBinding{consensusBinding(), consensusBinding()}
	for name, u := range map[string]NodeUpdate{
		"no bindings":            {Operator: updateOperator, NodeID: "node-a"},
		"a bad operator":         {Operator: "orama1nope", NodeID: "node-a", Bindings: []NodeBinding{consensusBinding()}},
		"a bad node id":          {Operator: updateOperator, NodeID: "bad id", Bindings: []NodeBinding{consensusBinding()}},
		"a short ed25519 pubkey": {Operator: updateOperator, NodeID: "node-a", Bindings: []NodeBinding{short}},
		"a service bound twice":  {Operator: updateOperator, NodeID: "node-a", Bindings: dup},
	} {
		if err := ValidateNodeUpdate(u); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

package clusterreg

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestEncodeRegisterNode_matchesChainMarshal(t *testing.T) {
	const want = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611a0102222c6f72616d613171717171717171717171717171717171717171717171717171717171717171716e72716c38612a710a0870726f766964657210011a210202020202020202020202020202020202020202020202020202020202020202022240abababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababababab321868747470733a2f2f6e6f64652e6578616d706c652e636f6d3a026575"
	got := EncodeRegisterNode(NodeRegistration{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeID:   "node-a",
		Roles:    []int{RoleStorage},
		HotKey:   "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnrql8a",
		Bindings: []NodeBinding{{
			Service: "provider", KeyType: "secp256k1",
			Pubkey: bytes.Repeat([]byte{0x02}, 33), Signature: bytes.Repeat([]byte{0xab}, 64),
		}},
		Endpoints:  []string{"https://node.example.com"},
		RegionHint: "eu",
	})
	if hex.EncodeToString(got) != want {
		t.Fatalf("wire %x", got)
	}
}

func TestValidateNode_refusesTheOperatorAsHotKey(t *testing.T) {
	const op = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	n := NodeRegistration{
		Operator: op, NodeID: "node-a", Roles: []int{RoleStorage}, HotKey: op,
		Bindings: []NodeBinding{{
			Service: "provider", KeyType: "secp256k1",
			Pubkey: bytes.Repeat([]byte{0x02}, 33), Signature: bytes.Repeat([]byte{0x11}, 64),
		}},
	}
	if err := ValidateNode(n); err == nil {
		t.Fatal("the operator was accepted as the hot key")
	}
	hot, err := bech32Encode(accountHRP, bytes.Repeat([]byte{0x01}, 20))
	if err != nil {
		t.Fatal(err)
	}
	n.HotKey = hot
	n.Endpoints = []string{"https://10.1.1.1"}
	if err := ValidateNode(n); err == nil {
		t.Fatal("a private endpoint was accepted")
	}
	n.Endpoints = nil
	if err := ValidateNode(n); err != nil {
		t.Fatal(err)
	}
}

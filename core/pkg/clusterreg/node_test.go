package clusterreg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	//lint:ignore SA1019 the account address hash is RIPEMD-160 by Cosmos definition; SHA-256 would derive other addresses
	"golang.org/x/crypto/ripemd160"
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
	hotPub := bytes.Repeat([]byte{0x03}, 33)
	sum := sha256.Sum256(hotPub)
	rmd := ripemd160.New()
	rmd.Write(sum[:])
	hot, err := bech32Encode(accountHRP, rmd.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	n.HotKey = hot
	if err := ValidateNode(n); err == nil {
		t.Fatal("a node with no hot-key binding was accepted")
	}
	n.Bindings = append(n.Bindings, NodeBinding{
		Service: HotKeyService, KeyType: "secp256k1", Pubkey: bytes.Repeat([]byte{0x02}, 33), Signature: bytes.Repeat([]byte{0x11}, 64),
	})
	if err := ValidateNode(n); err == nil {
		t.Fatal("a hot-key binding for a different key was accepted")
	}
	n.Bindings[1].Pubkey = hotPub
	n.Endpoints = []string{"https://10.1.1.1"}
	if err := ValidateNode(n); err == nil {
		t.Fatal("a private endpoint was accepted")
	}
	n.Endpoints = nil
	if err := ValidateNode(n); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeRegisterNode_asnIsField8(t *testing.T) {
	// The chain's MsgRegisterNode{Operator: "a", Asn: 15169}.Marshal() ends
	// with field 8 (tag 0x40) and the varint of 15169.
	got := EncodeRegisterNode(NodeRegistration{Operator: "a", ASN: 15169})
	if !strings.HasSuffix(hex.EncodeToString(got), "40c176") {
		t.Fatalf("wire %x", got)
	}
	if strings.HasSuffix(hex.EncodeToString(EncodeRegisterNode(NodeRegistration{Operator: "a"})), "40c176") {
		t.Fatal("an undeclared asn was encoded")
	}
}

func TestValidateASN_refusesWhatTheChainRefuses(t *testing.T) {
	for _, asn := range []uint32{0, 23456, 64496, 64511, 64512, 65535, 65536, 65551, 4200000000, 4294967295} {
		if err := ValidateASN(asn); err == nil {
			t.Errorf("asn %d was accepted", asn)
		}
	}
	for _, asn := range []uint32{1, 15169, 64495, 65552, 396982, 4199999999} {
		if err := ValidateASN(asn); err != nil {
			t.Errorf("asn %d refused: %v", asn, err)
		}
	}
}

func TestValidateNode_checksTheDeclaredASN(t *testing.T) {
	hotPub := bytes.Repeat([]byte{0x03}, 33)
	sum := sha256.Sum256(hotPub)
	rmd := ripemd160.New()
	rmd.Write(sum[:])
	hot, err := bech32Encode(accountHRP, rmd.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	n := NodeRegistration{
		Operator: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s", NodeID: "node-a", Roles: []int{RoleStorage}, HotKey: hot,
		Bindings: []NodeBinding{
			{Service: "provider", KeyType: "secp256k1", Pubkey: bytes.Repeat([]byte{0x02}, 33), Signature: bytes.Repeat([]byte{0x11}, 64)},
			{Service: HotKeyService, KeyType: "secp256k1", Pubkey: hotPub, Signature: bytes.Repeat([]byte{0x11}, 64)},
		},
	}
	n.ASN = 64512
	if err := ValidateNode(n); err == nil {
		t.Fatal("a private-use asn was accepted")
	}
	n.ASN = 15169
	if err := ValidateNode(n); err != nil {
		t.Fatal(err)
	}
	n.ASN = 0
	if err := ValidateNode(n); err != nil {
		t.Fatalf("an undeclared asn was refused: %v", err)
	}
}

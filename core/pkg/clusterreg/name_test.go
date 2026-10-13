package clusterreg

import (
	"encoding/hex"
	"strings"
	"testing"
)

const testNameOperator = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"

// The wire hex is the one chain/x/nodes/types/name_wire_test.go checks against the chain's own
// Marshal, so the two encoders cannot drift apart.
func TestEncodeClaimNodeName_matchesChainMarshal(t *testing.T) {
	const want = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611a09616c7068612d6f6e65"
	got := EncodeClaimNodeName(NodeNameClaim{Operator: testNameOperator, NodeID: "node-a", Name: "alpha-one"})
	if hex.EncodeToString(got) != want {
		t.Fatalf("wire %x", got)
	}
}

func TestEncodeReleaseNodeName_matchesChainMarshal(t *testing.T) {
	const want = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d61"
	got := EncodeReleaseNodeName(NodeNameRelease{Operator: testNameOperator, NodeID: "node-a"})
	if hex.EncodeToString(got) != want {
		t.Fatalf("wire %x", got)
	}
}

func TestValidateNodeNameClaim(t *testing.T) {
	ok := NodeNameClaim{Operator: testNameOperator, NodeID: "node-a", Name: "alpha-one"}
	if err := ValidateNodeNameClaim(ok); err != nil {
		t.Fatalf("a good claim was refused: %v", err)
	}
	for name, edit := range map[string]func(*NodeNameClaim){
		"no operator":        func(c *NodeNameClaim) { c.Operator = "" },
		"a bad operator":     func(c *NodeNameClaim) { c.Operator = "orama1notanaddress" },
		"no node id":         func(c *NodeNameClaim) { c.NodeID = "" },
		"a node id with a /": func(c *NodeNameClaim) { c.NodeID = "a/b" },
		"a node id too long": func(c *NodeNameClaim) { c.NodeID = strings.Repeat("a", 65) },
		"no name":            func(c *NodeNameClaim) { c.Name = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c := ok
			edit(&c)
			if err := ValidateNodeNameClaim(c); err == nil {
				t.Fatal("the claim was accepted")
			}
		})
	}
}

func TestValidateNodeNameRelease(t *testing.T) {
	if err := ValidateNodeNameRelease(NodeNameRelease{Operator: testNameOperator, NodeID: "node-a"}); err != nil {
		t.Fatalf("a good release was refused: %v", err)
	}
	for _, r := range []NodeNameRelease{{NodeID: "node-a"}, {Operator: testNameOperator}, {Operator: testNameOperator, NodeID: "bad id"}} {
		if err := ValidateNodeNameRelease(r); err == nil {
			t.Errorf("release %+v was accepted", r)
		}
	}
}

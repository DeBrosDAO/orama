package onchain

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func typeURLOf(t *testing.T, signed []byte) string { return decodeSignDoc(t, signed).typeURL }

func TestBond_fillsTheOperatorAndSendsTheRoleBond(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	_, err := newClient(t, chain, signer).Bond(context.Background(), clusterreg.Bond{
		NodeID: "node-a", Role: clusterreg.RoleStorage, Amount: "1000000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.BondNodeTypeURL {
		t.Errorf("type = %s", got)
	}
	if !bytes.Contains(chain.sent[0], []byte(testOperator)) || !bytes.Contains(chain.sent[0], []byte("1000000000")) {
		t.Error("the bond does not name the operator and the amount")
	}
}

func TestBond_refusesAnotherOperatorAndBadFacts(t *testing.T) {
	for name, b := range map[string]clusterreg.Bond{
		"another operator": {Operator: "orama1other", NodeID: "node-a", Role: clusterreg.RoleStorage, Amount: "1"},
		"zero amount":      {NodeID: "node-a", Role: clusterreg.RoleStorage, Amount: "0"},
		"unknown role":     {NodeID: "node-a", Role: 99, Amount: "1"},
		"bad node id":      {NodeID: "bad id", Role: clusterreg.RoleStorage, Amount: "1"},
	} {
		t.Run(name, func(t *testing.T) {
			chain := newFakeChain()
			if _, err := newClient(t, chain, newSigner()).Bond(context.Background(), b); err == nil {
				t.Fatal("Bond accepted it")
			}
			if len(chain.simulated) != 0 {
				t.Error("an invalid bond reached the chain")
			}
		})
	}
}

func TestDeclareCapacity_zeroBytesIsADeclaration(t *testing.T) {
	signer := newSigner()
	_, err := newClient(t, newFakeChain(), signer).DeclareCapacity(context.Background(), clusterreg.Capacity{NodeID: "node-a"})
	if err != nil {
		t.Fatalf("zero capacity was refused: %v", err)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.DeclareCapacityTypeURL {
		t.Errorf("type = %s", got)
	}
}

func TestDeclareCapacity_refusesABadNodeID(t *testing.T) {
	if _, err := newClient(t, newFakeChain(), newSigner()).DeclareCapacity(context.Background(), clusterreg.Capacity{NodeID: ""}); err == nil {
		t.Fatal("an empty node id was accepted")
	}
}

func validNode(t *testing.T) clusterreg.NodeRegistration {
	t.Helper()
	hotPub := bytes.Repeat([]byte{2}, 33)
	hotPub[0] = 0x02
	hot, err := clusterreg.AccountAddressOf(hotPub)
	if err != nil {
		t.Fatal(err)
	}
	return clusterreg.NodeRegistration{
		NodeID: "node-a", Roles: []int{clusterreg.RoleStorage}, HotKey: hot,
		Bindings: []clusterreg.NodeBinding{{
			Service: clusterreg.HotKeyService, KeyType: "secp256k1", Pubkey: hotPub, Signature: bytes.Repeat([]byte{5}, 64),
		}},
		Endpoints: []string{"203.0.113.9:4001"},
	}
}

func TestRegisterNode_fillsTheOperatorAndSends(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	if _, err := newClient(t, chain, signer).RegisterNode(context.Background(), validNode(t)); err != nil {
		t.Fatal(err)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.RegisterNodeTypeURL {
		t.Errorf("type = %s", got)
	}
}

func TestRegisterNode_refusesWhatTheChainWouldRefuse(t *testing.T) {
	for name, edit := range map[string]func(*clusterreg.NodeRegistration){
		"no roles":           func(n *clusterreg.NodeRegistration) { n.Roles = nil },
		"no hot key binding": func(n *clusterreg.NodeRegistration) { n.Bindings = nil },
		"another operator":   func(n *clusterreg.NodeRegistration) { n.Operator = "orama1other" },
		"bad asn":            func(n *clusterreg.NodeRegistration) { n.ASN = 23456 },
	} {
		t.Run(name, func(t *testing.T) {
			n := validNode(t)
			edit(&n)
			chain := newFakeChain()
			if _, err := newClient(t, chain, newSigner()).RegisterNode(context.Background(), n); err == nil {
				t.Fatal("RegisterNode accepted it")
			}
			if len(chain.simulated) != 0 {
				t.Error("an invalid registration reached the chain")
			}
		})
	}
}

func TestCreateValidator_usesTheDocumentedDefaults(t *testing.T) {
	chain, signer := newFakeChain(), newSigner()
	_, err := newClient(t, chain, signer).CreateValidator(context.Background(), ValidatorSpec{
		Moniker: "node-a", ConsensusPubKey: bytes.Repeat([]byte{7}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := typeURLOf(t, signer.signed[0]); got != clusterreg.CreateValidatorTypeURL {
		t.Errorf("type = %s", got)
	}
	for _, want := range []string{DefaultSelfBondNorama, "100000000000000000", "200000000000000000", "oramavaloper19rl4cm2hmr8afy4kldpxz3fka4jguq0al2xuls"} {
		if !bytes.Contains(chain.sent[0], []byte(want)) {
			t.Errorf("the transaction does not carry %s", want)
		}
	}
}

func TestCreateValidator_anExplicitBondReplacesTheDefault(t *testing.T) {
	chain := newFakeChain()
	_, err := newClient(t, chain, newSigner()).CreateValidator(context.Background(), ValidatorSpec{
		Moniker: "node-a", ConsensusPubKey: bytes.Repeat([]byte{7}, 32), SelfBond: "5000000000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The coin is the denom and then the amount as a length-prefixed string.
	coin := func(amount string) []byte { return append([]byte("norama\x12\x0d"), amount...) }
	if !bytes.Contains(chain.sent[0], coin("5000000000000")) || bytes.Contains(chain.sent[0], coin(DefaultSelfBondNorama)) {
		t.Error("the explicit self bond was not used")
	}
}

func TestCreateValidator_refusesBadFacts(t *testing.T) {
	for name, spec := range map[string]ValidatorSpec{
		"no moniker":          {ConsensusPubKey: bytes.Repeat([]byte{7}, 32)},
		"no consensus key":    {Moniker: "node-a"},
		"short consensus key": {Moniker: "node-a", ConsensusPubKey: []byte{1}},
		"bond not an integer": {Moniker: "node-a", ConsensusPubKey: bytes.Repeat([]byte{7}, 32), SelfBond: "1.5"},
	} {
		t.Run(name, func(t *testing.T) {
			chain := newFakeChain()
			_, err := newClient(t, chain, newSigner()).CreateValidator(context.Background(), spec)
			if err == nil || !strings.Contains(err.Error(), "create the validator") {
				t.Fatalf("error = %v", err)
			}
			if len(chain.simulated) != 0 {
				t.Error("an invalid validator reached the chain")
			}
		})
	}
}

func TestOperator_isTheSigningAccount(t *testing.T) {
	got, err := newClient(t, newFakeChain(), newSigner()).Operator(context.Background())
	if err != nil || got != testOperator {
		t.Fatalf("Operator = %q, %v", got, err)
	}
}

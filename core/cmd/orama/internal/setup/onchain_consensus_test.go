package setup

import (
	"crypto/ed25519"
	"encoding/hex"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
)

func testNetwork() *netregistry.Network {
	return &netregistry.Network{Manifest: &netregistry.Manifest{ChainID: testChainID}}
}

func hotOnly() []clusterreg.NodeBinding {
	return []clusterreg.NodeBinding{{Service: clusterreg.HotKeyService, KeyType: "secp256k1"}}
}

func TestRun_joinBindsTheConsensusKeyOfTheValidatorNodeOnly(t *testing.T) {
	h := newHarness()
	mustRun(t, h, h.opts(ip1, ip2))

	if h.w.index("tx register-node alice roles=[2] asn=24940 bindings=hot-key,consensus") < 0 {
		t.Errorf("the validator's node registers with its consensus binding:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	if h.w.index("tx register-node alice-2 roles=[2] asn=24940 bindings=hot-key\n") >= 0 || h.w.index("identity "+ip2+" consensus=false") < 0 {
		t.Errorf("the second node is not a validator, so its consensus key is not bound:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_noValidatorBindsNoConsensusKey(t *testing.T) {
	h := newHarness()
	opts := h.opts(ip1)
	opts.NoValidator = true
	mustRun(t, h, opts)

	if h.w.index("identity "+ip1+" consensus=false") < 0 || h.w.index("bindings=hot-key,consensus") >= 0 {
		t.Errorf("a node that carries no validator binds no consensus key:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_aNodeRegisteredWithoutTheConsensusBindingGetsItOnce(t *testing.T) {
	h := newHarness()
	h.w.operatorRegistered = true
	h.w.nodes["alice"] = &RegisteredNode{Roles: []int{clusterreg.RoleStorage}, Bonds: map[int]*big.Int{}, Bindings: hotOnly()}
	mustRun(t, h, h.opts(ip1))

	if h.w.count("tx register-node") != 0 || h.w.index("tx update-bindings alice hot-key,consensus") < 0 {
		t.Fatalf("the registered node is updated, not registered again:\n%s", strings.Join(h.w.entries(), "\n"))
	}
	got := serviceList(h.w.nodes["alice"].Bindings)
	if got != "hot-key,consensus" {
		t.Errorf("the update replaces the whole set, so it keeps the hot key's binding: %s", got)
	}

	before := h.w.count("tx update-bindings")
	mustRun(t, h, h.opts(ip1))
	if h.w.count("tx update-bindings") != before {
		t.Errorf("a re-run sent the binding again:\n%s", strings.Join(h.w.entries(), "\n"))
	}
}

func TestRun_aNodeKeepsItsOtherBindingsWhenTheConsensusKeyIsBound(t *testing.T) {
	h := newHarness()
	h.w.operatorRegistered = true
	tor := clusterreg.NodeBinding{Service: "tor", KeyType: "ed25519"}
	h.w.nodes["alice"] = &RegisteredNode{Roles: []int{clusterreg.RoleStorage}, Bonds: map[int]*big.Int{}, Bindings: append(hotOnly(), tor)}
	mustRun(t, h, h.opts(ip1))

	if got := serviceList(h.w.nodes["alice"].Bindings); got != "hot-key,tor,consensus" {
		t.Errorf("bindings after the update: %s", got)
	}
}

func TestRun_aConsensusBindingForAnotherOperatorIsRefusedBeforeAnyTransaction(t *testing.T) {
	h := newHarness()
	h.enroll.tamperIdentity = func(id *NodeIdentity) {
		id.ConsensusBinding.Signature = make([]byte, len(id.ConsensusBinding.Signature))
	}
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), "consensus key binding") || !strings.Contains(err.Error(), testOperator) {
		t.Fatalf("got %v, want the unverifiable binding named with its operator", err)
	}
	if h.w.count("tx register-node") != 0 {
		t.Error("a node was registered with a binding the chain would refuse")
	}
}

func TestRun_aNodeThatReturnsNoConsensusBindingIsAnError(t *testing.T) {
	h := newHarness()
	h.enroll.tamperIdentity = func(id *NodeIdentity) { id.ConsensusBinding = nil }
	_, err := run(t, h, h.opts(ip1))
	if err == nil || !strings.Contains(err.Error(), "no binding for its consensus key") {
		t.Fatalf("got %v", err)
	}
}

func TestRunCreate_everySeatBindsItsConsensusKey(t *testing.T) {
	h := newCreateHarness(t)
	h.mustCreate(t, h.createOpts(fiveIPs...))

	if got := h.w.count("identity ") - h.w.count("consensus=false"); got != 5 {
		t.Errorf("%d identities asked for the consensus binding, want 5", got)
	}
	for _, name := range []string{"founder", "founder-2", "founder-3", "founder-4", "founder-5"} {
		if !slices.ContainsFunc(h.w.entries(), func(e string) bool {
			return strings.HasPrefix(e, "tx register-node "+name+" ") && strings.HasSuffix(e, "bindings=hot-key,consensus")
		}) {
			t.Errorf("seat %s did not register with its consensus binding:\n%s", name, strings.Join(h.w.entries(), "\n"))
		}
	}
}

func TestConsensusBindingOf_verifiesTheChainsExactStatement(t *testing.T) {
	r := &runner{oper: testOperator, net: testNetwork()}
	n := &nodeRun{plan: NodePlan{Name: "alice", BindConsensus: true}}
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 9
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	sign := func(operator, service string) NodeIdentity {
		statement := "orama-global-bind-v1|" + testChainID + "|" + operator + "|" + service + "|" + hex.EncodeToString(pub)
		return NodeIdentity{ConsensusBinding: &clusterreg.NodeBinding{Service: clusterreg.ConsensusService, KeyType: "ed25519", Pubkey: pub, Signature: ed25519.Sign(priv, []byte(statement))}}
	}

	if _, err := r.consensusBindingOf(n, sign(testOperator, "consensus")); err != nil {
		t.Errorf("the chain's statement was refused: %v", err)
	}
	for name, id := range map[string]NodeIdentity{
		"another operator": sign("orama1other", "consensus"),
		"another service":  sign(testOperator, "tor"),
	} {
		if _, err := r.consensusBindingOf(n, id); err == nil {
			t.Errorf("%s: a statement the chain would refuse was accepted", name)
		}
	}
}

func TestConsensusBindingOf_aNodeThatDoesNotBindReturnsNil(t *testing.T) {
	r := &runner{oper: testOperator, net: testNetwork()}
	b, err := r.consensusBindingOf(&nodeRun{plan: NodePlan{Name: "alice"}}, NodeIdentity{})
	if err != nil || b != nil {
		t.Errorf("got %v, %v: only a validator's node binds its consensus key", b, err)
	}
}

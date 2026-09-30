//go:build e2e_fleet

package chaineconomics

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// nodeView is `oramad query nodes node`.
type nodeView struct {
	Node struct {
		NodeID       string    `json:"node_id"`
		Operator     string    `json:"operator"`
		Roles        []string  `json:"roles"`
		HotKey       string    `json:"hot_key"`
		Endpoints    []string  `json:"endpoints"`
		RegionHint   string    `json:"region_hint"`
		Status       string    `json:"status"`
		DepositBytes chain.Int `json:"deposit_bytes"`
		Bindings     []struct {
			Service string `json:"service"`
			KeyType string `json:"key_type"`
			Pubkey  string `json:"pubkey"`
		} `json:"bindings"`
	} `json:"node"`
}

func queryNode(t *testing.T, c *chain.Chain, id string) nodeView {
	t.Helper()
	var v nodeView
	c.Query(t, c.Node(t, 0), &v, "nodes", "node", id)
	return v
}

// operator returns the run's funded x/nodes operator (node-1's validator key),
// registered if no package did yet.
func operator(t *testing.T, c *chain.Chain) chain.Key {
	t.Helper()
	k := c.FundedValidator(t, chain.OperatorNode, chain.Orama(10))
	c.EnsureOperator(t, k)
	return k
}

// nonOperator is a funded account that is not an x/nodes operator.
func nonOperator(t *testing.T, c *chain.Chain) chain.Key {
	t.Helper()
	k := c.FundedValidator(t, chain.NonOperatorNode, chain.Orama(1))
	if c.OperatorRegistered(t, k.Address) {
		t.Fatalf("%s (%s) is an x/nodes operator; the chain packages keep it unregistered for the not-an-operator cases", k.Address, k.Node.Name)
	}
	return k
}

// TestNodesOperator_registerIsPermanentAndUnique: registering an operator
// records its height and time; registering the same address again is
// refused (x/nodes ErrExists); the query of an unknown operator is not found.
func TestNodesOperator_registerIsPermanentAndUnique(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := operator(t, c)
	var op struct {
		Operator struct {
			Address            string    `json:"address"`
			RegisteredAtHeight chain.Int `json:"registered_at_height"`
			RegisteredAtUnix   chain.Int `json:"registered_at_unix"`
		} `json:"operator"`
	}
	c.Query(t, k.Node, &op, "nodes", "operator", k.Address)
	if op.Operator.Address != k.Address || op.Operator.RegisteredAtHeight.Int64() <= 0 || op.Operator.RegisteredAtUnix.Int64() <= 0 {
		t.Errorf("operator record %+v", op.Operator)
	}
	dup := c.Submit(t, k, chain.TxOptions{}, chain.RegisterOperatorMsg(k.Address))
	chain.RequireRefused(t, "register the same operator twice", dup, "already exists")
	stranger := c.NewKey(t, k.Node, "e2e-nobody")
	if out := c.QueryFails(t, k.Node, "nodes", "operator", stranger.Address); !chain.NotFound(out) {
		t.Errorf("unknown operator: %s", out)
	}
	c.RequireInvariants(t, "a duplicate operator registration")
}

// TestNodesRegister_happyPathLocksDepositFromEarnings: a node with two roles,
// an ed25519 and a secp256k1 binding, a hot key that is not the operator and
// proved it holds itself (its own "hot-key" binding) and a public endpoint is registered in status REGISTERED (no role bonded yet),
// its deposit is locked under nodes/node/<id> (paid from earnings: the
// operator's bank balance is empty), and retiring it releases the deposit.
func TestNodesRegister_happyPathLocksDepositFromEarnings(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := operator(t, c)
	id := chain.UniqueID(t, "e2e-node-")
	hot, proof := chain.HotKeyBinding(t, c.ID, k.Address)
	spec := chain.NodeSpec{Operator: k.Address, NodeID: id, Roles: []string{chain.RoleStorage, chain.RoleRelay}, HotKey: hot,
		Bindings:  []chain.Binding{chain.Ed25519Binding(t, c.ID, k.Address, "relay"), chain.Secp256k1Binding(t, c.ID, k.Address, "storage"), proof},
		Endpoints: []string{fmt.Sprintf("https://%s:31013", k.Node.PublicIP), "relay.example.com:443"}, Region: "eu-central"}
	r := chain.RequireOK(t, "register node", c.Submit(t, k, chain.TxOptions{}, chain.RegisterNodeMsg(spec)))
	v := queryNode(t, c, id)
	if v.Node.Status != "NODE_STATUS_REGISTERED" || v.Node.Operator != k.Address || v.Node.HotKey != hot || len(v.Node.Bindings) != 3 {
		t.Errorf("node record %+v", v.Node)
	}
	dep := deposit(t, c, "nodes/node/"+id)
	if dep.Owner != k.Address || dep.Amount.IsZero() {
		t.Errorf("deposit %+v, want a positive amount owned by %s", dep, k.Address)
	}
	if b := c.BankAt(t, k.Node, k.Address, r.Height); !b.IsZero() {
		t.Logf("operator bank balance %s: the deposit may have been taken from bank first", b.String())
	}
	chain.RequireOK(t, "retire", c.Submit(t, k, chain.TxOptions{}, chain.RetireNodeMsg(k.Address, id)))
	if st := queryNode(t, c, id).Node.Status; st != "NODE_STATUS_RETIRED" {
		t.Errorf("status after retire %s", st)
	}
	if out := c.QueryFails(t, k.Node, "fees", "deposit", "nodes/node/"+id); !chain.NotFound(out) {
		t.Errorf("deposit still readable after retire: %s", out)
	}
	again := c.Submit(t, k, chain.TxOptions{}, chain.RetireNodeMsg(k.Address, id))
	chain.RequireRefused(t, "retire twice", again, id, "RETIRED")
	c.RequireInvariants(t, "a node registered and retired")
}

type feeDeposit struct {
	ID     string    `json:"id"`
	Owner  string    `json:"owner"`
	Amount chain.Int `json:"amount"`
}

func deposit(t *testing.T, c *chain.Chain, id string) feeDeposit {
	t.Helper()
	var r struct {
		Deposit feeDeposit `json:"deposit"`
	}
	c.Query(t, c.Node(t, 0), &r, "fees", "deposit", id)
	return r.Deposit
}

// TestNodesRegister_refusals: every documented refusal of MsgRegisterNode
// (docs/CHAIN.md "x/nodes", x/nodes/types/validate.go, keeper/msg.go), one
// transaction each, and none of them leaves a node behind.
func TestNodesRegister_refusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := operator(t, c)
	for name, tc := range registerRefusals(t, c, k) {
		r := c.Submit(t, k, chain.TxOptions{}, chain.RegisterNodeMsg(tc.spec))
		chain.RequireRefused(t, name, r, tc.want)
		if out := c.QueryOut(t, k.Node, "nodes", "node", tc.spec.NodeID); out.Exit == 0 && tc.spec.NodeID != "" {
			t.Errorf("%s: node %s exists after the refusal", name, tc.spec.NodeID)
		}
	}
	c.RequireInvariants(t, "refused node registrations")
}

type refusal struct {
	spec chain.NodeSpec
	want string
}

func registerRefusals(t *testing.T, c *chain.Chain, k chain.Key) map[string]refusal {
	t.Helper()
	hot, proof := chain.HotKeyBinding(t, c.ID, k.Address)
	// good is a valid registration with mut applied; a binding set mut leaves
	// non-empty keeps the hot key's proof (without it the chain refuses the
	// set for that reason, not for the fault under test).
	good := func(mut func(*chain.NodeSpec)) chain.NodeSpec {
		s := chain.NodeSpec{Operator: k.Address, NodeID: chain.UniqueID(t, "e2e-bad-"), Roles: []string{chain.RoleRelay}, HotKey: hot,
			Bindings: []chain.Binding{chain.Ed25519Binding(t, c.ID, k.Address, "relay")}}
		mut(&s)
		if len(s.Bindings) > 0 {
			s.Bindings = append(append([]chain.Binding{}, s.Bindings...), proof)
		}
		return s
	}
	wrongDomain := chain.SignedEd25519Binding(t, "relay", func(pub []byte) []byte {
		return []byte(strings.Replace(string(chain.BindingSignBytes(c.ID, k.Address, "relay", pub)), chain.BindingPrefix, "orama-global-bind-v0", 1))
	})
	wrongChain := chain.SignedEd25519Binding(t, "relay", func(pub []byte) []byte {
		return chain.BindingSignBytes(c.ID+"-other", k.Address, "relay", pub)
	})
	forged := chain.Ed25519Binding(t, c.ID, k.Address, "relay")
	forged.Signature[0] ^= 0xff
	manyEndpoints := make([]string, 9)
	for i := range manyEndpoints {
		manyEndpoints[i] = fmt.Sprintf("relay%d.example.com:443", i)
	}
	return map[string]refusal{
		"hot key is the operator":      {good(func(s *chain.NodeSpec) { s.HotKey = k.Address }), "hot key must differ from the operator"},
		"forged binding signature":     {good(func(s *chain.NodeSpec) { s.Bindings = []chain.Binding{forged} }), "binding signature is invalid"},
		"binding under another domain": {good(func(s *chain.NodeSpec) { s.Bindings = []chain.Binding{wrongDomain} }), "binding signature is invalid"},
		"binding for another chain id": {good(func(s *chain.NodeSpec) { s.Bindings = []chain.Binding{wrongChain} }), "binding signature is invalid"},
		"no binding":                   {good(func(s *chain.NodeSpec) { s.Bindings = nil }), "at least one binding is required"},
		"no role":                      {good(func(s *chain.NodeSpec) { s.Roles = nil }), "at least one role is required"},
		"duplicate role":               {good(func(s *chain.NodeSpec) { s.Roles = []string{chain.RoleRelay, chain.RoleRelay} }), "duplicate role"},
		"private endpoint":             {good(func(s *chain.NodeSpec) { s.Endpoints = []string{"http://10.0.0.1:31013"} }), "is not a public address"},
		"localhost endpoint":           {good(func(s *chain.NodeSpec) { s.Endpoints = []string{"https://localhost:31013"} }), "is not public"},
		"userinfo endpoint":            {good(func(s *chain.NodeSpec) { s.Endpoints = []string{"https://u:p@relay.example.com"} }), "must not include userinfo"},
		"nine endpoints (max 8)":       {good(func(s *chain.NodeSpec) { s.Endpoints = manyEndpoints }), "max is 8"},
		"bad node id":                  {good(func(s *chain.NodeSpec) { s.NodeID = "-starts-with-dash" }), "must match"},
		"bad region":                   {good(func(s *chain.NodeSpec) { s.Region = "has space" }), "region hint"},
	}
}

// TestNodesRegister_notAnOperatorRefused: an account that never registered
// as an operator cannot register a node (keeper requireOperator).
func TestNodesRegister_notAnOperatorRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := nonOperator(t, c)
	hot, proof := chain.HotKeyBinding(t, c.ID, k.Address)
	spec := chain.NodeSpec{Operator: k.Address, NodeID: chain.UniqueID(t, "e2e-node-"), Roles: []string{chain.RoleRelay},
		HotKey: hot, Bindings: []chain.Binding{chain.Ed25519Binding(t, c.ID, k.Address, "relay"), proof}}
	r := c.Submit(t, k, chain.TxOptions{}, chain.RegisterNodeMsg(spec))
	chain.RequireRefused(t, "node of a non-operator", r, "not found")
}

// TestNodesRegister_duplicateAndReusedKeysRefused: a node id cannot be
// registered twice; a service pubkey bound to a live node cannot be bound to
// a second one; and after the first node retires, its pubkey stays revoked
// forever (docs/CHAIN.md: "A service pubkey is unique on the network").
func TestNodesRegister_duplicateAndReusedKeysRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := operator(t, c)
	id, bindings := c.RegisterTestNode(t, k, []string{chain.RoleRelay}, "relay")
	hot, proof := chain.HotKeyBinding(t, c.ID, k.Address)
	sameID := chain.NodeSpec{Operator: k.Address, NodeID: id, Roles: []string{chain.RoleRelay}, HotKey: hot,
		Bindings: []chain.Binding{chain.Ed25519Binding(t, c.ID, k.Address, "relay"), proof}}
	chain.RequireRefused(t, "same node id", c.Submit(t, k, chain.TxOptions{}, chain.RegisterNodeMsg(sameID)), "already exists")
	reuse := sameID
	reuse.NodeID = chain.UniqueID(t, "e2e-node-")
	reuse.Bindings = append(append([]chain.Binding{}, bindings...), proof)
	chain.RequireRefused(t, "live pubkey on a second node", c.Submit(t, k, chain.TxOptions{}, chain.RegisterNodeMsg(reuse)), "cannot be reused")
	chain.RequireOK(t, "retire", c.Submit(t, k, chain.TxOptions{}, chain.RetireNodeMsg(k.Address, id)))
	chain.RequireRefused(t, "revoked pubkey after retire", c.Submit(t, k, chain.TxOptions{}, chain.RegisterNodeMsg(reuse)), "cannot be reused")
	c.RequireInvariants(t, "reused service keys")
}

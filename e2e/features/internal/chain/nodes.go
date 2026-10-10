//go:build e2e_fleet

package chain

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// Operator roles in x/nodes. Economics registers OperatorNode's validator as
// the run's x/nodes operator; NonOperatorNode's validator is never registered
// by a chain package, so it is the funded account every "signer is not an
// operator" case uses.
const (
	OperatorNode    = 0
	NonOperatorNode = 2
)

// BindingPrefix is x/nodes' binding domain separator (x/nodes/types/binding.go).
const BindingPrefix = "orama-global-bind-v1"

// Roles and key types as proto-JSON enum names (orama/nodes/v1/nodes.proto).
const (
	RoleValidator = "ROLE_VALIDATOR"
	RoleStorage   = "ROLE_STORAGE"
	RoleRelay     = "ROLE_RELAY"
	RoleExit      = "ROLE_EXIT"
	RoleDirauth   = "ROLE_DIRAUTH"
	RoleArchiver  = "ROLE_ARCHIVER"
	KeyEd25519    = "KEY_TYPE_ED25519"
	KeySecp256k1  = "KEY_TYPE_SECP256K1"
)

// BindingSignBytes is orama-global-bind-v1|chain-id|operator|service|hex(pubkey).
func BindingSignBytes(chainID, operator, service string, pubkey []byte) []byte {
	return []byte(strings.Join([]string{BindingPrefix, chainID, operator, service, hex.EncodeToString(pubkey)}, "|"))
}

// Binding is one service-key binding. Priv is the ed25519 service key that
// made it (nil for secp256k1): a relay's cross-certificate is signed with it.
type Binding struct {
	Service   string
	KeyType   string
	Pubkey    []byte
	Signature []byte
	Priv      ed25519.PrivateKey
}

// JSON is the binding as a MsgRegisterNode/MsgUpdateNode field.
func (b Binding) JSON() map[string]any {
	return map[string]any{
		"service": b.Service, "key_type": b.KeyType,
		"pubkey":    base64.StdEncoding.EncodeToString(b.Pubkey),
		"signature": base64.StdEncoding.EncodeToString(b.Signature),
	}
}

// Ed25519Binding makes a fresh ed25519 service key and signs the binding
// statement for chainID and operator with it.
func Ed25519Binding(t testing.TB, chainID, operator, service string) Binding {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate a service key: %v", err)
	}
	return Binding{Service: service, KeyType: KeyEd25519, Pubkey: pub, Priv: priv,
		Signature: ed25519.Sign(priv, BindingSignBytes(chainID, operator, service, pub))}
}

// SignedEd25519Binding signs an arbitrary statement with a fresh key: the
// raw material of wrong-domain and wrong-chain bindings.
func SignedEd25519Binding(t testing.TB, service string, statement func(pub []byte) []byte) Binding {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate a service key: %v", err)
	}
	return Binding{Service: service, KeyType: KeyEd25519, Pubkey: pub, Signature: ed25519.Sign(priv, statement(pub))}
}

// Secp256k1Binding makes a fresh secp256k1 service key and signs the binding
// over the Cosmos SHA-256 digest (x/nodes VerifyBinding), R||S low-S.
func Secp256k1Binding(t testing.TB, chainID, operator, service string) Binding {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate a secp256k1 key: %v", err)
	}
	pub := crypto.CompressPubkey(&key.PublicKey)
	digest := sha256.Sum256(BindingSignBytes(chainID, operator, service, pub))
	sig, err := crypto.Sign(digest[:], key)
	if err != nil {
		t.Fatalf("failed to sign the binding: %v", err)
	}
	return Binding{Service: service, KeyType: KeySecp256k1, Pubkey: pub, Signature: sig[:64]}
}

// RegisterOperatorMsg is MsgRegisterOperator for addr.
func RegisterOperatorMsg(addr string) Msg {
	return NewMsg("/orama.nodes.v1.MsgRegisterOperator", map[string]any{"operator": addr})
}

// NodeSpec is a MsgRegisterNode body.
type NodeSpec struct {
	Operator  string
	NodeID    string
	Roles     []string
	HotKey    string
	Bindings  []Binding
	Endpoints []string
	Region    string
}

// RegisterNodeMsg is MsgRegisterNode for s.
func RegisterNodeMsg(s NodeSpec) Msg {
	bindings := make([]any, 0, len(s.Bindings))
	for _, b := range s.Bindings {
		bindings = append(bindings, b.JSON())
	}
	return NewMsg("/orama.nodes.v1.MsgRegisterNode", map[string]any{
		"operator": s.Operator, "node_id": s.NodeID, "roles": s.Roles, "hot_key": s.HotKey,
		"bindings": bindings, "endpoints": s.Endpoints, "region_hint": s.Region,
	})
}

// RetireNodeMsg is MsgRetireNode.
func RetireNodeMsg(operator, nodeID string) Msg {
	return NewMsg("/orama.nodes.v1.MsgRetireNode", map[string]any{"operator": operator, "node_id": nodeID})
}

// OperatorRegistered reports whether addr is an x/nodes operator.
func (c *Chain) OperatorRegistered(t testing.TB, addr string) bool {
	t.Helper()
	out := c.QueryOut(t, c.Node(t, 0), "nodes", "operator", addr)
	if out.Exit == 0 {
		return true
	}
	if strings.Contains(out.Stderr+out.Stdout, "not found") {
		return false
	}
	t.Fatalf("nodes operator %s exited %d: %s", addr, out.Exit, out.Stderr)
	return false
}

// EnsureOperator registers k as an x/nodes operator unless it already is
// (several chain packages share the run's operator; registration is
// permanent: x/nodes has no message that removes an operator, docs/CHAIN.md
// "x/nodes"). Losing a race to another package is fine: the record exists.
func (c *Chain) EnsureOperator(t testing.TB, k Key) {
	t.Helper()
	if c.OperatorRegistered(t, k.Address) {
		return
	}
	r := c.Submit(t, k, TxOptions{}, RegisterOperatorMsg(k.Address))
	if !r.OK() && !strings.Contains(r.Log, "already exists") {
		t.Fatalf("failed to register %s as an operator: %s", k.Address, r)
	}
	if !c.OperatorRegistered(t, k.Address) {
		t.Fatalf("%s is not an operator after registering", k.Address)
	}
}

// TestNode is a node a test registered: its id, its hot key and the service
// bindings it was registered with (the hot-key binding is not among them).
type TestNode struct {
	ID       string
	HotKey   string
	Bindings []Binding
}

// RegisterTestNode registers a node for operator k with one fresh ed25519
// binding per service and a fresh hot key that proved it holds itself
// (HotKeyBinding), and retires it at cleanup (which releases its deposit:
// x/nodes RetireNode). It returns the node id and the service bindings.
func (c *Chain) RegisterTestNode(t testing.TB, k Key, roles []string, services ...string) (string, []Binding) {
	t.Helper()
	n := c.RegisterProvenNode(t, k, roles, services...)
	return n.ID, n.Bindings
}

// RegisterProvenNode is RegisterTestNode returning the node with its hot key.
func (c *Chain) RegisterProvenNode(t testing.TB, k Key, roles []string, services ...string) TestNode {
	t.Helper()
	hot, proof := HotKeyBinding(t, c.ID, k.Address)
	n := TestNode{ID: UniqueID(t, "e2e-node-"), HotKey: hot, Bindings: c.serviceBindings(t, k, services)}
	c.registerNode(t, k, NodeSpec{Operator: k.Address, NodeID: n.ID, Roles: roles, HotKey: hot,
		Bindings: append(append([]Binding{}, n.Bindings...), proof)})
	return n
}

// RegisterTestNodeAt is RegisterTestNode with the given endpoints instead of a hostname of its own:
// for a test that needs the node to have a literal IP (x/nodes refuses a public IP another live
// node holds, so the address must be one no node of the run uses).
func (c *Chain) RegisterTestNodeAt(t testing.TB, k Key, roles []string, endpoints []string, services ...string) string {
	t.Helper()
	hot, proof := HotKeyBinding(t, c.ID, k.Address)
	id := UniqueID(t, "e2e-node-")
	bindings := c.serviceBindings(t, k, services)
	c.registerNode(t, k, NodeSpec{Operator: k.Address, NodeID: id, Roles: roles, HotKey: hot,
		Bindings: append(append([]Binding{}, bindings...), proof), Endpoints: endpoints})
	return id
}

func (c *Chain) serviceBindings(t testing.TB, k Key, services []string) []Binding {
	var bindings []Binding
	for _, s := range services {
		bindings = append(bindings, Ed25519Binding(t, c.ID, k.Address, s))
	}
	return bindings
}

// registerNode submits spec with a hostname endpoint of its own (x/nodes
// refuses a public IP another live node holds, and every test node of a
// run shares its operator's server) and retires the node at cleanup.
func (c *Chain) registerNode(t testing.TB, k Key, spec NodeSpec) {
	t.Helper()
	if len(spec.Endpoints) == 0 {
		spec.Endpoints = []string{fmt.Sprintf("https://%s.example.com:31013", spec.NodeID)}
	}
	r := c.Submit(t, k, TxOptions{}, RegisterNodeMsg(spec))
	if !r.OK() {
		t.Fatalf("failed to register node %s: %s", spec.NodeID, r)
	}
	t.Cleanup(func() { c.retireAtCleanup(t, k, spec.NodeID) })
}

// retireAtCleanup retires a node the test registered, unless the test did.
func (c *Chain) retireAtCleanup(t testing.TB, k Key, id string) {
	var n struct {
		Node struct {
			Status string `json:"status"`
		} `json:"node"`
	}
	out, err := c.cleanupRun(t, k.Node, c.OramadCmd("query", "nodes", "node", id, "--node", c.RPC(), "--output", "json"))
	if err != nil || out.Exit != 0 {
		t.Errorf("cleanup: failed to read node %s: %v %s", id, err, out.Stderr)
		return
	}
	if err := json.Unmarshal([]byte(out.Stdout), &n); err != nil {
		t.Errorf("cleanup: node %s: %v", id, err)
		return
	}
	if n.Node.Status == "NODE_STATUS_RETIRED" {
		return
	}
	c.CleanupSubmit(t, k, "retire node "+id, RetireNodeMsg(k.Address, id))
}

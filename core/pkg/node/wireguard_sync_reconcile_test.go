package node

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/environments/production"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// fakeProvisioner records reconciler decisions instead of touching a real
// WireGuard interface.
type fakeProvisioner struct {
	added   []production.WireGuardPeer
	removed []string
	addErr  error
	rmErr   error
}

func (f *fakeProvisioner) AddPeer(peer production.WireGuardPeer) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.added = append(f.added, peer)
	return nil
}

func (f *fakeProvisioner) RemovePeer(publicKey string) error {
	if f.rmErr != nil {
		return f.rmErr
	}
	f.removed = append(f.removed, publicKey)
	return nil
}

func testNode(t *testing.T) *Node {
	t.Helper()
	lg, err := logging.NewColoredLogger(logging.ComponentNode, false)
	if err != nil {
		t.Fatalf("NewColoredLogger: %v", err)
	}
	return &Node{logger: lg}
}

func peerSet(keys ...string) map[string]struct{} {
	s := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		s[k] = struct{}{}
	}
	return s
}

func desired(auth bool, source string, keys ...string) desiredWGPeers {
	m := make(map[string]production.WireGuardPeer, len(keys))
	for _, k := range keys {
		m[k] = production.WireGuardPeer{PublicKey: k, AllowedIP: "10.0.0.9/32", Endpoint: "203.0.113.9:51820"}
	}
	return desiredWGPeers{peers: m, authoritative: auth, source: source}
}

func TestReconcileWireGuardPeers_authoritative_addsAndRemoves(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	// live: A, B — cluster says: B, C  => add C, remove A
	n.reconcileWireGuardPeersWith(f, peerSet("A", "B"), desired(true, "leader", "B", "C"))

	if len(f.added) != 1 || f.added[0].PublicKey != "C" {
		t.Errorf("added = %+v; want exactly peer C", f.added)
	}
	if len(f.removed) != 1 || f.removed[0] != "A" {
		t.Errorf("removed = %v; want exactly [A]", f.removed)
	}
}

// The bootstrap-deadlock guarantee: a non-authoritative (local-replica) read
// may ADD peers so a partitioned node can rebuild its mesh, but must never
// remove — the local snapshot can be arbitrarily stale.
func TestReconcileWireGuardPeers_localFallback_addsButNeverRemoves(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	n.reconcileWireGuardPeersWith(f, peerSet("A", "B"), desired(false, "local-replica", "B", "C"))

	if len(f.added) != 1 || f.added[0].PublicKey != "C" {
		t.Errorf("added = %+v; want exactly peer C (fallback must still repair the mesh)", f.added)
	}
	if len(f.removed) != 0 {
		t.Errorf("removed = %v; a non-authoritative read must never remove peers", f.removed)
	}
}

// The regression that severs a cluster: an empty desired set is "I learned
// nothing", not "the cluster has no members". Removing on it takes the node
// off the mesh, and being off the mesh is what makes the failure permanent.
func TestReconcileWireGuardPeers_emptyAuthoritativeSet_doesNotRemove(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	n.reconcileWireGuardPeersWith(f, peerSet("A", "B"), desired(true, "leader"))

	if len(f.removed) != 0 {
		t.Errorf("removed = %v; an empty membership read must never wipe the live mesh", f.removed)
	}
	if len(f.added) != 0 {
		t.Errorf("added = %+v; want none", f.added)
	}
}

func TestReconcileWireGuardPeers_noCurrentPeers_addsAll(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	// The devnet outage shape: interface has zero peers, membership known.
	n.reconcileWireGuardPeersWith(f, peerSet(), desired(false, "local-replica", "A", "B"))

	if len(f.added) != 2 {
		t.Errorf("added %d peers; want 2 (a peerless node must be able to rebuild)", len(f.added))
	}
	if len(f.removed) != 0 {
		t.Errorf("removed = %v; want none", f.removed)
	}
}

func TestReconcileWireGuardPeers_alreadyConverged_noChanges(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	n.reconcileWireGuardPeersWith(f, peerSet("A", "B"), desired(true, "leader", "A", "B"))

	if len(f.added) != 0 || len(f.removed) != 0 {
		t.Errorf("added=%+v removed=%v; a converged mesh must be left alone", f.added, f.removed)
	}
}

// A failing AddPeer must not abort the loop — one unreachable peer should not
// stop the others from being installed.
func TestReconcileWireGuardPeers_addErrorDoesNotAbortRemaining(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{addErr: errFake}

	n.reconcileWireGuardPeersWith(f, peerSet(), desired(true, "leader", "A", "B"))

	if len(f.added) != 0 {
		t.Errorf("added = %+v; the fake fails every add", f.added)
	}
	// Reaching here without panicking is the assertion: errors are logged and
	// iteration continues.
}

func TestShortWGKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"12345678", "12345678"},
		{"123456789", "12345678..."},
	}
	for _, c := range cases {
		if got := shortWGKey(c.in); got != c.want {
			t.Errorf("shortWGKey(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// fakeErr is a canned provisioner failure.
type fakeErr string

func (e fakeErr) Error() string { return string(e) }

var errFake = fakeErr("wg set failed")

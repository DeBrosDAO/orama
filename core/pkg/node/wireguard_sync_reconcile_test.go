package node

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// fakeProvisioner records reconciler decisions instead of touching a real
// WireGuard interface.
type fakeProvisioner struct {
	added      []install.WireGuardPeer
	removed    []string
	persisted  [][]install.WireGuardPeer
	addErr     error
	rmErr      error
	persistErr error
}

func (f *fakeProvisioner) PersistPeers(peers []install.WireGuardPeer) error {
	if f.persistErr != nil {
		return f.persistErr
	}
	f.persisted = append(f.persisted, peers)
	return nil
}

func (f *fakeProvisioner) AddPeer(peer install.WireGuardPeer) error {
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

// testPeer is key's peer: every key its own overlay address, as the registry
// guarantees (wireguard_peers.wg_ip is UNIQUE), and the same endpoint and
// address on both sides, so an existing key looks converged rather than
// drifted unless a test says otherwise.
func testPeer(key string) install.WireGuardPeer {
	sum := 0
	for _, b := range []byte(key) {
		sum += int(b)
	}
	return install.WireGuardPeer{
		PublicKey: key,
		AllowedIP: fmt.Sprintf("10.0.0.%d/32", 10+sum%200),
		Endpoint:  "203.0.113.9:51820",
	}
}

// peerSet builds a live-interface peer map.
func peerSet(keys ...string) map[string]install.WireGuardPeer {
	s := make(map[string]install.WireGuardPeer, len(keys))
	for _, k := range keys {
		s[k] = testPeer(k)
	}
	return s
}

func desired(auth bool, source string, keys ...string) desiredWGPeers {
	m := make(map[string]install.WireGuardPeer, len(keys))
	for _, k := range keys {
		m[k] = testPeer(k)
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

// A peer whose public IP moved keeps the same key, so the old reconciler
// skipped it and the dead endpoint survived forever. That is the manual
// "reset the peer on both sides" recipe in docs/COMMON_PROBLEMS.md.
func TestReconcileWireGuardPeers_appliesEndpointDrift(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	live := peerSet("A")
	moved := live["A"]
	moved.Endpoint = "198.51.100.7:51820" // was 203.0.113.9
	live["A"] = moved

	n.reconcileWireGuardPeersWith(f, live, desired(true, "leader", "A"))

	if len(f.added) != 1 || f.added[0].PublicKey != "A" {
		t.Fatalf("expected the moved peer to be re-applied, got %+v", f.added)
	}
	if f.added[0].Endpoint != "203.0.113.9:51820" {
		t.Errorf("re-applied endpoint = %q, want the desired one", f.added[0].Endpoint)
	}
}

func TestReconcileWireGuardPeers_allowedIPDriftIsApplied(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	live := peerSet("A")
	moved := live["A"]
	moved.AllowedIP = "10.0.0.99/32"
	live["A"] = moved

	n.reconcileWireGuardPeersWith(f, live, desired(true, "leader", "A"))
	if len(f.added) != 1 {
		t.Fatalf("expected the re-addressed peer to be re-applied, got %+v", f.added)
	}
}

// A converged mesh must not churn: no kernel calls, and once the conf is known
// to hold it, no rewrite of the conf.
func TestReconcileWireGuardPeers_noDriftIsQuiet(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	n.reconcileWireGuardPeersWith(f, peerSet("A", "B"), desired(true, "leader", "A", "B"))
	n.reconcileWireGuardPeersWith(f, peerSet("A", "B"), desired(true, "leader", "A", "B"))

	if len(f.added) != 0 || len(f.removed) != 0 {
		t.Errorf("converged mesh churned: added=%+v removed=%+v", f.added, f.removed)
	}
	if len(f.persisted) != 1 {
		t.Errorf("converged mesh wrote the conf %d times, want once (its first pass)", len(f.persisted))
	}
}

// The bug: persistence ran only when a pass changed the interface, so a node
// whose wg0.conf an earlier release left stale, on a mesh that had already
// converged, never had it repaired. The node cannot read the conf, so its
// first pass writes what the interface holds.
func TestReconcileWireGuardPeers_convergedMeshRepairsAStaleConf(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	n.reconcileWireGuardPeersWith(f, peerSet("A", "B"), desired(true, "leader", "A", "B"))

	if len(f.persisted) != 1 || len(f.persisted[0]) != 2 {
		t.Fatalf("expected the live set {A, B} to be written once, got %+v", f.persisted)
	}
}

// A failed write leaves the conf unknown, so the next pass tries again even
// though the interface did not change.
func TestReconcileWireGuardPeers_failedPersistIsRetried(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{persistErr: errFake}
	n.reconcileWireGuardPeersWith(f, peerSet("A"), desired(true, "leader", "A"))

	f.persistErr = nil
	n.reconcileWireGuardPeersWith(f, peerSet("A"), desired(true, "leader", "A"))
	if len(f.persisted) != 1 {
		t.Fatalf("a failed write must be retried on the next pass, got %d writes", len(f.persisted))
	}
}

// The interface can change without this pass changing it (another node's
// repair, an operator's `wg set`): the conf follows what is live.
func TestReconcileWireGuardPeers_liveSetThatDiffersFromTheConfIsWritten(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}
	n.reconcileWireGuardPeersWith(f, peerSet("A"), desired(false, "local-replica", "A"))
	n.reconcileWireGuardPeersWith(f, peerSet("A", "B"), desired(false, "local-replica", "A"))

	if len(f.persisted) != 2 || len(f.persisted[1]) != 2 {
		t.Fatalf("the second live set {A, B} was not written: %+v", f.persisted)
	}
}

// An empty interface is what a node that has not reached its mesh looks like;
// writing it would erase the peers wg-quick restores at boot.
func TestReconcileWireGuardPeers_emptyInterfaceIsNeverWritten(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}
	n.reconcileWireGuardPeersWith(f, peerSet(), desired(false, "local-replica"))
	if len(f.persisted) != 0 {
		t.Fatalf("an empty peer set was written to the conf: %+v", f.persisted)
	}
}

// The whole point of the ticket: what reaches the interface must also reach the
// file, or the mesh regresses on the next `wg-quick up`.
func TestReconcileWireGuardPeers_persistsResultingSet(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	// live A,B — cluster says B,C => add C, remove A => persist {B, C}
	n.reconcileWireGuardPeersWith(f, peerSet("A", "B"), desired(true, "leader", "B", "C"))

	if len(f.persisted) != 1 {
		t.Fatalf("expected exactly one persist, got %d", len(f.persisted))
	}
	got := map[string]bool{}
	for _, p := range f.persisted[0] {
		got[p.PublicKey] = true
	}
	if len(got) != 2 || !got["B"] || !got["C"] {
		t.Errorf("persisted set = %v, want exactly B and C", got)
	}
}

// A non-authoritative read never removes, so the peers it could not confirm
// must still be persisted rather than dropped from the file.
func TestReconcileWireGuardPeers_nonAuthoritativeKeepsUnknownPeers(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}

	n.reconcileWireGuardPeersWith(f, peerSet("A"), desired(false, "local-replica", "B"))

	if len(f.removed) != 0 {
		t.Errorf("non-authoritative read removed peers: %v", f.removed)
	}
	if len(f.persisted) != 1 {
		t.Fatalf("expected one persist, got %d", len(f.persisted))
	}
	got := map[string]bool{}
	for _, p := range f.persisted[0] {
		got[p.PublicKey] = true
	}
	if !got["A"] || !got["B"] {
		t.Errorf("persisted set = %v, want both the unconfirmed A and the new B", got)
	}
}

// A peer that reached the interface is live even if the file write failed. The
// two outcomes are reported separately so an operator can tell "the mesh is
// broken now" from "the mesh will break at next boot".
func TestReconcileWireGuardPeers_persistFailureDoesNotUndoKernelChanges(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{persistErr: errFake}

	n.reconcileWireGuardPeersWith(f, peerSet("A"), desired(true, "leader", "A", "B"))

	if len(f.added) != 1 || f.added[0].PublicKey != "B" {
		t.Errorf("kernel add did not happen: %+v", f.added)
	}
}

// replacedNode is a node replaced on the same overlay address: the registry
// row for 10.0.0.5 now names NEW, while the interface still holds OLD there.
func replacedNode() (map[string]install.WireGuardPeer, install.WireGuardPeer) {
	old := install.WireGuardPeer{PublicKey: "OLD", AllowedIP: "10.0.0.5/32", Endpoint: "203.0.113.5:51820"}
	replacement := install.WireGuardPeer{PublicKey: "NEW", AllowedIP: "10.0.0.5/32", Endpoint: "198.51.100.5:51820"}
	live := peerSet("A")
	live[old.PublicKey] = old
	return live, replacement
}

func persistedAddresses(t *testing.T, peers []install.WireGuardPeer) map[string]string {
	t.Helper()
	owners := make(map[string]string, len(peers))
	for _, p := range peers {
		if other, taken := owners[p.AllowedIP]; taken {
			t.Fatalf("persisted %s for both %s and %s; orama-privhelper refuses such a list", p.AllowedIP, other, p.PublicKey)
		}
		owners[p.AllowedIP] = p.PublicKey
	}
	return owners
}

// A non-authoritative pass never removes OLD, but applying NEW moves the /32
// to it on the interface; the persisted set must follow, with NEW as the
// address's only owner.
func TestReconcileWireGuardPeers_replacementOnTheSameAddressPersistsOneOwner(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{}
	live, replacement := replacedNode()
	want := desired(false, "local-replica", "A")
	want.peers[replacement.PublicKey] = replacement

	n.reconcileWireGuardPeersWith(f, live, want)

	if len(f.removed) != 0 {
		t.Errorf("non-authoritative pass removed %v", f.removed)
	}
	if len(f.persisted) != 1 {
		t.Fatalf("expected one persist, got %d", len(f.persisted))
	}
	owners := persistedAddresses(t, f.persisted[0])
	if owners["10.0.0.5/32"] != "NEW" {
		t.Errorf("10.0.0.5/32 persisted for %q, want the registry's key NEW", owners["10.0.0.5/32"])
	}
	if owners[testPeer("A").AllowedIP] != "A" {
		t.Errorf("an unrelated peer was dropped: %v", owners)
	}
}

// When the replacement never reached the interface, OLD still holds the
// address there, and that is what the file must say.
func TestReconcileWireGuardPeers_failedReplacementKeepsTheHolder(t *testing.T) {
	n := testNode(t)
	f := &fakeProvisioner{addErr: errFake}
	live, replacement := replacedNode()
	want := desired(false, "local-replica")
	want.peers[replacement.PublicKey] = replacement

	n.reconcileWireGuardPeersWith(f, live, want)

	if len(f.persisted) != 1 {
		t.Fatalf("expected one persist, got %d", len(f.persisted))
	}
	if owner := persistedAddresses(t, f.persisted[0])["10.0.0.5/32"]; owner != "OLD" {
		t.Errorf("10.0.0.5/32 persisted for %q, want OLD, which still holds it", owner)
	}
}

func TestReleaseSupersededAddress_ignoresThePrefixSuffix(t *testing.T) {
	live := map[string]install.WireGuardPeer{
		"OLD": {PublicKey: "OLD", AllowedIP: "10.0.0.5"},
		"B":   testPeer("B"),
	}
	releaseSupersededAddress(live, install.WireGuardPeer{PublicKey: "NEW", AllowedIP: "10.0.0.5/32"})
	if _, ok := live["OLD"]; ok {
		t.Error("a /32 written without its suffix was not recognised as the same address")
	}
	if _, ok := live["B"]; !ok {
		t.Error("a peer on another address was dropped")
	}
}

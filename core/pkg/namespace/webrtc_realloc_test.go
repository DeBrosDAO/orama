package namespace

import (
	"context"
	"reflect"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// Bugboard #161 — node replacement left TURN/SFU roles on the departed node.
// Reproduces the exact devnet anchat-test state and pins the reallocation.
func TestPlanWebRTCReallocation_devnetAnchatTestState(t *testing.T) {
	// Live cluster members after the replacement: .56 and .160.
	live := []string{"peer-56", "peer-160"}
	// Roles as actually found: dead node holds turn+sfu; .232 (no longer a
	// member) holds sfu; .56 holds both; .160 holds NOTHING.
	current := []webrtcAllocRef{
		{"peer-dead", "turn"},
		{"peer-dead", "sfu"},
		{"peer-232", "sfu"},
		{"peer-56", "turn"},
		{"peer-56", "sfu"},
	}

	plan := planWebRTCReallocation(current, live, live, DefaultTURNNodeCount)

	wantDealloc := []webrtcAllocRef{
		{"peer-232", "sfu"},
		{"peer-dead", "sfu"},
		{"peer-dead", "turn"},
	}
	if !reflect.DeepEqual(plan.Deallocate, wantDealloc) {
		t.Errorf("Deallocate = %v, want %v (roles on non-members must be dropped)", plan.Deallocate, wantDealloc)
	}
	if !reflect.DeepEqual(plan.AllocateSFU, []string{"peer-160"}) {
		t.Errorf("AllocateSFU = %v, want [peer-160] — the replacement member must get an SFU role, else its config has no ports and crash-loops", plan.AllocateSFU)
	}
	if !reflect.DeepEqual(plan.AllocateTURN, []string{"peer-160"}) {
		t.Errorf("AllocateTURN = %v, want [peer-160] — TURN must return to 2 LIVE relays, not 1 live + 1 dead", plan.AllocateTURN)
	}
}

// Steady state must be a no-op, or the reconciler would thrash every sweep.
func TestPlanWebRTCReallocation_healthyIsNoop(t *testing.T) {
	live := []string{"a", "b", "c"}
	current := []webrtcAllocRef{
		{"a", "sfu"}, {"b", "sfu"}, {"c", "sfu"},
		{"a", "turn"}, {"b", "turn"},
	}
	if plan := planWebRTCReallocation(current, live, live, DefaultTURNNodeCount); !plan.empty() {
		t.Errorf("healthy cluster must not reallocate, got %+v", plan)
	}
}

// TURN must never exceed the desired fan-out, and must cap at the number of
// live members when the cluster has shrunk.
func TestPlanWebRTCReallocation_turnFanoutCapped(t *testing.T) {
	// Only one live member: TURN caps at 1, not DefaultTURNNodeCount.
	plan := planWebRTCReallocation(nil, []string{"only"}, []string{"only"}, DefaultTURNNodeCount)
	if len(plan.AllocateTURN) != 1 {
		t.Errorf("AllocateTURN = %v, want exactly 1 (capped by live member count)", plan.AllocateTURN)
	}
	// Already at the desired count — must not add a third relay.
	current := []webrtcAllocRef{{"a", "turn"}, {"b", "turn"}, {"a", "sfu"}, {"b", "sfu"}, {"c", "sfu"}}
	plan = planWebRTCReallocation(current, []string{"a", "b", "c"}, []string{"a", "b", "c"}, DefaultTURNNodeCount)
	if len(plan.AllocateTURN) != 0 {
		t.Errorf("AllocateTURN = %v, want none — TURN is already at the desired fan-out", plan.AllocateTURN)
	}
}

// Empty membership must not produce allocations (nothing to allocate onto).
func TestPlanWebRTCReallocation_noLiveMembers(t *testing.T) {
	current := []webrtcAllocRef{{"gone", "turn"}, {"gone", "sfu"}}
	plan := planWebRTCReallocation(current, nil, nil, DefaultTURNNodeCount)
	if len(plan.AllocateSFU) != 0 || len(plan.AllocateTURN) != 0 {
		t.Errorf("no live members must yield no allocations, got %+v", plan)
	}
	if len(plan.Deallocate) != 2 {
		t.Errorf("stale roles should still be dropped, got %v", plan.Deallocate)
	}
}

// The coordinator election must be deterministic and identical on every node —
// that is what prevents two nodes double-allocating the same role.
func TestWebRTCReconcileCoordinator_deterministic(t *testing.T) {
	a := webrtcReconcileCoordinator([]string{"c", "a", "b"})
	b := webrtcReconcileCoordinator([]string{"b", "c", "a"})
	if a != "a" || b != "a" {
		t.Errorf("coordinator = %q/%q, want %q from both orderings", a, b, "a")
	}
	if webrtcReconcileCoordinator(nil) != "" {
		t.Error("no members = no coordinator")
	}
}

// REGRESSION (review finding): revocation must key on cluster MEMBERSHIP, not on
// heartbeat liveness. A member that is briefly non-active (a rolling restart, a
// 120s heartbeat gap) must KEEP its roles — stripping them on a blip is exactly
// the thrash this reconciler exists to prevent.
func TestPlanWebRTCReallocation_downMemberKeepsItsRoles(t *testing.T) {
	members := []string{"a", "b", "c"}
	live := []string{"a", "c"} // b is a member but momentarily not heartbeating
	current := []webrtcAllocRef{
		{"a", "sfu"}, {"b", "sfu"}, {"c", "sfu"},
		{"a", "turn"}, {"b", "turn"},
	}

	plan := planWebRTCReallocation(current, members, live, DefaultTURNNodeCount)

	if len(plan.Deallocate) != 0 {
		t.Errorf("Deallocate = %v, want none — a member that missed a heartbeat must keep its roles", plan.Deallocate)
	}
	// b still counts toward the TURN fan-out, so no replacement relay is created.
	if len(plan.AllocateTURN) != 0 {
		t.Errorf("AllocateTURN = %v, want none — a down member's TURN role still counts, else we over-provision to 3 relays", plan.AllocateTURN)
	}
}

// Allocation targets must be LIVE members — never a member that is currently down.
func TestPlanWebRTCReallocation_allocatesOnlyToLiveMembers(t *testing.T) {
	members := []string{"a", "b"}
	live := []string{"a"} // b is a member but down
	// Nobody holds anything yet.
	plan := planWebRTCReallocation(nil, members, live, DefaultTURNNodeCount)

	for _, id := range append(append([]string{}, plan.AllocateSFU...), plan.AllocateTURN...) {
		if id != "a" {
			t.Errorf("allocated to %q, but only live member is \"a\" — never target a down node", id)
		}
	}
}

// REGRESSION (review finding, CRITICAL): a TURN allocation missing its listen or
// TLS port must never be spawned. "0.0.0.0:0" is not an error to the network
// stack — it binds a RANDOM ephemeral port, so the unit reports active, health
// checks pass, and the node stays in TURN DNS while relaying nothing. A silent
// dead relay is worse than the crash-loop it replaces.
func TestTurnPortBlockSpawnable(t *testing.T) {
	full := &WebRTCPortBlock{TURNListenPort: 3478, TURNTLSPort: 5349, TURNRelayPortStart: 49152, TURNRelayPortEnd: 49951}
	if !turnPortBlockSpawnable(full) {
		t.Error("a complete TURN allocation must be spawnable")
	}
	for name, b := range map[string]*WebRTCPortBlock{
		"nil":            nil,
		"no listen port": {TURNListenPort: 0, TURNTLSPort: 5349, TURNRelayPortStart: 49152, TURNRelayPortEnd: 49951},
		"no tls port":    {TURNListenPort: 3478, TURNTLSPort: 0, TURNRelayPortStart: 49152, TURNRelayPortEnd: 49951},
		"no relay start": {TURNListenPort: 3478, TURNTLSPort: 5349, TURNRelayPortStart: 0, TURNRelayPortEnd: 49951},
		"no relay end":   {TURNListenPort: 3478, TURNTLSPort: 5349, TURNRelayPortStart: 49152, TURNRelayPortEnd: 0},
	} {
		if turnPortBlockSpawnable(b) {
			t.Errorf("%s: must NOT be spawnable — a zero port binds a random one and silently relays nothing", name)
		}
	}
}

// The quorum floor is the only thing between a partition and a role stampede.
func TestWebRTCReconcileQuorumOK(t *testing.T) {
	cases := []struct {
		live, members int
		want          bool
		why           string
	}{
		{2, 3, true, "majority of a 3-node cluster may act"},
		{1, 3, false, "a lone survivor must NOT pull every role onto itself"},
		{1, 2, false, "half is not a majority"},
		{1, 1, true, "a single-member cluster is its own majority"},
		{0, 0, false, "nothing live, nothing to do"},
		{0, 3, false, "no live view at all must never reshape roles"},
		{3, 3, true, "fully healthy"},
	}
	for _, c := range cases {
		if got := webrtcReconcileQuorumOK(c.live, c.members); got != c.want {
			t.Errorf("quorumOK(live=%d, members=%d) = %v, want %v — %s", c.live, c.members, got, c.want, c.why)
		}
	}
}

// MED-3: two overlapping majorities can each add a relay, pushing TURN past the
// desired fan-out. Nothing else removes the excess, and every extra relay locks
// 3478/5349 against other namespaces on that host — so the planner must trim.
func TestPlanWebRTCReallocation_trimsSurplusTURN(t *testing.T) {
	members := []string{"a", "b", "c"}
	current := []webrtcAllocRef{
		{"a", "sfu"}, {"b", "sfu"}, {"c", "sfu"},
		{"a", "turn"}, {"b", "turn"}, {"c", "turn"}, // 3 relays, want 2
	}

	plan := planWebRTCReallocation(current, members, members, 2)

	if len(plan.Deallocate) != 1 {
		t.Fatalf("Deallocate = %v, want exactly 1 surplus TURN dropped", plan.Deallocate)
	}
	got := plan.Deallocate[0]
	if got.ServiceType != "turn" || got.NodeID != "c" {
		t.Errorf("dropped %+v, want the highest-sorted TURN holder (c) so every node picks the same one", got)
	}
	if len(plan.AllocateTURN) != 0 {
		t.Errorf("must not allocate while trimming, got %v", plan.AllocateTURN)
	}
}

// MED-2: members and live are read as two separate queries, so a node can appear
// in live but not in members. It must never be an allocation target — otherwise
// the same plan deallocates its roles and re-allocates to it.
func TestPlanWebRTCReallocation_neverAllocatesToNonMember(t *testing.T) {
	members := []string{"a"}
	live := []string{"a", "ghost"} // ghost is live but not a member

	plan := planWebRTCReallocation(nil, members, live, DefaultTURNNodeCount)

	for _, id := range append(append([]string{}, plan.AllocateSFU...), plan.AllocateTURN...) {
		if id == "ghost" {
			t.Error("allocated a role to a node that is not a cluster member — it would be deallocated in the same pass")
		}
	}
}

// These exercise the PRODUCTION guards, not local copies — deleting the guard in
// cluster_manager_webrtc.go must fail a test here.

// CR-1 (security audit, CRITICAL): a node that cannot identify itself must never
// stop services. GetTURNPorts(clusterID, "") is a CLEAN read returning no rows —
// indistinguishable from "role revoked" — so an unreadable identity.key would
// otherwise stop every WebRTC service on the node, permanently.
//
// With a zero ClusterManager the guard returns before touching the (nil)
// allocator or spawner; reaching either would panic. A clean return therefore
// proves the guard fired.
func TestStopUnallocatedWebRTCServices_failsClosedOnUnknownIdentity(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop(), systemdSpawner: &SystemdSpawner{systemdMgr: &systemd.Manager{}}}
	// localNodeID is empty — must bail before any allocator call.
	cm.stopUnallocatedWebRTCServices(context.Background(), "cluster-1", "ns")
	// clusterID empty — same.
	cm.localNodeID = "node-1"
	cm.stopUnallocatedWebRTCServices(context.Background(), "", "ns")
}

// The whole sweep must also refuse to run without an identity.
func TestReconcileWebRTCForLocalNamespaces_bailsWithoutIdentity(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop()} // nil allocator/spawner: any work panics
	cm.reconcileWebRTCForLocalNamespaces(context.Background())
}

// Spawn must refuse when cluster-state.json lacks the fields it would otherwise
// feed straight into the SFU listen address (an empty LocalIP binds ALL
// interfaces, including the public one, instead of the WireGuard address).
func TestSpawnAllocatedWebRTCServices_refusesIncompleteLocalState(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop(), localNodeID: "node-1",
		systemdSpawner: &SystemdSpawner{systemdMgr: &systemd.Manager{}}}
	// nil allocator: reaching the allocation read would panic.
	cm.spawnAllocatedWebRTCServices(context.Background(),
		&ClusterLocalState{NamespaceName: "ns", ClusterID: "c1", LocalIP: ""}, &WebRTCConfig{})
	cm.spawnAllocatedWebRTCServices(context.Background(),
		&ClusterLocalState{NamespaceName: "ns", ClusterID: "c1", LocalIP: "10.0.0.2"}, &WebRTCConfig{})
}

// The spawn backoff must actually suppress a retry after a failure.
func TestSpawnBackoff_suppressesRetry(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop()}
	if cm.spawnBackoffActive("ns") {
		t.Error("no failure recorded yet — must not be backing off")
	}
	cm.recordSpawnFailure("ns")
	if !cm.spawnBackoffActive("ns") {
		t.Error("after a failed spawn the namespace must back off, else a crash-looping unit is restarted every tick")
	}
	if cm.spawnBackoffActive("other-ns") {
		t.Error("backoff must be per-namespace — one wedged namespace must not stall the others")
	}
}

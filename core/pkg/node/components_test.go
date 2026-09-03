package node

import (
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/node/boot"
	"github.com/DeBrosOfficial/network/pkg/node/lifecycle"
)

func TestNextLifecycleState(t *testing.T) {
	// The serving core is rqlite-local plus gateway; "other" stands for any
	// component outside it.
	snap := func(rqlite, gateway, other boot.Status) boot.Snapshot {
		return boot.Snapshot{Components: []boot.ComponentStatus{
			{Name: compRQLiteLocal, Status: rqlite},
			{Name: compGateway, Status: gateway},
			{Name: "other", Status: other},
		}}
	}

	tests := []struct {
		name    string
		current lifecycle.State
		snap    boot.Snapshot
		want    lifecycle.State
		change  bool
	}{
		{
			name:    "still bringing the serving core up stays joining",
			current: lifecycle.StateJoining,
			snap:    snap(boot.StatusPending, boot.StatusBlocked, boot.StatusBlocked),
			want:    lifecycle.StateJoining,
			change:  false,
		},
		{
			name:    "serving core up but the rest is not is degraded",
			current: lifecycle.StateJoining,
			snap:    snap(boot.StatusReady, boot.StatusReady, boot.StatusPending),
			want:    lifecycle.StateDegraded,
			change:  true,
		},
		{
			// A broken ntfy inside `edge` must not hold the node out of
			// IsAvailable, or it would never announce maintenance on shutdown.
			name:    "a non-core component that can never converge must not pin the node in joining",
			current: lifecycle.StateJoining,
			snap: boot.Snapshot{Components: []boot.ComponentStatus{
				{Name: compRQLiteLocal, Status: boot.StatusReady},
				{Name: compGateway, Status: boot.StatusReady},
				{Name: compEdgeAux, Status: boot.StatusPending, Attempts: 40},
			}},
			want:   lifecycle.StateDegraded,
			change: true,
		},
		{
			name:    "everything converged is active",
			current: lifecycle.StateDegraded,
			snap:    snap(boot.StatusReady, boot.StatusReady, boot.StatusReady),
			want:    lifecycle.StateActive,
			change:  true,
		},
		{
			name:    "losing quorum after being active degrades",
			current: lifecycle.StateActive,
			snap:    snap(boot.StatusReady, boot.StatusReady, boot.StatusPending),
			want:    lifecycle.StateDegraded,
			change:  true,
		},
		{
			name:    "the serving core failing after boot degrades, it does not rejoin",
			current: lifecycle.StateActive,
			snap:    snap(boot.StatusPending, boot.StatusBlocked, boot.StatusBlocked),
			want:    lifecycle.StateDegraded,
			change:  true,
		},
		{
			name:    "already degraded and still degraded is not a transition",
			current: lifecycle.StateDegraded,
			snap:    snap(boot.StatusReady, boot.StatusReady, boot.StatusPending),
			want:    lifecycle.StateDegraded,
			change:  false,
		},
		{
			name:    "maintenance is operator-driven and never overridden",
			current: lifecycle.StateMaintenance,
			snap:    snap(boot.StatusReady, boot.StatusReady, boot.StatusReady),
			want:    lifecycle.StateMaintenance,
			change:  false,
		},
		{
			name:    "draining is operator-driven and never overridden",
			current: lifecycle.StateDraining,
			snap:    snap(boot.StatusReady, boot.StatusReady, boot.StatusReady),
			want:    lifecycle.StateDraining,
			change:  false,
		},
		{
			name:    "an empty snapshot counts as converged",
			current: lifecycle.StateJoining,
			snap:    boot.Snapshot{},
			want:    lifecycle.StateActive,
			change:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, change := nextLifecycleState(tc.current, tc.snap)
			if got != tc.want || change != tc.change {
				t.Fatalf("nextLifecycleState = (%q, %v), want (%q, %v)", got, change, tc.want, tc.change)
			}
		})
	}
}

// A node stuck in joining is not IsAvailable, so it would never announce
// maintenance on shutdown. That is the failure the serving-core rule prevents.
func TestNextLifecycleState_joiningNodeBecomesAvailableOnceItServes(t *testing.T) {
	m := lifecycle.NewManager()
	snap := boot.Snapshot{Components: []boot.ComponentStatus{
		{Name: compRQLiteLocal, Status: boot.StatusReady},
		{Name: compGateway, Status: boot.StatusReady},
		{Name: compEdgeAux, Status: boot.StatusPending},
	}}

	want, change := nextLifecycleState(m.State(), snap)
	if !change {
		t.Fatal("a serving node must leave the joining state")
	}
	if err := m.TransitionTo(want); err != nil {
		t.Fatalf("TransitionTo(%q): %v", want, err)
	}
	if !m.IsAvailable() {
		t.Fatal("a serving node must be available, or it never announces maintenance on shutdown")
	}
}

// newGraphNode builds a Node with only the fields the graph declaration touches.
func newGraphNode(t *testing.T) *Node {
	t.Helper()
	n, err := NewNode(&config.Config{})
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	return n
}

func TestRegisterComponents_declaresAValidGraph(t *testing.T) {
	n := newGraphNode(t)
	sup := boot.New(nil, boot.Options{})
	n.registerComponents(sup)

	if err := sup.Err(); err != nil {
		t.Fatalf("component graph is invalid: %v", err)
	}

	snap := sup.Snapshot()
	if len(snap.Components) == 0 {
		t.Fatal("no components registered")
	}
	if snap.AllReady() {
		t.Fatal("a freshly registered graph must not report itself converged")
	}
}

// dependsOnQuorum reports the set of components that transitively depend on
// the quorum gate. That set IS the cluster tier — nothing else needs a leader
// to converge.
func dependsOnQuorum(components []boot.Component) map[string]bool {
	cluster := map[string]bool{compRQLiteCluster: true}
	for _, c := range components { // valid dependency order, so one pass suffices
		for _, dep := range c.DependsOn {
			if cluster[dep] {
				cluster[c.Name] = true
			}
		}
	}
	return cluster
}

func TestBootComponents_onlyTheQuorumGateAndItsDependentsNeedALeader(t *testing.T) {
	n := newGraphNode(t)
	cluster := dependsOnQuorum(n.bootComponents())

	// These need a raft leader and nothing else may be behind them.
	for _, name := range []string{compRQLiteCluster, compDNSRegistration} {
		if !cluster[name] {
			t.Errorf("%q should depend on the quorum gate", name)
		}
	}

	// These must keep serving on a node that cannot reach a quorum.
	mustServeWithoutQuorum := []string{
		compWireGuard, compLibP2P, compStorage, compClusterDiscovery, compRQLiteLocal,
		compNameserver, compPubsub, compGateway, compEdgeServing, compEdgeAux, compMonitoring,
		compWireGuardSync, compIPFSSwarmSync,
	}
	for _, name := range mustServeWithoutQuorum {
		if cluster[name] {
			t.Errorf("%q transitively depends on the quorum gate, so a peer outage would keep it from starting", name)
		}
	}
}

func TestBootComponents_servingCoreConvergesWithoutAQuorum(t *testing.T) {
	n := newGraphNode(t)
	cluster := dependsOnQuorum(n.bootComponents())

	// If any part of the serving core needed a quorum, a node alone in the
	// world could never leave the joining state.
	for _, name := range servingCore {
		if cluster[name] {
			t.Errorf("serving-core component %q depends on the quorum gate", name)
		}
	}
}

func TestBootComponents_slowWorkDoesNotDelayIndependentComponents(t *testing.T) {
	n := newGraphNode(t)
	components := n.bootComponents()

	index := map[string]int{}
	for i, c := range components {
		index[c.Name] = i
	}

	// One pass runs components in order, so anything that can block for
	// minutes must not sit in front of work that does not depend on it.
	for _, name := range []string{compMonitoring, compPubsub} {
		if index[name] > index[compRQLiteLocal] {
			t.Errorf("%q is ordered after %q, so a slow rqlite delays it for no reason", name, compRQLiteLocal)
		}
	}
}

func TestBootComponents_everyDependencyIsDeclaredBeforeItIsUsed(t *testing.T) {
	n := newGraphNode(t)
	seen := map[string]bool{}
	for _, c := range n.bootComponents() {
		for _, dep := range c.DependsOn {
			if !seen[dep] {
				t.Errorf("%q depends on %q, which is not declared before it", c.Name, dep)
			}
		}
		seen[c.Name] = true
	}
}

func TestP2PPort(t *testing.T) {
	tests := []struct {
		name  string
		addrs []string
		want  int
	}{
		{"no listen addresses", nil, defaultP2PPort},
		{"empty listen address", []string{""}, defaultP2PPort},
		{"tcp multiaddr", []string{"/ip4/0.0.0.0/tcp/4101"}, 4101},
		{"ipv6 multiaddr", []string{"/ip6/::/tcp/5001"}, 5001},
		{"no port after tcp", []string{"/ip4/0.0.0.0/tcp"}, defaultP2PPort},
		{"unparseable port", []string{"/ip4/0.0.0.0/tcp/abc"}, defaultP2PPort},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := &Node{config: &config.Config{}}
			n.config.Node.ListenAddresses = tc.addrs
			if got := n.p2pPort(); got != tc.want {
				t.Fatalf("p2pPort() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAdvertiseIP(t *testing.T) {
	tests := []struct {
		name     string
		http     string
		raft     string
		want     string
		protocol string
	}{
		{"http address preferred", "10.0.0.4:10100", "10.0.0.4:10101", "10.0.0.4", "ip4"},
		{"falls back to raft", "", "10.0.0.9:10101", "10.0.0.9", "ip4"},
		{"localhost is not advertisable", "localhost:10100", "10.0.0.9:10101", "10.0.0.9", "ip4"},
		{"nothing configured", "", "", "0.0.0.0", "ip4"},
		{"ipv6", "[fd00::1]:10100", "", "fd00::1", "ip6"},
		{"malformed address", "not-an-address", "10.0.0.9:10101", "10.0.0.9", "ip4"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := &Node{config: &config.Config{}}
			n.config.Discovery.HttpAdvAddress = tc.http
			n.config.Discovery.RaftAdvAddress = tc.raft
			got := n.advertiseIP()
			if got != tc.want {
				t.Fatalf("advertiseIP() = %q, want %q", got, tc.want)
			}
			if proto := advertiseIPProtocol(got); proto != tc.protocol {
				t.Fatalf("advertiseIPProtocol(%q) = %q, want %q", got, proto, tc.protocol)
			}
		})
	}
}

// The whole design rests on Reconcile being idempotent: the supervisor calls it
// again after every failure. These pin the guards that make that true for the
// components whose second call would otherwise do damage.

func TestStartLibP2P_secondCallIsANoOpOnceStarted(t *testing.T) {
	n := newGraphNode(t)
	n.libp2pStarted = true

	if err := n.startLibP2P(); err != nil {
		t.Fatalf("startLibP2P on an already-started node: %v", err)
	}
	if n.host != nil {
		t.Fatal("a second call must not create a host — two hosts would put this node's identity on the network twice")
	}
}

func TestStartClusterDiscovery_refusesWithoutLibP2P(t *testing.T) {
	n := newGraphNode(t)

	err := n.startClusterDiscovery(context.Background())
	if err == nil {
		t.Fatal("cluster discovery must not claim success without a libp2p host")
	}
	if n.getClusterDiscovery() != nil {
		t.Fatal("a failed start must not publish a discovery service")
	}
}

func TestStartIPFSClusterConfig_noOpWithoutAClusterAPI(t *testing.T) {
	n := newGraphNode(t)
	n.config.Database.IPFS.ClusterAPIURL = ""

	for i := 0; i < 3; i++ {
		if err := n.startIPFSClusterConfig(); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if n.getClusterConfigManager() != nil {
		t.Fatal("a node with no cluster API must not build a cluster config manager")
	}
}

func TestStartIPFSSwarmSync_startsOneLoop(t *testing.T) {
	n := newGraphNode(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		if err := n.startIPFSSwarmSync(ctx); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}

	// sync.Once is what makes the repeated calls safe; assert it actually fired
	// rather than trusting the call to have been a no-op.
	started := false
	n.ipfsSwarmSyncOnce.Do(func() { started = true })
	if started {
		t.Fatal("the swarm sync loop guard never fired, so each reconcile would start another goroutine")
	}
}

func TestStartConnectionMonitoring_startsOneLoop(t *testing.T) {
	n := newGraphNode(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 3; i++ {
		n.startConnectionMonitoring(ctx)
	}

	started := false
	n.monitoringOnce.Do(func() { started = true })
	if started {
		t.Fatal("the monitoring guard never fired, so each reconcile would start another goroutine")
	}
}

func TestNodeID_defaultsWhenUnset(t *testing.T) {
	n := newGraphNode(t)
	if got := n.nodeID(); got != "node" {
		t.Fatalf("nodeID() = %q, want %q", got, "node")
	}

	n.config.Node.ID = "node-4"
	if got := n.nodeID(); got != "node-4" {
		t.Fatalf("nodeID() = %q, want %q", got, "node-4")
	}
}

// A dns_nodes row saying `active` routes real traffic to this node. The old
// sequential start-up registered last, so a node whose Caddy failed exited
// before advertising itself; with independent components the registration must
// depend on the units that make the promise true, or it fails open.
func TestBootComponents_dnsRegistrationDependsOnEverythingItPromises(t *testing.T) {
	n := newGraphNode(t)

	var dns boot.Component
	for _, c := range n.bootComponents() {
		if c.Name == compDNSRegistration {
			dns = c
		}
	}
	if dns.Name == "" {
		t.Fatal("dns-registration is not registered")
	}

	declared := map[string]bool{}
	for _, dep := range dns.DependsOn {
		declared[dep] = true
	}

	for _, promised := range []string{compRQLiteCluster, compGateway, compNameserver, compEdgeServing} {
		if !declared[promised] {
			t.Errorf("dns-registration does not depend on %q, so a node that cannot serve would still register as active", promised)
		}
	}

	// ntfy and the anyone-client serve no traffic for this node. Gating the
	// registration on them would take a healthy node out of DNS for nothing.
	if declared[compEdgeAux] {
		t.Error("dns-registration must not depend on edge-aux — a broken ntfy would remove a serving node from DNS")
	}
}

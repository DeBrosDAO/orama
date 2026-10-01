//go:build e2e_fleet

package wireguardfirewall

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Ports a node may listen on at a public address (docs/ARCHITECTURE.md edge
// ports, core/pkg/install/firewall.go GenerateRules): SSH, HTTP(S), WireGuard;
// DNS on a nameserver; TURN while the host relays. DHCP is the OS's client.
var (
	publicTCP      = map[int]bool{22: true, 80: true, 443: true}
	publicUDP      = map[int]bool{infra.WireGuardPort: true, 68: true}
	nameserverPort = 53
	turnTCP        = map[int]bool{3478: true, 5349: true}
	turnUDPPort    = 3478
)

// Linux's default ephemeral range: a UDP socket bound there with no rule is a
// client's (a resolver, an outbound query), not a service.
const ephemeralLow, ephemeralHigh = 32768, 60999

// TURN relay range the firewall opens while the host relays.
const relayLow, relayHigh = 49152, 65535

// allowedPublic says whether l may listen at a public address on n.
func allowedPublic(l fleet.Listener, n fleet.Node, turn bool) bool {
	ns := n.Role == fleet.RoleNameserver
	switch {
	case strings.HasPrefix(l.Proto, "tcp"):
		return publicTCP[l.Port] || (ns && l.Port == nameserverPort) || (turn && turnTCP[l.Port])
	case strings.HasPrefix(l.Proto, "udp"):
		return publicUDP[l.Port] || (ns && l.Port == nameserverPort) || (turn && (l.Port == turnUDPPort || (l.Port >= relayLow && l.Port <= relayHigh)))
	}
	return false
}

// private reports an address only the host or the mesh reaches.
func private(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	_, mesh, _ := net.ParseCIDR(infra.WireGuardSubnet)
	return ip.IsLoopback() || mesh.Contains(ip)
}

// TestListeners_onlyEdgePortsPublic: on every node, a socket bound to a
// public address (a wildcard or the node's own public IP) is one of the
// documented edge ports; everything else binds loopback or the WireGuard
// address (docs/SECURITY.md "Listeners on the overlay, not every interface",
// "Local APIs bind loopback").
func TestListeners_onlyEdgePortsPublic(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		turn := infra.HostRunsTURN(t, f, n)
		for _, l := range f.Listeners(t, n) {
			if private(l.Addr) {
				continue
			}
			public := l.Public() || l.Addr == n.PublicIP
			if !public {
				t.Errorf("%s: %s %s:%d (%s) binds an address that is neither public, loopback nor the mesh", n.Name, l.Proto, l.Addr, l.Port, l.Process)
				continue
			}
			if allowedPublic(l, n, turn) {
				continue
			}
			if strings.HasPrefix(l.Proto, "udp") && l.Port >= ephemeralLow && l.Port <= ephemeralHigh && l.Process != "" {
				t.Logf("%s: client UDP socket %s:%d (%s)", n.Name, l.Addr, l.Port, l.Process)
				continue
			}
			t.Errorf("%s: %s listens publicly on %s:%d (%s)", n.Name, l.Proto, l.Addr, l.Port, l.Process)
		}
	}
}

// TestListeners_internalsOnTheirAddress: the index internals are where the
// docs put them: rqlite HTTP and raft, Olric and the gateway on the node's
// WireGuard address (the gateway also on loopback for Caddy), the chain's
// P2P on the WireGuard address and its RPC on loopback.
func TestListeners_internalsOnTheirAddress(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		wg := wgAddress(t, f, n)
		ls := f.Listeners(t, n)
		want := map[int][]string{infra.IndexRQLiteHTTP: {wg}, infra.IndexRQLiteRaft: {wg}, 10102: {wg}, 10104: {wg, "127.0.0.1"}}
		// The stagenet chain runs in the orama-global netns: its ports are not
		// host listeners, so only a fleet run's chain is asserted here.
		if f.State.ChainID != "" && !f.State.IsStagenet() {
			want[infra.ChainP2PPort] = []string{wg}
			want[infra.ChainRPCPort] = []string{"127.0.0.1"}
		}
		for port, addrs := range want {
			got := boundTCP(ls, port)
			if !sameSet(got, addrs) {
				t.Errorf("%s: tcp %d binds %v, want exactly %v", n.Name, port, got, addrs)
			}
		}
	}
}

func boundTCP(ls []fleet.Listener, port int) []string {
	var out []string
	for _, l := range ls {
		if strings.HasPrefix(l.Proto, "tcp") && l.Port == port {
			out = append(out, l.Addr)
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, x := range b {
		if !seen[x] {
			return false
		}
	}
	return true
}

// TestListeners_rqliteRefusesTheInternet: rqlite's port is not reachable on
// the public address from another node (it binds only the mesh).
func TestListeners_rqliteRefusesTheInternet(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for i, n := range f.State.Nodes {
		other := f.State.Nodes[(i+1)%len(f.State.Nodes)]
		cmd := fmt.Sprintf("timeout 5 bash -c '</dev/tcp/%s/%d' && echo OPEN || echo CLOSED", other.PublicIP, infra.IndexRQLiteHTTP)
		if out := f.MustExec(t, n, cmd); strings.Contains(out.Stdout, "OPEN") {
			t.Errorf("%s reaches rqlite on %s's public address", n.Name, other.Name)
		}
	}
}

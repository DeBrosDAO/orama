//go:build e2e_fleet

package wireguardfirewall

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Ports a node may listen on at a public address (docs/ARCHITECTURE.md edge
// ports, core/pkg/install/firewall.go GenerateRules): SSH, HTTP(S), WireGuard;
// DNS on a nameserver; TURN while the host relays; the global layer's ports
// on a global node. DHCP (68, and DHCPv6's 546 on the link-local address) is
// the OS's client.
var (
	publicTCP      = map[int]bool{22: true, 80: true, 443: true}
	publicUDP      = map[int]bool{infra.WireGuardPort: true, 68: true, 546: true}
	nameserverPort = 53
	turnTCP        = map[int]bool{3478: true, 5349: true}
	turnUDPPort    = 3478
)

// Linux's default ephemeral range: a UDP socket bound there with no rule is a
// client's (a resolver, an outbound query), not a service.
const ephemeralLow, ephemeralHigh = 32768, 60999

// TURN relay range the firewall opens while the host relays.
const relayLow, relayHigh = 49152, 65535

// edge is what a node publishes beyond the always-open ports: TURN while it
// relays, and the global layer's ports (chain P2P, public Kubo swarm, storage
// provider) when the node is also a global node, which is what the firewall's
// orama-global rules say.
type edge struct {
	turn bool
	// global is the global ports a HOST process may listen on; forwarded adds
	// the ports ufw forwards to the orama-global namespace, which are open from
	// the internet but are served inside the namespace, never by the host.
	global    map[string]bool
	forwarded map[string]bool
	ufw       string
}

// nodeEdge reads n's optional public ports.
func nodeEdge(t testing.TB, f *fleet.Fleet, n fleet.Node) edge {
	t.Helper()
	ufw := f.MustExec(t, n, "ufw status verbose").Stdout
	return edge{turn: infra.HostRunsTURN(t, f, n), global: fleet.GlobalHostPorts(ufw), forwarded: fleet.GlobalPublicPorts(ufw), ufw: ufw}
}

// allowedPublic says whether l may listen at a public address on n.
func allowedPublic(l fleet.Listener, n fleet.Node, e edge) bool {
	ns := n.Role == fleet.RoleNameserver
	switch {
	case strings.HasPrefix(l.Proto, "tcp"):
		return publicTCP[l.Port] || (ns && l.Port == nameserverPort) || (e.turn && turnTCP[l.Port]) || e.global["tcp/"+strconv.Itoa(l.Port)]
	case strings.HasPrefix(l.Proto, "udp"):
		return publicUDP[l.Port] || (ns && l.Port == nameserverPort) || (e.turn && (l.Port == turnUDPPort || (l.Port >= relayLow && l.Port <= relayHigh))) || e.global["udp/"+strconv.Itoa(l.Port)]
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
		e := nodeEdge(t, f, n)
		used := map[config.HostListener]bool{}
		for _, l := range f.Listeners(t, n) {
			if private(l.Addr) {
				continue
			}
			if h, ok := declaredHostListener(f, n, l); ok {
				used[h] = true
				t.Logf("%s: %s %s:%d (%s) is a declared host extra: %s", n.Name, l.Proto, l.Addr, l.Port, l.Process, h.Why)
				continue
			}
			public := l.Public() || l.Addr == n.PublicIP
			if !public {
				t.Errorf("%s: %s %s:%d (%s) binds an address that is neither public, loopback nor the mesh", n.Name, l.Proto, l.Addr, l.Port, l.Process)
				continue
			}
			if allowedPublic(l, n, e) {
				continue
			}
			if unit, ok := tenantDeployment(t, f, n, l, e); ok {
				t.Logf("%s: %s %s:%d (%s) is tenant deployment %s on its own port, kept off the internet by ufw's default deny", n.Name, l.Proto, l.Addr, l.Port, l.Process, unit)
				continue
			}
			if strings.HasPrefix(l.Proto, "udp") && l.Port >= ephemeralLow && l.Port <= ephemeralHigh && l.Process != "" {
				t.Logf("%s: client UDP socket %s:%d (%s)", n.Name, l.Addr, l.Port, l.Process)
				continue
			}
			t.Errorf("%s: %s listens publicly on %s:%d (%s)", n.Name, l.Proto, l.Addr, l.Port, l.Process)
		}
		for _, h := range declaredHostListeners(f, n) {
			if !used[h] {
				t.Errorf("%s: the declared host extra %s %s:%d is not listening any more: remove it from config.StagenetHostListeners", n.Name, h.Process, h.Proto, h.Port)
			}
		}
	}
}

// tenantDeployment says whether l is a tenant deployment's own socket: the
// process runs in an orama-deploy-<runtime>@<instance>.service cgroup, the port
// is in the deployment range, ufw is active with a default deny of incoming
// traffic, and no ufw allow rule opens the port. A deployment
// binds the address its code picks (the unit confines it to its one port, not
// to loopback: it must still dial out), so the firewall is what keeps the port
// off the internet; a deployment port ufw allows is not excused.
func tenantDeployment(t testing.TB, f *fleet.Fleet, n fleet.Node, l fleet.Listener, e edge) (string, bool) {
	t.Helper()
	if !strings.HasPrefix(l.Proto, "tcp") || !fleet.InDeploymentRange(l.Port) || !fleet.UFWDefaultDenyActive(e.ufw) || fleet.UFWAllowsPort(e.ufw, l.Proto, l.Port) {
		return "", false
	}
	cmd := fmt.Sprintf(`pid=$(ss -H -ltnp 'sport = :%d' | grep -o 'pid=[0-9]*' | head -n1 | cut -d= -f2); [ -n "$pid" ] && cat /proc/$pid/cgroup`, l.Port)
	out := f.Exec(t, n, cmd)
	if out.Exit != 0 {
		return "", false
	}
	return fleet.DeployUnitFromCgroup(out.Stdout)
}

// declaredHostListener is the operator's declaration (stagenet target only)
// that covers l, if any.
func declaredHostListener(f *fleet.Fleet, n fleet.Node, l fleet.Listener) (config.HostListener, bool) {
	if !f.State.IsStagenet() {
		return config.HostListener{}, false
	}
	return config.StagenetHostListener(n.Name, l.Proto, l.Addr, l.Port, l.Process)
}

// declaredHostListeners are the declarations for n's host extras.
func declaredHostListeners(f *fleet.Fleet, n fleet.Node) []config.HostListener {
	if !f.State.IsStagenet() {
		return nil
	}
	return config.StagenetHostListenersOn(n.Name)
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

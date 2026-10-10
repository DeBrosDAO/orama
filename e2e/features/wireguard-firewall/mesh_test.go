//go:build e2e_fleet

package wireguardfirewall

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// handshakeFresh: WireGuard re-handshakes every two minutes while traffic
// flows, so a live peer's last handshake is younger than three.
const handshakeFresh = 3 * time.Minute

// wgPeer is one line of `wg show wg0 dump` after the interface line.
type wgPeer struct {
	Key, Endpoint, AllowedIPs string
	Handshake                 time.Time
}

func wgDump(t testing.TB, f *fleet.Fleet, n fleet.Node) []wgPeer {
	t.Helper()
	out := f.MustExec(t, n, "wg show wg0 dump").Stdout
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var peers []wgPeer
	for _, l := range lines[1:] {
		fs := strings.Split(l, "\t")
		if len(fs) < 5 {
			t.Fatalf("%s: unexpected wg dump line %q", n.Name, l)
		}
		sec, err := strconv.ParseInt(fs[4], 10, 64)
		if err != nil {
			t.Fatalf("%s: handshake %q: %v", n.Name, fs[4], err)
		}
		peers = append(peers, wgPeer{Key: fs[0], Endpoint: fs[2], AllowedIPs: fs[3], Handshake: time.Unix(sec, 0)})
	}
	return peers
}

func wgAddress(t testing.TB, f *fleet.Fleet, n fleet.Node) string {
	t.Helper()
	out := strings.TrimSpace(f.MustExec(t, n, "ip -4 -o addr show dev wg0 | awk '{print $4}'").Stdout)
	ip, _, err := net.ParseCIDR(out)
	if err != nil {
		t.Fatalf("%s: wg0 address %q: %v", n.Name, out, err)
	}
	return ip.String()
}

// TestMesh_fullAndFresh: every node has exactly one peer per other node,
// reached at that node's public address on 51820, routing only that node's
// /32, with a recent handshake: the overlay is a full mesh and live
// (website/src/docs/contributor/architecture-reference.mdx "Inter-node traffic uses the WireGuard overlay").
func TestMesh_fullAndFresh(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	addrs := map[string]string{}
	for _, n := range f.State.Nodes {
		addrs[n.Name] = wgAddress(t, f, n)
	}
	_, subnet, err := net.ParseCIDR(infra.WireGuardSubnet)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range f.State.Nodes {
		if !subnet.Contains(net.ParseIP(addrs[n.Name])) {
			t.Errorf("%s: wg0 is %s, outside %s", n.Name, addrs[n.Name], infra.WireGuardSubnet)
		}
		peers := wgDump(t, f, n)
		if len(peers) != len(f.State.Nodes)-1 {
			t.Errorf("%s has %d peers, want %d", n.Name, len(peers), len(f.State.Nodes)-1)
		}
		for _, other := range f.State.Nodes {
			if other.Name == n.Name {
				continue
			}
			requirePeer(t, n, other, addrs[other.Name], peers)
		}
	}
}

func requirePeer(t testing.TB, n, other fleet.Node, otherWG string, peers []wgPeer) {
	t.Helper()
	for _, p := range peers {
		if p.AllowedIPs != otherWG+"/32" {
			continue
		}
		if p.Endpoint != net.JoinHostPort(other.PublicIP, strconv.Itoa(infra.WireGuardPort)) {
			t.Errorf("%s reaches %s at %s, want its public address on %d", n.Name, other.Name, p.Endpoint, infra.WireGuardPort)
		}
		if time.Since(p.Handshake) > handshakeFresh {
			t.Errorf("%s: last handshake with %s was %s ago", n.Name, other.Name, time.Since(p.Handshake).Round(time.Second))
		}
		return
	}
	t.Errorf("%s has no peer routing %s/32 (%s)", n.Name, otherWG, other.Name)
}

// TestMesh_overlayCarriesTraffic: every node reaches every other over the
// overlay (ICMP to the peer's 10.0.0.x through wg0).
func TestMesh_overlayCarriesTraffic(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		for _, other := range f.State.Nodes {
			if other.Name == n.Name {
				continue
			}
			wg := wgAddress(t, f, other)
			if out := f.Exec(t, n, "ping -c 2 -W 3 -I wg0 "+wg); out.Exit != 0 {
				t.Errorf("%s cannot reach %s at %s over wg0:\n%s", n.Name, other.Name, wg, out.Stdout)
			}
		}
	}
}

// TestMesh_unitOrdering: the overlay unit is not part of the supervisor (a
// node restart must not take wg0 down), and every unit that binds across
// the overlay is ordered after it (website/src/docs/contributor/architecture-reference.mdx "The overlay is not a
// child of the supervisor").
func TestMesh_unitOrdering(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		partOf := f.MustExec(t, n, "systemctl show -p PartOf --value "+infra.WireGuardUnit).Stdout
		if strings.Contains(partOf, infra.NodeUnit) {
			t.Errorf("%s: %s is PartOf %s", n.Name, infra.WireGuardUnit, infra.NodeUnit)
		}
		for _, u := range []string{infra.IndexRQLiteUnit, infra.IndexOlricUnit, infra.IndexGatewayUnit} {
			after := f.MustExec(t, n, "systemctl show -p After --value "+u).Stdout
			if !strings.Contains(after, infra.WireGuardUnit) {
				t.Errorf("%s: %s is not ordered after %s: %s", n.Name, u, infra.WireGuardUnit, after)
			}
		}
	}
}

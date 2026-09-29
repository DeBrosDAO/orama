//go:build e2e_fleet

package externalvantage

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// wsKey is a fixed Sec-WebSocket-Key (RFC 6455 4.1: 16 random bytes, base64);
// any well-formed value upgrades.
const wsKey = "dGhlIHNhbXBsZSBub25jZQ=="

// TestVantage_webSocketThroughCaddy: from outside, a pub/sub WebSocket to
// the namespace host upgrades (101) through Caddy's HTTP/1.1 front — the
// reason HTTP/2 is off (docs/ARCHITECTURE.md "HTTP/1.1 only", bug #249). The
// bearer travels in a header file on the probe, never on its command line.
func TestVantage_webSocketThroughCaddy(t *testing.T) {
	t.Parallel()
	f, ps := probes(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	host := tenancy.NamespaceHost(f, n.Name)
	for _, p := range ps {
		ca := caOnProbe(t, f, p)
		hdr := "/tmp/" + edge.RandomLabel(t, "e2e-ws-") + ".hdr"
		f.WriteFile(t, p, hdr, []byte("Authorization: Bearer "+n.Owner.Token()+"\n"), 0o600)
		cmd := fmt.Sprintf("curl -sS -i -N --http1.1 --max-time 5 --cacert %s -H @%s -H 'Connection: Upgrade' -H 'Upgrade: websocket' "+
			"-H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: %s' 'https://%s/v1/pubsub/ws?topic=e2e-vantage'", ca, hdr, wsKey, host)
		out := f.Exec(t, p, cmd)
		if !strings.HasPrefix(out.Stdout, "HTTP/1.1 101") || !strings.Contains(strings.ToLower(out.Stdout), "upgrade: websocket") {
			t.Errorf("%s: the upgrade answered %.300q (exit %d), want HTTP/1.1 101 Switching Protocols", p.Name, out.Stdout, out.Exit)
		}
	}
}

// scanPorts are TCP ports an attacker probes first: the edge ports and the
// internals that must never answer from the internet (Caddy admin 2019,
// libp2p 4001, the gateway 10104, rqlite 10100/10101, Olric 10102/10103,
// Kubo 10107, IPFS Cluster 10108/10110, Tor 9050, the chain P2P/RPC
// 31000/31001, OramaOS 9998/9999; docs/SECURITY.md "Network Isolation").
var scanPorts = []int{22, 53, 80, 443, 2019, 4001, 5001, 8080, 9050, 9998, 9999,
	10000, 10004, 10100, 10101, 10102, 10103, 10104, 10105, 10107, 10108, 10109, 10110, 31000, 31001}

// TestVantage_onlyDocumentedPortsOpenFromOutside: from another location,
// each node accepts TCP only on SSH, HTTP(S) and, on a nameserver, DNS
// (docs/ARCHITECTURE.md "UFW Firewall"). The runner's own scan is
// wireguard-firewall's; this one comes from a different network.
func TestVantage_onlyDocumentedPortsOpenFromOutside(t *testing.T) {
	t.Parallel()
	f, ps := probes(t)
	for _, p := range ps {
		requireTool(t, f, p, "timeout")
		for _, n := range f.State.Nodes {
			open := openPorts(t, f, p, n.PublicIP)
			for _, port := range open {
				if !(port == 22 || port == 80 || port == 443 || (port == 53 && n.Role == fleet.RoleNameserver)) {
					t.Errorf("%s -> %s: TCP %d answers from outside", p.Name, n.Name, port)
				}
			}
			for _, must := range []int{22, 80, 443} {
				if !slices.Contains(open, must) {
					t.Errorf("%s -> %s: TCP %d is closed", p.Name, n.Name, must)
				}
			}
		}
	}
}

// openPorts connects from p to every scan port of ip (3s each, bash
// /dev/tcp) and returns those that accepted.
func openPorts(t *testing.T, f *fleet.Fleet, p fleet.Node, ip string) []int {
	t.Helper()
	var list []string
	for _, port := range scanPorts {
		list = append(list, fmt.Sprint(port))
	}
	cmd := fmt.Sprintf("for p in %s; do timeout 3 bash -c \"</dev/tcp/%s/$p\" 2>/dev/null && echo $p; done", strings.Join(list, " "), ip)
	var open []int
	for _, s := range strings.Fields(f.Exec(t, p, cmd).Stdout) {
		var port int
		if _, err := fmt.Sscan(s, &port); err == nil {
			open = append(open, port)
		}
	}
	return open
}

// stunScript sends one STUN Binding request (RFC 5389) to argv[1]:3478 over
// UDP and prints the XOR-MAPPED-ADDRESS the server saw, or exits 1.
const stunScript = `import os,socket,struct,sys
tid=os.urandom(12);req=struct.pack(">HHI",1,0,0x2112A442)+tid
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(5);s.sendto(req,(sys.argv[1],3478))
r=s.recv(2048);typ,ln,cookie=struct.unpack(">HHI",r[:8])
if typ!=0x0101 or r[8:20]!=tid: sys.exit(1)
i=20
while i<20+ln:
    at,al=struct.unpack(">HH",r[i:i+4]);v=r[i+4:i+4+al]
    if at==0x0020:
        ip=struct.unpack(">I",v[4:8])[0]^0x2112A442;print(socket.inet_ntoa(struct.pack(">I",ip)));sys.exit(0)
    i+=4+al+((4-al%4)%4)
sys.exit(1)
`

// TestVantage_turnAnswersSTUNFromOutside: with WebRTC on, each TURN relay
// answers a STUN Binding on UDP 3478 from the internet with the probe's own
// public address (docs/ARCHITECTURE.md "WebRTC"; the relay port is open while
// the host relays).
func TestVantage_turnAnswersSTUNFromOutside(t *testing.T) {
	t.Parallel()
	f, ps := probes(t)
	n := tenancy.Namespace(t, f, ns.Options{Via: ns.ViaOperator})
	enableWebRTC(t, n)
	relays := relayIPs(t, f, n.Name)
	for _, p := range ps {
		for _, ip := range relays {
			eventually.Require(t, edge.PollEvery, relayBudget, p.Name+" -> relay "+ip+" STUN", func() (bool, error) {
				out := f.Exec(t, p, "python3 -c "+fleet.ShellQuote(stunScript)+" "+ip)
				if out.Exit != 0 {
					return false, fmt.Errorf("no STUN answer (exit %d)", out.Exit)
				}
				if got := strings.TrimSpace(out.Stdout); got != p.PublicIP {
					return false, eventually.Stop(fmt.Errorf("mapped address %q, want the probe's %s", got, p.PublicIP))
				}
				return true, nil
			})
		}
	}
}

// enableWebRTC turns WebRTC on for n (created ViaOperator) and off at cleanup.
func enableWebRTC(t *testing.T, n *ns.Namespace) {
	t.Helper()
	n.CLI.MustOK(t, "namespace", "enable", "webrtc", "--namespace", n.Name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if res, err := n.CLI.Run(ctx, "namespace", "disable", "webrtc", "--namespace", n.Name); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: disabling WebRTC on %s failed: %v %s", n.Name, err, res.Stderr)
		}
	})
}

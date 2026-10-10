//go:build e2e_fleet

package webrtc

import (
	"crypto/tls"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// waitPlaced waits until the SFU runs on every member and exactly two nodes'
// shared TURN list the namespace (website/src/docs/operator/webrtc-operations.mdx#turn-topology), and returns
// the TURN holders.
func waitPlaced(t *testing.T, fx *fixture) []fleet.Node {
	t.Helper()
	var holders []fleet.Node
	eventually.Require(t, pollEvery, readyBudget, "SFU on 3 nodes and TURN on 2", func() (bool, error) {
		for _, n := range fx.members {
			if s := fx.f.Unit(t, n, sfuUnit(fx.n.Name)); s != "active" {
				return false, fmt.Errorf("%s: sfu %s", n.Name, s)
			}
		}
		holders = turnHolders(t, fx.f, fx.n.Name)
		if len(holders) == turnNodes {
			return true, nil
		}
		return false, fmt.Errorf("%d TURN holders", len(holders))
	})
	return holders
}

// TestPlacement_sfuEverywhereWGOnlyTurnOnTwo: SFU on all three nodes bound to
// the WireGuard address only (signalling 30000-30099), TURN on two, the shared
// TURN unit active there with 3478 udp/tcp open, a 32-byte secret for the
// namespace in a 0600 turn.yaml, and both TURN DNS names pointing at exactly
// those two nodes (website/src/docs/operator/webrtc-operations.mdx).
func TestPlacement_sfuEverywhereWGOnlyTurnOnTwo(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	holders := waitPlaced(t, fx)
	for _, n := range fx.members {
		sfuBindsWGOnly(t, fx, n)
	}
	for _, n := range fx.f.State.Nodes {
		// The per-namespace TURN unit was retired for the shared one (bug-283;
		// core/pkg/namespace/host_turn.go stopLegacyPerNamespaceTURN).
		if s := fx.f.Unit(t, n, "orama-namespace-turn@"+fx.n.Name+".service"); s == "active" {
			t.Errorf("%s: the retired per-namespace TURN unit runs for %s", n.Name, fx.n.Name)
		}
	}
	var ips []string
	for _, n := range holders {
		ips = append(ips, n.PublicIP)
		turnHostFacts(t, fx, n)
	}
	slices.Sort(ips)
	for _, host := range []string{"turn.ns-" + fx.n.Name + "." + fx.f.State.BaseDomain, "turn-" + fx.n.Name + "." + fx.f.State.BaseDomain} {
		eventually.Require(t, pollEvery, readyBudget, host+" to name the TURN nodes", func() (bool, error) {
			got, err := tenancy.ResolveAt(t.Context(), tenancy.Nameservers(fx.f)[0].PublicIP, host)
			if slices.Equal(got, ips) {
				return true, nil
			}
			return false, fmt.Errorf("%v (%v), want %v", got, err, ips)
		})
	}
}

// sfuBindsWGOnly checks the sockets of this namespace's SFU process, found by
// the unit's main PID: other namespaces' SFUs run on the node at the same
// time, and a relay-only ICE agent opens 0.0.0.0 ephemeral UDP sockets that are
// not the signalling listeners.
func sfuBindsWGOnly(t *testing.T, fx *fixture, n fleet.Node) {
	t.Helper()
	f := fx.f
	pid := strings.TrimSpace(f.MustExec(t, n, "systemctl show -p MainPID --value "+sfuUnit(fx.n.Name)).Stdout)
	if pid == "" || pid == "0" {
		t.Fatalf("%s: %s has no main process", n.Name, sfuUnit(fx.n.Name))
	}
	out := f.Exec(t, n, "ss -H -ltnup | grep -F "+fleet.ShellQuote("pid="+pid+","))
	listeners, err := fleet.ParseSS(out.Stdout)
	if err != nil {
		t.Fatalf("failed to parse ss on %s: %v", n.Name, err)
	}
	signal := false
	for _, l := range listeners {
		if l.Addr != n.WGIP {
			t.Errorf("%s: the SFU listens on %s %s:%d, want the WireGuard address", n.Name, l.Proto, l.Addr, l.Port)
		}
		if l.Proto == "tcp" && l.Port >= sfuSignalLow && l.Port <= sfuSignalHigh {
			signal = true
		}
	}
	if !signal {
		t.Errorf("%s: no SFU signalling listener in %d-%d", n.Name, sfuSignalLow, sfuSignalHigh)
	}
}

func turnHostFacts(t *testing.T, fx *fixture, n fleet.Node) {
	t.Helper()
	if s := fx.f.Unit(t, n, turnUnit); s != "active" {
		t.Errorf("%s: %s is %q", n.Name, turnUnit, s)
	}
	if mode := strings.TrimSpace(fx.f.MustExec(t, n, "stat -c '%a %U' "+turnYAML).Stdout); mode != "600 orama" {
		t.Errorf("%s: turn.yaml is %q, want 600 orama", n.Name, mode)
	}
	script := `python3 -c 'import re,base64;t=open("` + turnYAML + `").read();` +
		`m=re.search(r"namespace:\s*\"?` + fx.n.Name + `\"?\s*\n\s*auth_secret:\s*\"?([A-Za-z0-9+/=]+)",t);` +
		`print(len(base64.b64decode(m.group(1))) if m else -1)'`
	if got := strings.TrimSpace(fx.f.MustExec(t, n, script).Stdout); got != strconv.Itoa(secretBytes) {
		t.Errorf("%s: the namespace's TURN secret is %s bytes, want %d", n.Name, got, secretBytes)
	}
	udp, tcp := false, false
	for _, l := range fx.f.Listeners(t, n) {
		if l.Port == turnPort {
			udp, tcp = udp || l.Proto == "udp", tcp || l.Proto == "tcp"
		}
	}
	fw := fx.f.Firewall(t, n)
	if !udp || !tcp || !fw.Allows("3478/udp") || !fw.Allows("3478/tcp") {
		t.Errorf("%s: TURN 3478 listening udp=%v tcp=%v, firewall %v", n.Name, udp, tcp, fw)
	}
}

// TestTURNS_wildcardCertOn5349: TURNS on 5349 presents the *.<base> wildcard
// for the single-label host turn-<ns>.<base>, verified against the run's
// pinned roots (website/src/docs/developer/webrtc.mdx#turns-tls-certificate).
func TestTURNS_wildcardCertOn5349(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	pool, err := gw.LoadCAPool(fx.f.State.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	host := "turn-" + fx.n.Name + "." + fx.f.State.BaseDomain
	for _, n := range waitPlaced(t, fx) {
		eventually.Require(t, pollEvery, readyBudget, "TURNS on "+n.Name, func() (bool, error) {
			d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: dialBudget}, Config: &tls.Config{ServerName: host, RootCAs: pool, MinVersion: tls.VersionTLS12}}
			conn, err := d.DialContext(t.Context(), "tcp", net.JoinHostPort(n.PublicIP, strconv.Itoa(turnsPort)))
			if err != nil {
				return false, err
			}
			return true, conn.Close()
		})
		if !fx.f.Firewall(t, n).Allows("5349/tcp") {
			t.Errorf("%s: 5349/tcp is not open", n.Name)
		}
	}
}

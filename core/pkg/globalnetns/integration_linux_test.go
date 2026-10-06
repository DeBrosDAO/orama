//go:build linux && netns_integration

// This test builds the real layout on the machine running it, so it needs
// root, ip and nft, and it is compiled only with -tags netns_integration
// (make test-netns). It never runs on darwin.
package globalnetns

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const dialTimeout = 1500 * time.Millisecond

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// buildLayout runs the commands RenderUnit lists, from its own rendering, so
// the test exercises what systemd would run. It returns the tear-down.
func buildLayout(t *testing.T, l Layout) {
	t.Helper()
	dir := t.TempDir()
	hostFile, nsFile := dir+"/host.nft", dir+"/ns.nft"
	if err := os.WriteFile(hostFile, []byte(l.RenderHostRules()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nsFile, []byte(l.RenderNSRules()), 0o600); err != nil {
		t.Fatal(err)
	}
	unit := strings.NewReplacer(HostRulesFile, hostFile, NSRulesFile, nsFile).Replace(l.RenderUnit())
	var stops []string
	for _, line := range strings.Split(unit, "\n") {
		switch {
		case strings.HasPrefix(line, "ExecStartPre=-"):
			exec.Command("sh", "-c", strings.TrimPrefix(line, "ExecStartPre=-")).Run() // a clean machine has nothing to remove
		case strings.HasPrefix(line, "ExecStart="):
			cmd := strings.TrimPrefix(line, "ExecStart=")
			if strings.Contains(cmd, "sysctl") {
				continue // do not change the CI machine's forwarding
			}
			run(t, "sh", "-c", cmd)
		case strings.HasPrefix(line, "ExecStop=-"):
			stops = append(stops, strings.TrimPrefix(line, "ExecStop=-"))
		}
	}
	t.Cleanup(func() {
		for _, c := range stops {
			exec.Command("sh", "-c", c).Run() // best effort: the namespace may already be gone
		}
	})
}

func integrationLayout(t *testing.T) Layout {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	tools, err := Preflight(Host{
		GOOS: "linux",
		Run: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		LookPath: exec.LookPath,
		Exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
	})
	if err != nil {
		t.Skipf("machine cannot host the layout: %v", err)
	}
	return Layout{Ports: []Port{{"tcp", 34567}}, Tools: tools}
}

func listenIn(t *testing.T, inNS bool, addr string) net.Listener {
	t.Helper()
	var ln net.Listener
	listen := func() (err error) { ln, err = net.Listen("tcp", addr); return err }
	var err error
	if inNS {
		err = InNamespace(Path, listen)
	} else {
		err = listen()
	}
	if err != nil {
		t.Fatalf("listen %s (in namespace: %v): %v", addr, inNS, err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln
}

func dialFrom(inNS bool, addr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	dial := func() error {
		c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err == nil {
			c.Close()
		}
		return err
	}
	if inNS {
		return InNamespace(Path, dial)
	}
	return dial()
}

// The namespace has its own port space and its own loopback: the same address
// binds twice, and neither side reaches the other's listener.
func TestLayout_portSpaceAndLoopbackAreSeparate(t *testing.T) {
	l := integrationLayout(t)
	buildLayout(t, l)

	const loopback = "127.0.0.1:34901"
	listenIn(t, false, loopback)
	listenIn(t, true, loopback) // the same address, bound again inside

	if err := dialFrom(true, loopback); err != nil {
		t.Errorf("inside the namespace, its own loopback service is unreachable: %v", err)
	}
	if err := dialFrom(false, loopback); err != nil {
		t.Errorf("in the root namespace, the cluster's loopback service is unreachable: %v", err)
	}
}

// A cluster service on the root loopback, on the host's WireGuard-style
// address, and on the veth's host address is unreachable from inside.
func TestLayout_clusterPortsAreUnreachableFromInside(t *testing.T) {
	l := integrationLayout(t)
	buildLayout(t, l)

	listenIn(t, false, "0.0.0.0:34902") // every root-namespace address, including 198.18.0.1
	for _, addr := range []string{"127.0.0.1:34902", HostAddr + ":34902"} {
		if err := dialFrom(true, addr); err == nil {
			t.Errorf("a global unit reached the cluster's %s", addr)
		}
	}
}

// A namespace listener is reachable only on a published port.
func TestLayout_onlyPublishedPortsAreReachable(t *testing.T) {
	l := integrationLayout(t)
	buildLayout(t, l)

	listenIn(t, true, "0.0.0.0:34567") // published
	listenIn(t, true, "0.0.0.0:34568") // not published
	if err := dialFrom(false, NSAddr+":34567"); err != nil {
		t.Errorf("the published port is unreachable: %v", err)
	}
	if err := dialFrom(false, NSAddr+":34568"); err == nil {
		t.Errorf("a port that was not published is reachable from the root namespace")
	}
}

func TestLayout_privateNetworksAreUnreachableFromInside(t *testing.T) {
	l := integrationLayout(t)
	buildLayout(t, l)
	for _, addr := range []string{"10.0.0.1:22", "192.168.0.1:22", "169.254.169.254:80", "172.16.0.1:22"} {
		if err := dialFrom(true, addr); err == nil {
			t.Errorf("a global unit connected to %s", addr)
		}
	}
}

func TestLayout_rulesetsReloadOverARunningLayout(t *testing.T) {
	l := integrationLayout(t)
	buildLayout(t, l)

	// The install loads the rewritten rulesets over a running layout on a re-install: one
	// transaction per table, the rules present afterwards.
	dir := t.TempDir()
	hostFile, nsFile := dir+"/host.nft", dir+"/ns.nft"
	if err := os.WriteFile(hostFile, []byte(l.RenderHostRules()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nsFile, []byte(l.RenderNSRules()), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, l.Tools.Nft, "-f", hostFile)
	run(t, l.Tools.IP, "netns", "exec", Name, l.Tools.Nft, "-f", nsFile)
	run(t, l.Tools.Nft, "list", "table", "ip", hostTable)
	run(t, l.Tools.IP, "netns", "exec", Name, l.Tools.Nft, "list", "table", "ip", nsTable)
}

// The chain's RPC listens on the namespace address for the host alone: the
// host reaches it, a source that is not the host's veth address does not, and
// it is not published (no DNAT).
func TestLayout_hostOnlyPortsAreReachableFromTheHostAlone(t *testing.T) {
	l := integrationLayout(t)
	l.HostPorts = []int{34569}
	l.HostClientUIDs = []int{65534}
	buildLayout(t, l)

	listenIn(t, true, NSAddr+":34569")
	if err := dialFrom(false, NSAddr+":34569"); err != nil {
		t.Errorf("the host cannot reach the namespace's host-only port: %v", err)
	}
	// A source that is not the host's veth address: the loopback address, which the kernel will not
	// route to the namespace at all. A genuinely foreign source cannot be made on one machine; the
	// rule that refuses it is asserted where the ruleset is rendered (TestRenderNSRules_hostOnlyPorts...).
	d := &net.Dialer{Timeout: dialTimeout, LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}}
	if c, err := d.Dial("tcp", NSAddr+":34569"); err == nil {
		c.Close()
		t.Errorf("a source other than the host's veth address reached the host-only port")
	}
	// A port the layout does not list stays closed to the host, as before.
	listenIn(t, true, NSAddr+":34570")
	if err := dialFrom(false, NSAddr+":34570"); err == nil {
		t.Errorf("an unlisted namespace port is reachable from the host")
	}
}

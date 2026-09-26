package install

import (
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func addrs(t *testing.T, s ...string) []netip.Addr {
	t.Helper()
	out := make([]netip.Addr, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddr(a)
	}
	return out
}

// F4: a build reached the node's own public address over lo, where UFW accepts
// everything. The drop-in denies it, and every other public address the host
// holds — a second or floating IP, an IPv6 address.
func TestHostPublicAddrs(t *testing.T) {
	got, err := hostPublicAddrs("203.0.113.7", addrs(t,
		"127.0.0.1", "::1", // loopback: the template denies it
		"10.0.0.7",                  // the overlay: denied already
		"192.168.1.5", "172.17.0.1", // private: denied already
		"100.64.3.2",    // CGNAT: denied already
		"fe80::1",       // link-local: denied already
		"198.51.100.9",  // a second public address
		"2001:db8:1::7", // a public IPv6 address
		"203.0.113.7",   // the recorded one, again
	))
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, a := range got {
		s = append(s, a.String())
	}
	if strings.Join(s, " ") != "198.51.100.9 203.0.113.7 2001:db8:1::7" {
		t.Errorf("denied %v", s)
	}
}

// The recorded public_ip is denied even when it is not on an interface (a
// floating IP routed to the host), and a malformed one is an error.
func TestHostPublicAddrs_recordedAddress(t *testing.T) {
	got, err := hostPublicAddrs("203.0.113.7", nil)
	if err != nil || len(got) != 1 || got[0].String() != "203.0.113.7" {
		t.Errorf("got %v, %v", got, err)
	}
	if _, err := hostPublicAddrs("not-an-ip", nil); err == nil {
		t.Error("a malformed public_ip was accepted")
	}
}

func TestBuildSandboxDropIn(t *testing.T) {
	d := buildSandboxDropIn(addrs(t, "203.0.113.7", "2001:db8:1::7"))
	if !strings.Contains(d, "[Service]\nIPAddressDeny=203.0.113.7/32 2001:db8:1::7/128\n") {
		t.Errorf("drop-in:\n%s", d)
	}
	if strings.Contains(buildSandboxDropIn(nil), "IPAddressDeny") {
		t.Error("an empty deny line would reset the template's list")
	}
}

// The build cannot reach the loopback stub resolver, so it gets public ones —
// and nothing that sends short names to the cluster's own zone.
func TestBuildResolvConf(t *testing.T) {
	conf := buildResolvConf()
	var servers []string
	for _, line := range strings.Split(conf, "\n") {
		switch {
		case strings.HasPrefix(line, "nameserver "):
			ip := netip.MustParseAddr(strings.TrimPrefix(line, "nameserver "))
			if ip.IsLoopback() || ip.IsPrivate() || cgnat.Contains(ip) {
				t.Errorf("%s is a resolver the build sandbox denies", ip)
			}
			servers = append(servers, ip.String())
		case strings.HasPrefix(line, "search"), strings.HasPrefix(line, "domain"), strings.HasPrefix(line, "options"):
			t.Errorf("unexpected line %q", line)
		}
	}
	if len(servers) < 2 {
		t.Errorf("resolvers %v: one resolver down would stop every build", servers)
	}
}

// The file the drop-in and the resolver configuration are written to is the
// one the build template binds.
func TestBuildUnit_bindsTheResolverFileInstallWrites(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "systemd", "orama-deploy-build@.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "BindReadOnlyPaths="+BuildResolvConfPath+":/etc/resolv.conf") {
		t.Errorf("orama-deploy-build@.service does not bind %s over /etc/resolv.conf", BuildResolvConfPath)
	}
	if filepath.Base(buildUnitDropInDir) != "orama-deploy-build@.service.d" {
		t.Errorf("the drop-in is not the build template's: %s", buildUnitDropInDir)
	}
}

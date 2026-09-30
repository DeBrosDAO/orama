//go:build e2e_fleet

package dnstls

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	corefilePath = "/etc/coredns/Corefile"
	corednsUser  = "orama-coredns"
	// stagingCADir is the directory Caddy names after the staging ACME
	// directory it obtained the certificates from.
	stagingCADir = "acme-staging-v02.api.letsencrypt.org-directory"
	dnsPort      = 53
)

// TestCoreDNS_isolatedAccountAndEmptyOptOrama: CoreDNS runs as
// orama-coredns, its Corefile (which holds the index rqlite password) is
// root:orama-coredns 0640, and /opt/orama is an empty read-only tmpfs in its
// mount namespace (docs/SECURITY.md "Per-service accounts").
func TestCoreDNS_isolatedAccountAndEmptyOptOrama(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range edge.Nameservers(f) {
		pid := edge.MainPID(t, f, n, edge.CoreDNSUnit)
		if u := edge.ProcessUser(t, f, n, pid); u != corednsUser {
			t.Errorf("%s: CoreDNS runs as %q, want %s", n.Name, u, corednsUser)
		}
		out := f.MustExec(t, n, fmt.Sprintf("ls -A /proc/%d/root/opt/orama | wc -l; findmnt -N %d -no FSTYPE,OPTIONS /opt/orama", pid, pid))
		lines := strings.Split(strings.TrimSpace(out.Stdout), "\n")
		if len(lines) < 2 || strings.TrimSpace(lines[0]) != "0" || !strings.HasPrefix(lines[1], "tmpfs") || !strings.Contains(lines[1], "ro") {
			t.Errorf("%s: CoreDNS sees /opt/orama as %q, want an empty read-only tmpfs", n.Name, out.Stdout)
		}
		infra.RequireStat(t, f, n, corefilePath, "root", corednsUser, "640")
	}
}

// TestCoreDNS_port53OnlyOnNameservers: a nameserver listens on 53/tcp and
// 53/udp and its firewall opens both; a node without the role does neither
// (docs/ARCHITECTURE.md "UFW Firewall": 53 on nameservers only).
func TestCoreDNS_port53OnlyOnNameservers(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	isNS := map[string]bool{}
	for _, n := range edge.Nameservers(f) {
		isNS[n.Name] = true
	}
	for _, n := range f.State.Nodes {
		tcp, udp := false, false
		for _, l := range f.Listeners(t, n) {
			if l.Port == dnsPort && l.Public() {
				tcp, udp = tcp || strings.HasPrefix(l.Proto, "tcp"), udp || strings.HasPrefix(l.Proto, "udp")
			}
		}
		fw := f.Firewall(t, n)
		open := fw.Allows("53/tcp") && fw.Allows("53/udp")
		if isNS[n.Name] && !(tcp && udp && open) {
			t.Errorf("%s (nameserver): 53 tcp %v udp %v firewall %v, want all three", n.Name, tcp, udp, open)
		}
		if !isNS[n.Name] && (tcp || udp || fw.Allows("53/tcp") || fw.Allows("53/udp")) {
			t.Errorf("%s (no nameserver role): port 53 is served or opened", n.Name)
		}
	}
}

// TestCaddy_certificateFilesAndKeys: every node keeps the staging-issued
// base and wildcard certificates under Caddy's data dir, private keys 0600
// owned by orama; the ACME challenge key is root:orama 0640 and the admin API
// is a 0600 unix socket in a 0700 directory (docs/SECURITY.md "ACME DNS-01",
// "Local control planes"; core/systemd/orama-namespace-caddy@.service).
func TestCaddy_certificateFilesAndKeys(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	base := f.State.BaseDomain
	for _, n := range f.State.Nodes {
		dir := edge.CaddyDataDir + "/caddy/certificates/" + stagingCADir
		for _, name := range []string{base, "wildcard_." + base} {
			infra.RequireStat(t, f, n, dir+"/"+name+"/"+name+".crt", "orama", "orama", "600")
			infra.RequireStat(t, f, n, dir+"/"+name+"/"+name+".key", "orama", "orama", "600")
		}
		infra.RequireStat(t, f, n, edge.ACMEKeyPath, "root", "orama", "640")
		infra.RequireStat(t, f, n, "/run/orama-caddy", "orama", "orama", "700")
		st, ok := infra.StatFile(t, f, n, edge.CaddyAdminSocket)
		if !ok || st.Mode != "600" || st.Owner != "orama" || st.Type != "socket" {
			t.Errorf("%s: Caddy admin socket is %+v (exists %v), want an orama 0600 socket", n.Name, st, ok)
		}
		if l := f.Listeners(t, n); hasPort(l, 2019) {
			t.Errorf("%s: something listens on 2019, Caddy's default admin port", n.Name)
		}
	}
}

func hasPort(ls []fleet.Listener, port int) bool {
	for _, l := range ls {
		if l.Port == port {
			return true
		}
	}
	return false
}

// TestHTTP_plainPortServesTheGatewayWithoutRedirect records what port 80 does
// today: Caddy serves the gateway over plain HTTP, by name and by bare IP, so
// a node is reachable before its certificate exists (docs/ARCHITECTURE.md
// "Production": "use the IP over HTTP port 80"; core/pkg/install/installers
// caddy.go http:// blocks). There is no redirect to HTTPS, and no HSTS over
// cleartext. Nothing credential-bearing is sent here.
func TestHTTP_plainPortServesTheGatewayWithoutRedirect(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		for _, host := range []string{f.State.BaseDomain, n.PublicIP} {
			resp := plainGet(t, n.PublicIP, host, "/health")
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s: http://%s/health answered %d, want 200 from the gateway", n.Name, host, resp.StatusCode)
			}
			if loc := resp.Header.Get("Location"); loc != "" {
				t.Errorf("%s: http://%s/health redirects to %s", n.Name, host, loc)
			}
			if hsts := resp.Header.Get("Strict-Transport-Security"); hsts != "" {
				t.Errorf("%s: HSTS %q sent over cleartext", n.Name, hsts)
			}
		}
	}
}

// plainGet sends one cleartext GET to ip:80 with the given Host header.
func plainGet(t *testing.T, ip, host, path string) *http.Response {
	t.Helper()
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: handshakeBudget}).DialContext(ctx, network, net.JoinHostPort(ip, "80"))
	}
	c := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DialContext: dial},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("plain HTTP to %s (Host %s): %v", ip, host, err)
	}
	resp.Body.Close()
	return resp
}

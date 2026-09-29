//go:build e2e_fleet

package anontor

import (
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestAnon_fetchesThroughTor: a signed-in user fetches a public page through
// Tor; the answer carries the destination's status and body
// (docs/ARCHITECTURE.md: POST /v1/proxy/anon).
func TestAnon_fetchesThroughTor(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	eventually.Require(t, pollEvery(), torBudget, "a fetch through Tor", func() (bool, error) {
		r, res := anon(t, n.Client, tenancy.Owner(n), "https://"+publicTarget+"/", "GET")
		if r.Status != http.StatusOK {
			return false, eventually.Stop(errorf("proxy answered %d %s", r.Status, res.Error))
		}
		return res.StatusCode == http.StatusOK && strings.Contains(body(t, res), "Example Domain"), nil
	})
}

// TestAnon_destinationMatrix: literal loopback, private and link-local
// destinations are DESTINATION_NOT_ALLOWED (403) before Tor is asked; other
// internal forms Tor itself refuses (ClientRejectInternalAddresses) — never
// a 200 carrying a node's own service.
func TestAnon_destinationMatrix(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	owner := tenancy.Owner(n)
	for _, u := range []string{"http://127.0.0.1:10104/v1/health", "http://localhost/", "http://10.0.0.1:10104/",
		"http://" + f.State.Nodes[0].WGIP + ":10104/", "http://192.168.0.1/", "http://172.16.0.1/",
		"http://169.254.169.254/latest/meta-data/", "http://[::1]:10104/", "http://[fe80::1]/"} {
		r, _ := anon(t, n.Client, owner, u, "GET")
		if r.Status != http.StatusForbidden || r.ErrorCode() != codeNotAllowed {
			t.Errorf("%s: want 403 %s, got %d %.200s", u, codeNotAllowed, r.Status, r.Body)
		}
	}
	for _, u := range []string{"http://100.64.0.1/", "http://0.0.0.0:10104/", "http://2130706433:10104/", "http://localtest.me:10104/",
		"http://[::ffff:127.0.0.1]:10104/"} {
		if r, res := anon(t, n.Client, owner, u, "GET"); r.Status == http.StatusOK && res.StatusCode != 0 {
			t.Errorf("%s reached a destination (status %d)", u, res.StatusCode)
		}
	}
	for name, u := range map[string]string{"ftp scheme": "ftp://" + publicTarget + "/", "file scheme": "file:///etc/passwd", "not a URL": "%zz"} {
		if r, _ := anon(t, n.Client, owner, u, "GET"); r.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, r.Status)
		}
	}
	if r, _ := anon(t, n.Client, owner, "https://"+publicTarget+"/", "CONNECT"); r.Status != http.StatusBadRequest {
		t.Errorf("method CONNECT: want 400, got %d", r.Status)
	}
	if r := tenancy.Get(t, n.Client, pathAnon, owner); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("GET /v1/proxy/anon: want 405, got %d", r.Status)
	}
}

// TestAnon_needsUserAndProxyGrant: a key alone (even app-runtime, which
// holds proxy) is 401 USER_JWT_REQUIRED; a reader has no proxy grant (403);
// nobody is 401 (docs/API_SURFACE.md; route_policy.go dataPlane proxy).
func TestAnon_needsUserAndProxyGrant(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	target := "https://" + publicTarget + "/"
	r, _ := anon(t, n.Client, tenancy.Cred{APIKey: tenancy.APIKey(t, n, "app-runtime")}, target, "GET")
	if r.Status != http.StatusUnauthorized || r.ErrorCode() != codeJWTNeeded {
		t.Errorf("app-runtime key: want 401 %s, got %d %s", codeJWTNeeded, r.Status, r.ErrorCode())
	}
	r, _ = anon(t, n.Client, tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleReader).Token()}, target, "GET")
	tenancy.ExpectRefused(t, r, http.StatusForbidden, tenancy.CodeScope)
	r, _ = anon(t, n.Client, tenancy.Cred{}, target, "GET")
	tenancy.ExpectRefused(t, r, http.StatusUnauthorized, tenancy.CodeMissing)
	if r, _ := anon(t, n.Client, tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()}, target, "HEAD"); r.Status != http.StatusOK {
		t.Errorf("a runtime member: %d %.200s", r.Status, r.Body)
	}
}

// TestTunnel_endToEndTLS: the tunnel carries a raw TCP stream; the client's
// own TLS handshake with the destination, verified against the system
// roots, runs through it and an HTTP/1.1 request returns 200 — the gateway
// sees ciphertext only (anon_tunnel_handler.go).
func TestTunnel_endToEndTLS(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	conn, status, msg := openTunnel(t, n.Client, n.Owner.Token(), publicTarget, "443")
	if conn == nil {
		t.Fatalf("opening the tunnel: %d %s", status, msg)
	}
	tc := tls.Client(conn, &tls.Config{ServerName: publicTarget, MinVersion: tls.VersionTLS12})
	if err := tc.SetDeadline(deadline()); err != nil {
		t.Fatal(err)
	}
	if err := tc.Handshake(); err != nil {
		t.Fatalf("TLS through the tunnel: %v", err)
	}
	if _, err := io.WriteString(tc, "GET / HTTP/1.1\r\nHost: "+publicTarget+"\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(tc)
	if err != nil && len(reply) == 0 {
		t.Fatalf("reading through the tunnel: %v", err)
	}
	if !strings.HasPrefix(string(reply), "HTTP/1.1 200") {
		t.Errorf("reply through the tunnel: %.200s", reply)
	}
}

// TestTunnel_refusals: only ports 80/443, no literal internal address or
// localhost, a host is required, a WebSocket upgrade is required, and only a
// signed-in user with the proxy grant may open one.
func TestTunnel_refusals(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	tok := n.Owner.Token()
	for name, hp := range map[string][2]string{
		"port 25": {publicTarget, "25"}, "port 8080": {publicTarget, "8080"}, "port text": {publicTarget, "https"},
		"loopback": {"127.0.0.1", "443"}, "overlay": {f.State.Nodes[0].WGIP, "443"}, "localhost": {"localhost", "80"},
		"sub.localhost": {"x.localhost", "80"}, "ipv6 loopback": {"[::1]", "443"}, "no host": {"", "443"},
		"host with path": {publicTarget + "/x", "443"}, "userinfo": {"a@" + publicTarget, "443"},
		"too long": {strings.Repeat("a", 254), "443"},
	} {
		if conn, status, _ := openTunnel(t, n.Client, tok, hp[0], hp[1]); conn != nil || status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, status)
		}
	}
	key := tenancy.APIKey(t, n, "app-runtime")
	if conn, _, _ := openTunnel(t, n.Client, "", publicTarget, "443"); conn != nil {
		t.Error("an anonymous tunnel opened")
	}
	if r := n.Client.MustSend(t, upgradeReq(tunnelPath(publicTarget, "443"), key)); r.Status != http.StatusUnauthorized {
		t.Errorf("a tunnel with an app-runtime key: want 401, got %d", r.Status)
	}
	if r := tenancy.Get(t, n.Client, tunnelPath(publicTarget, "443"), tenancy.Owner(n)); r.Status != http.StatusBadRequest {
		t.Errorf("a plain GET without the upgrade: want 400, got %d", r.Status)
	}
}

// TestAnon_exitIsTorForEveryUser: two users of one namespace both leave
// through Tor exits (check.torproject.org says IsTor); the SOCKS port
// isolates circuits per credential (IsolateSOCKSAuth, checked on the node by
// TestTor_unitHardening), so their streams do not share a circuit.
func TestAnon_exitIsTorForEveryUser(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for _, who := range []tenancy.Cred{tenancy.Owner(n), {Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()}} {
		eventually.Require(t, pollEvery(), torBudget, "a Tor exit check", func() (bool, error) {
			r, res := anon(t, n.Client, who, "https://check.torproject.org/api/ip", "GET")
			if r.Status != http.StatusOK {
				return false, errorf("proxy answered %d %s", r.Status, res.Error)
			}
			return strings.Contains(strings.ReplaceAll(body(t, res), " ", ""), `"IsTor":true`), nil
		})
	}
}

package clientkey

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testProof = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func req(peer, xff, realIP string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = peer
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	if realIP != "" {
		r.Header.Set("X-Real-IP", realIP)
	}
	return r
}

// viaCaddy is r after the gateway's first middleware has seen it carry the proof: what a request
// forwarded by the node's Caddy looks like to everything below.
func viaCaddy(t *testing.T, r *http.Request) *http.Request {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proof")
	if err := os.WriteFile(path, []byte(testProof+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.Header.Set(ProofHeader, testProof)
	var seen *http.Request
	NewProver(path, nil).Middleware(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) { seen = got })).
		ServeHTTP(httptest.NewRecorder(), r)
	if seen == nil {
		t.Fatal("the middleware did not call the next handler")
	}
	return seen
}

// Attribution (log, audit, affinity, the XFF handed on) trusts the last entry from the local proxy
// when it proved itself and from a gateway on the WireGuard mesh, and nothing from any other peer.
func TestAttribute(t *testing.T) {
	cases := []struct {
		name string
		r    *http.Request
		want string
	}{
		{"a mesh peer's forwarded client", req("10.0.0.2:5000", "203.0.113.9", ""), "203.0.113.9"},
		{"a mesh peer's spoofed first entry", req("10.0.0.2:5000", "6.6.6.6, 203.0.113.9", ""), "203.0.113.9"},
		{"a mesh peer with no header is itself", req("10.0.0.2:5000", "", ""), "10.0.0.2"},
		{"a mesh peer's unparseable entry falls back to the peer", req("10.0.0.2:5000", "not-an-ip", ""), "10.0.0.2"},
		{"the local proxy's last entry, with its proof", viaCaddy(t, req("127.0.0.1:5000", "6.6.6.6, 203.0.113.9", "")), "203.0.113.9"},
		{"the local proxy over IPv6 loopback, with its proof", viaCaddy(t, req("[::1]:5000", "203.0.113.9", "")), "203.0.113.9"},
		{"a loopback caller with no proof is itself, whatever it forwards", req("127.0.0.1:5000", "6.6.6.6, 203.0.113.9", ""), "127.0.0.1"},
		{"a proven request with an unparseable entry is the peer", viaCaddy(t, req("127.0.0.1:5000", "not-an-ip", "")), "127.0.0.1"},
		{"a private peer off the mesh is the client", req("192.168.1.7:5000", "203.0.113.9", "203.0.113.10"), "192.168.1.7"},
		{"another 10.x peer off the mesh subnet is the client", req("10.1.2.3:5000", "203.0.113.9", ""), "10.1.2.3"},
		{"a public peer is the client", req("198.51.100.7:5000", "203.0.113.9", "203.0.113.10"), "198.51.100.7"},
		{"a public peer with the proof marker is still itself", viaCaddy(t, req("198.51.100.7:5000", "203.0.113.9", "")), "198.51.100.7"},
		{"X-Real-IP is never used", viaCaddy(t, req("127.0.0.1:5000", "", "203.0.113.10")), "127.0.0.1"},
	}
	for _, tc := range cases {
		if got := Attribute(tc.r); got != tc.want {
			t.Errorf("%s: Attribute = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The rate-limit resolution keeps its own rules: a mesh peer is exempt and is itself, whatever it
// forwards, and an internal caller is never keyed by a header it wrote.
func TestResolve_keepsItsRateLimitSemantics(t *testing.T) {
	cases := []struct {
		name       string
		r          *http.Request
		wantClient string
		wantExempt bool
	}{
		{"mesh peer", req("10.0.0.2:5000", "203.0.113.9", ""), "10.0.0.2", true},
		{"proxied with the proof", viaCaddy(t, req("127.0.0.1:5000", "6.6.6.6, 203.0.113.9", "")), "203.0.113.9", false},
		{"two proven callers are two clients", viaCaddy(t, req("127.0.0.1:5000", "203.0.113.10", "")), "203.0.113.10", false},
		{"forwarded by a loopback caller with no proof", req("127.0.0.1:5000", "6.6.6.6, 203.0.113.9", ""), "127.0.0.1", false},
		{"proven with no address to name is not a local process", viaCaddy(t, req("127.0.0.1:5000", "", "")), "127.0.0.1", false},
		{"proven with an unparseable address is not a local process", viaCaddy(t, req("127.0.0.1:5000", "garbage", "")), "127.0.0.1", false},
		{"a process on the node, nothing forwarded", req("127.0.0.1:5000", "", ""), "127.0.0.1", true},
		{"direct", req("198.51.100.7:5000", "203.0.113.9", ""), "198.51.100.7", false},
	}
	for _, tc := range cases {
		client, exempt := Resolve(tc.r)
		if client != tc.wantClient || exempt != tc.wantExempt {
			t.Errorf("%s: Resolve = %q exempt=%v, want %q exempt=%v", tc.name, client, exempt, tc.wantClient, tc.wantExempt)
		}
	}
}

func TestPublic_isTheProvenClientElseTheConnectionsAddress(t *testing.T) {
	for _, remote := range []string{"127.0.0.1:1", "10.0.0.2:1", "198.51.100.7:1", "[2001:db8::1]:1"} {
		r := httptest.NewRequest(http.MethodPost, "/v1/chain/faucet", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", "203.0.113.9, 203.0.113.10")
		r.Header.Set("X-Real-IP", "203.0.113.11")
		want := strings.TrimSuffix(strings.TrimPrefix(remote[:strings.LastIndex(remote, ":")], "["), "]")
		if got := Public(r); got != want {
			t.Errorf("Public(%s) = %q, want %q", remote, got, want)
		}
	}

	proven := viaCaddy(t, req("127.0.0.1:1", "6.6.6.6, 203.0.113.9", ""))
	if got := Public(proven); got != "203.0.113.9" {
		t.Errorf("Public(proven) = %q, want the address Caddy appended", got)
	}
	if got := Public(viaCaddy(t, req("127.0.0.1:1", "", ""))); got != "127.0.0.1" {
		t.Errorf("Public(proven, no address) = %q, want the peer", got)
	}
	// The marker means nothing from a peer that is not the local proxy.
	if got := Public(viaCaddy(t, req("198.51.100.7:1", "203.0.113.9", ""))); got != "198.51.100.7" {
		t.Errorf("Public(proven, off-node peer) = %q, want the peer", got)
	}
}

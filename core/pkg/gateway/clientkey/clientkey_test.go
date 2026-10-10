package clientkey

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

// Attribution (log, audit, affinity, the XFF handed on) trusts the last entry from the local proxy
// and from a gateway on the WireGuard mesh, and nothing from any other peer.
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
		{"the local proxy's last entry", req("127.0.0.1:5000", "6.6.6.6, 203.0.113.9", ""), "203.0.113.9"},
		{"a private peer off the mesh is the client", req("192.168.1.7:5000", "203.0.113.9", "203.0.113.10"), "192.168.1.7"},
		{"another 10.x peer off the mesh subnet is the client", req("10.1.2.3:5000", "203.0.113.9", ""), "10.1.2.3"},
		{"a public peer is the client", req("198.51.100.7:5000", "203.0.113.9", "203.0.113.10"), "198.51.100.7"},
		{"X-Real-IP is never used", req("127.0.0.1:5000", "", "203.0.113.10"), "127.0.0.1"},
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
	client, exempt := Resolve(req("10.0.0.2:5000", "203.0.113.9", ""))
	if client != "10.0.0.2" || !exempt {
		t.Errorf("mesh peer: %q exempt=%v, want the peer, exempt", client, exempt)
	}
	client, exempt = Resolve(req("127.0.0.1:5000", "6.6.6.6, 203.0.113.9", ""))
	if client != "203.0.113.9" || exempt {
		t.Errorf("proxied: %q exempt=%v, want the last entry, not exempt", client, exempt)
	}
	client, exempt = Resolve(req("198.51.100.7:5000", "203.0.113.9", ""))
	if client != "198.51.100.7" || exempt {
		t.Errorf("direct: %q exempt=%v", client, exempt)
	}
}

func TestPeer_isTheConnectionsAddressWhateverTheHeadersSay(t *testing.T) {
	for _, remote := range []string{"127.0.0.1:1", "10.0.0.2:1", "198.51.100.7:1", "[2001:db8::1]:1"} {
		r := httptest.NewRequest(http.MethodPost, "/v1/chain/faucet", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", "203.0.113.9, 203.0.113.10")
		r.Header.Set("X-Real-IP", "203.0.113.11")
		want := strings.TrimSuffix(strings.TrimPrefix(remote[:strings.LastIndex(remote, ":")], "["), "]")
		if got := Peer(r); got != want {
			t.Errorf("Peer(%s) = %q, want %q", remote, got, want)
		}
	}
}

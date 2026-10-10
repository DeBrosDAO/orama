//go:build e2e_fleet

package networkroutes

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// hugeBody is past anything a multiaddr or a peer id could be.
const hugeBody = 2 << 20

// overlayPrefix is an address on the WireGuard mesh (infra.WireGuardSubnet);
// loopbackPrefix one that only reaches the dialling host.
const (
	overlayPrefix  = "/ip4/10.0.0."
	loopbackPrefix = "/ip4/127."
)

// is4xx reports a client-error status.
func is4xx(status int) bool { return status >= 400 && status < 500 }

// badBodies are request bodies no operator could mean, for field: each is
// the caller's mistake and owes a 4xx, never a 5xx.
func badBodies(field string) map[string][]byte {
	return map[string][]byte{
		"malformed JSON":  []byte(`{"` + field + `":`),
		"wrong type":      []byte(`{"` + field + `":5}`),
		"missing field":   []byte(`{}`),
		"empty value":     []byte(`{"` + field + `":""}`),
		"garbage value":   []byte(`{"` + field + `":"not a multiaddr or a peer id"}`),
		"unicode and NUL": []byte(`{"` + field + `":"/ip4/\u202e1.2.3.4\u0000/tcp/1"}`),
		"huge value":      []byte(`{"` + field + `":"/` + strings.Repeat("a", hugeBody) + `"}`),
	}
}

// networkConnectMalformedInputIs4xx: an operator's malformed, mistyped,
// garbage and oversized bodies to connect and disconnect are refused as the
// caller's mistake (4xx), and the wrong method is 405 — never a 2xx and never
// a 500 carrying the gateway client's internal error text.
func networkConnectMalformedInputIs4xx(t *testing.T, opNS *ns.Namespace) {
	op := tenancy.Owner(opNS)
	c := harness.GW(t)
	for path, field := range map[string]string{pathConnect: "multiaddr", pathDisconnect: "peer_id"} {
		for what, body := range badBodies(field) {
			if r := call(t, c, path, op, body, nil); !is4xx(r.Status) {
				t.Errorf("%s with a %s: HTTP %d, want 4xx: %.300s", path, what, r.Status, r.Body)
			}
		}
		r := c.MustSend(t, gw.Req{Method: http.MethodGet, Path: path, Bearer: op.Bearer})
		if r.Status != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: HTTP %d, want 405: %.300s", path, r.Status, r.Body)
		}
	}
	addr := "/ip4/192.0.2.1/tcp/4001"
	if r := call(t, c, pathConnect, op, jsonBody(t, map[string]string{"multiaddr": addr}), nil); !is4xx(r.Status) {
		t.Errorf("connect to %s, which names no peer: HTTP %d, want 4xx: %.300s", addr, r.Status, r.Body)
	}
}

// hostileTargets are dial targets the gateway's host must not report as
// connected: loopback, private ranges and the overlay, each under a peer id
// nobody holds, so no dial can complete a handshake.
func hostileTargets(id string) map[string]string {
	return map[string]string{
		"IPv4 loopback":        fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", edge.GatewayPort, id),
		"IPv6 loopback":        "/ip6/::1/tcp/4001/p2p/" + id,
		"a private range":      "/ip4/192.168.0.1/tcp/4001/p2p/" + id,
		"the overlay":          overlayPrefix + "1/tcp/4001/p2p/" + id,
		"a loopback host name": "/dns4/localhost/tcp/4001/p2p/" + id,
		"a peer id alone":      "/p2p/" + id,
	}
}

// networkConnectHostileTargetsNotConnected: connecting to loopback,
// private or overlay addresses under a peer nobody holds never answers 2xx,
// and a dial that cannot complete is not an internal error (500): the answer
// is a refusal (4xx) or an upstream failure (502/504), and the peer never
// appears in the peers list.
func networkConnectHostileTargetsNotConnected(t *testing.T, opNS *ns.Namespace) {
	op := tenancy.Owner(opNS)
	c := harness.GW(t).PinTo(harness.Fleet(t).State.Nodes[0].PublicIP)
	id := unheldPeer(t)
	for what, addr := range hostileTargets(id) {
		r := call(t, c, pathConnect, op, jsonBody(t, map[string]string{"multiaddr": addr}), nil)
		if !is4xx(r.Status) && r.Status != http.StatusBadGateway && r.Status != http.StatusGatewayTimeout {
			t.Errorf("connect to %s (%s): HTTP %d, want 4xx, 502 or 504: %.300s", what, addr, r.Status, r.Body)
		}
	}
	if _, listed := remotePeers(peersList(t, c, op))[id]; listed {
		t.Errorf("a peer nobody holds (%s) is listed as connected", id)
	}
}

// networkDisconnectUnheldPeerIdempotent: disconnecting from a
// well-formed peer the gateway is not connected to is a no-op that succeeds
// every time (libp2p ClosePeer on no connection), and no connected peer is
// dropped by it.
func networkDisconnectUnheldPeerIdempotent(t *testing.T, opNS *ns.Namespace) {
	op := tenancy.Owner(opNS)
	c := harness.GW(t).PinTo(harness.Fleet(t).State.Nodes[0].PublicIP)
	before := remotePeers(peersList(t, c, op))
	id := unheldPeer(t)
	for attempt := 1; attempt <= 2; attempt++ {
		r := call(t, c, pathDisconnect, op, jsonBody(t, map[string]string{"peer_id": id}), nil)
		if r.Status != http.StatusOK {
			t.Errorf("disconnect from an unconnected peer, attempt %d: HTTP %d, want 200: %.300s", attempt, r.Status, r.Body)
		}
	}
	after := remotePeers(peersList(t, c, op))
	for peer := range before {
		if _, ok := after[peer]; !ok {
			t.Errorf("peer %s was dropped by a disconnect aimed at %s", peer, id)
		}
	}
}

// dialablePeer is a connected remote peer's IPv4 multiaddr (its overlay
// address when it has one, else any non-loopback one) with its /p2p/ id, or
// fails the test.
func dialablePeer(t *testing.T, peers map[string][]string) (string, string) {
	t.Helper()
	ids := make([]string, 0, len(peers))
	for id := range peers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, want := range []func(string) bool{
		func(a string) bool { return strings.HasPrefix(a, overlayPrefix) },
		func(a string) bool { return strings.HasPrefix(a, "/ip4/") && !strings.HasPrefix(a, loopbackPrefix) },
	} {
		for _, id := range ids {
			for _, addr := range peers[id] {
				if want(addr) {
					return id, addr + "/p2p/" + id
				}
			}
		}
	}
	t.Fatalf("no connected peer has a non-loopback IPv4 address (overlay %s preferred): %v", infra.WireGuardSubnet, peers)
	return "", ""
}

// networkConnectConnectedPeerIdempotent: connecting to a peer the
// gateway is already connected to, at an address it has for it, succeeds twice
// and leaves it connected: a connect is idempotent and moves nothing.
func networkConnectConnectedPeerIdempotent(t *testing.T, opNS *ns.Namespace) {
	op := tenancy.Owner(opNS)
	c := harness.GW(t).PinTo(harness.Fleet(t).State.Nodes[0].PublicIP)
	id, addr := dialablePeer(t, remotePeers(peersList(t, c, op)))
	for attempt := 1; attempt <= 2; attempt++ {
		r := call(t, c, pathConnect, op, jsonBody(t, map[string]string{"multiaddr": addr}), nil)
		if r.Status != http.StatusOK || r.ErrorCode() != "" {
			t.Errorf("connect to connected peer %s, attempt %d: HTTP %d, want 200: %.300s", addr, attempt, r.Status, r.Body)
		}
	}
	if _, ok := remotePeers(peersList(t, c, op))[id]; !ok {
		t.Errorf("peer %s is no longer connected after an idempotent connect", id)
	}
}

//go:build e2e_fleet

package authcapabilityws

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// Capability rules (docs/whitepaper/technical-reference/vol1/13-identity.md#open-websockets, website/src/docs/developer/functions.mdx#capabilities).
const (
	minTTL           = time.Minute
	maxTTL           = 7 * 24 * time.Hour
	maxResourceBytes = 256
	socketsPerCap    = 16
	expiryGrace      = 2 * time.Minute
	sweep            = 10 * time.Second
	slack            = 5 * time.Second
	// revokedPoll is how often a revoked capability's upgrade is retried while
	// the gateways' revocation lists catch up.
	revokedPoll = time.Second
	// revokedRefusalBound is how long after a revocation another gateway may
	// still accept its upgrade: its list is used until it is 10 seconds old
	// (core pkg/gateway/auth RevocationStaleness), and a reload past that
	// takes at most 3 more (revocationReloadTimeout).
	revokedRefusalBound = 13 * time.Second
	closeExpired        = 4401
	closeRevoked        = 4403
	invalidBody         = "forbidden: the capability is not valid for this function"
	revokedBody         = "forbidden: this capability, or the device that issued it, was revoked"
	tooManyBody         = "too many sockets are open on this capability"
	upgradeRetryAfter   = "60"
)

// TestCapabilitySocket_lifecycle walks a capability from mint to expiry in
// one namespace (deploying the fixture twice is the expensive part): the
// mint's bounds, opening with the capability alone, identical refusals, the
// per-capability socket cap, revocation by capability and by device,
// expiry and the fn selector. Sockets are pinned to gateways because the
// socket cap is per gateway: node-3 opens and revokes, node-1 holds sixteen.
// The per-address upgrade limit needs a flood and is in
// auth-capability-ws-chaos, which runs alone.
func TestCapabilitySocket_lifecycle(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	if len(fx.f.State.Nodes) < 3 {
		t.Fatalf("this walk pins sockets to three gateways, the run has %d", len(fx.f.State.Nodes))
	}
	var first minted
	t.Run("mint bounds", func(t *testing.T) { first = mintBounds(t, fx) })
	if first.Token == "" {
		t.Fatal("no capability to go on with")
	}
	t.Run("opens alone and reports the capability", func(t *testing.T) { opensAlone(t, fx, first) })
	t.Run("identical refusals", func(t *testing.T) { identicalRefusals(t, fx, first) })
	t.Run("sixteen sockets per capability", func(t *testing.T) { sixteenSockets(t, fx, first) })
	t.Run("revoking the capability closes its sockets", func(t *testing.T) { revokeCapability(t, fx) })
	t.Run("revoking the issuing device closes its sockets", func(t *testing.T) { revokeDevice(t, fx) })
	t.Run("expiry closes with 4401", func(t *testing.T) { expiry(t, fx) })
	t.Run("fn selector", func(t *testing.T) { fnSelector(t, fx) })
}

func mintBounds(t *testing.T, fx *fixture) minted {
	for name, tc := range map[string]struct {
		s        *gw.Session
		resource string
		ttl      time.Duration
	}{
		"ttl under a minute":      {fx.bound, "mailbox-1", minTTL / 2},
		"ttl over seven days":     {fx.bound, "mailbox-1", maxTTL + time.Second},
		"resource over 256 bytes": {fx.bound, strings.Repeat("r", maxResourceBytes+1), time.Hour},
		"session with no device":  {fx.plain, "mailbox-1", time.Hour},
	} {
		if m := mint(t, fx, tc.s, tc.resource, tc.ttl); m.Token != "" || m.Error == "" {
			t.Errorf("%s: a capability was minted", name)
		}
	}
	m := mint(t, fx, fx.bound, "mailbox-1", time.Hour)
	if m.Token == "" || m.CapID == "" || m.Resource != "mailbox-1" || m.IssuerDevice != fx.dev.ID() {
		t.Fatalf("minted %+v, want mailbox-1 issued by %s", m, fx.dev.ID())
	}
	return m
}

func opensAlone(t *testing.T, fx *fixture, m minted) {
	node := fx.f.State.Nodes[2]
	conn, status, body := dialAt(t, fx.c, node, capPath(capFunction, fx.n.Name, m.Token), nil)
	if conn == nil {
		t.Fatalf("opening with the capability: %d %s", status, body)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(gw.WSHandshakeBudget)); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil || !strings.Contains(string(msg), m.CapID) || !strings.Contains(string(msg), "mailbox-1") {
		t.Errorf("the function did not see the capability it was opened with: %v %.300s", err, msg)
	}
	for name, h := range map[string]http.Header{"with a bearer": {"Authorization": {"Bearer " + fx.plain.AccessToken}}} {
		if _, status, _ := dialAt(t, fx.c, node, capPath(capFunction, fx.n.Name, m.Token), h); status != http.StatusBadRequest {
			t.Errorf("capability %s: want 400, got %d", name, status)
		}
	}
	withJWT := capPath(capFunction, fx.n.Name, m.Token) + "&" + url.Values{"jwt": {fx.plain.AccessToken}}.Encode()
	if _, status, _ := dialAt(t, fx.c, node, withJWT, nil); status != http.StatusBadRequest {
		t.Errorf("capability with ?jwt=: want 400, got %d", status)
	}
}

func identicalRefusals(t *testing.T, fx *fixture, m minted) {
	node := fx.f.State.Nodes[2]
	forged := randomToken(t, len(m.Token))
	for name, path := range map[string]string{
		"forged token":             capPath(capFunction, fx.n.Name, forged),
		"function without ws_auth": capPath(plainFunc, fx.n.Name, m.Token),
		"pinned version":           capPath(capFunction+"@1", fx.n.Name, m.Token),
		"no such function":         capPath("e2e-no-such-fn", fx.n.Name, m.Token),
		"another namespace":        capPath(capFunction, gw.LobbyNamespace, m.Token),
	} {
		if _, status, body := dialAt(t, fx.c, node, path, nil); status != http.StatusForbidden || body != invalidBody {
			t.Errorf("%s: want 403 %q, got %d %q", name, invalidBody, status, body)
		}
	}
}

// randomToken is n base64url characters of random bytes: a forgery owes
// nothing to the real capability, so the refusal cannot hinge on which
// characters of it were changed.
func randomToken(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, base64.RawURLEncoding.DecodedLen(n)+1)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("failed to read random bytes for a forged capability: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n]
}

func sixteenSockets(t *testing.T, fx *fixture, m minted) {
	node := fx.f.State.Nodes[0]
	var open []*websocket.Conn
	for i := 0; i < socketsPerCap; i++ {
		conn, status, body := dialAt(t, fx.c, node, capPath(capFunction, fx.n.Name, m.Token), nil)
		if conn == nil {
			t.Fatalf("socket %d of %d refused: %d %s", i+1, socketsPerCap, status, body)
		}
		open = append(open, conn)
	}
	if _, status, body := dialAt(t, fx.c, node, capPath(capFunction, fx.n.Name, m.Token), nil); status != http.StatusTooManyRequests || !strings.Contains(body, tooManyBody) {
		t.Errorf("socket %d: want 429 %q, got %d %q", socketsPerCap+1, tooManyBody, status, body)
	}
	for _, c := range open {
		c.Close()
	}
}

func revokeCapability(t *testing.T, fx *fixture) {
	node := fx.f.State.Nodes[2]
	m := mint(t, fx, fx.bound, "mailbox-revoke", time.Hour)
	conn, status, body := dialAt(t, fx.c, node, capPath(capFunction, fx.n.Name, m.Token), nil)
	if conn == nil {
		t.Fatalf("opening: %d %s", status, body)
	}
	resp := invoke(t, fx, capFunction, fx.bound.AccessToken, map[string]any{"op": "revoke", "token": m.Token})
	if !strings.Contains(string(resp.Expect(t, http.StatusOK).Body), `"revoked":true`) {
		t.Fatalf("capability_revoke: %s", resp.Body)
	}
	revokedAt := time.Now()
	if code := closeCode(t, conn, sweep+slack); code != closeRevoked {
		t.Errorf("the revoked capability's socket closed with %d, want %d within %s", code, closeRevoked, sweep)
	}
	refusedAsRevoked(t, fx, node, m.Token, revokedAt, "reopening a revoked capability")
}

func revokeDevice(t *testing.T, fx *fixture) {
	node := fx.f.State.Nodes[2]
	d2 := gw.NewDevice(t, wallet.AlgES256)
	m := mint(t, fx, signIn(t, fx.c, fx.user, fx.n.Name, d2), "mailbox-device", time.Hour)
	conn, status, body := dialAt(t, fx.c, node, capPath(capFunction, fx.n.Name, m.Token), nil)
	if conn == nil {
		t.Fatalf("opening: %d %s", status, body)
	}
	if _, err := fx.c.For(t).RevokeDevice(t.Context(), fx.plain.AccessToken, d2.ID(), nil); err != nil {
		t.Fatalf("revoking the issuing device: %v", err)
	}
	revokedAt := time.Now()
	if code := closeCode(t, conn, sweep+slack); code != closeRevoked {
		t.Errorf("a capability of a revoked device kept its socket (close %d)", code)
	}
	refusedAsRevoked(t, fx, node, m.Token, revokedAt, "a revoked device's capability")
}

func expiry(t *testing.T, fx *fixture) {
	m := mint(t, fx, fx.bound, "mailbox-short", minTTL)
	conn, status, body := dialAt(t, fx.c, fx.f.State.Nodes[2], capPath(capFunction, fx.n.Name, m.Token), nil)
	if conn == nil {
		t.Fatalf("opening a one-minute capability: %d %s", status, body)
	}
	start := time.Now()
	if code := closeCode(t, conn, minTTL+expiryGrace+sweep+slack); code != closeExpired {
		t.Fatalf("an expired capability's socket closed with %d after %s, want %d", code, time.Since(start), closeExpired)
	}
	if took := time.Since(start); took < minTTL+expiryGrace-slack {
		t.Errorf("closed after %s, before the capability's minute plus the two-minute grace", took)
	}
}

func fnSelector(t *testing.T, fx *fixture) {
	w := newWallet(t)
	fx.n.CLI.MustOK(t, "members", "add", w.Address(), "--role", "runtime", "--resource", "fn:name="+capFunction)
	tok := signIn(t, fx.c, w, fx.n.Name, nil).AccessToken
	if r := invoke(t, fx, capFunction, tok, map[string]any{}); r.Status != http.StatusOK {
		t.Errorf("invoking the named function under fn:name=%s: %d %s", capFunction, r.Status, r.Body)
	}
	if r := invoke(t, fx, plainFunc, tok, map[string]any{}); r.Status != http.StatusForbidden {
		t.Errorf("invoking another function under fn:name=%s: want 403, got %d", capFunction, r.Status)
	}
}

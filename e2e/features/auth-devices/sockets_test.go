//go:build e2e_fleet

package authdevices

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	pathPubsubWS = "/v1/pubsub/ws"
	pathKeys     = "/v1/namespace/keys"
	// closeRevoked: "the token, its session or its subject was revoked"
	// (docs/AUTH.md#open-websockets).
	closeRevoked = 4403
	// sweepBudget: sockets are re-checked every 10 seconds; a close frame
	// and the round trip come on top.
	sweepBudget = revocationStaleness + stalenessSlack
)

// subscribe opens a pub/sub subscription on c with bearer (header), or with
// a query credential when query is set.
func subscribe(t testing.TB, c *gw.Client, bearer string, query url.Values) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	if query == nil {
		query = url.Values{}
	}
	query.Set("topic", "e2e-auth")
	conn, resp, err := c.For(t).DialWS(t.Context(), pathPubsubWS+"?"+query.Encode(), bearer, nil)
	if conn != nil {
		t.Cleanup(func() { conn.Close() })
	}
	return conn, resp, err
}

func mustSubscribe(t testing.TB, c *gw.Client, bearer string, query url.Values) *websocket.Conn {
	t.Helper()
	conn, resp, err := subscribe(t, c, bearer, query)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("subscribing: HTTP %d: %v", status, err)
	}
	return conn
}

// closeCode reads until the server closes conn or within passes, and returns
// the close code (0 when the socket stayed open).
func closeCode(t testing.TB, conn *websocket.Conn, within time.Duration) int {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(within)); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			return ce.Code
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return 0
		}
		t.Fatalf("socket failed without a close frame: %v", err)
	}
}

// TestSocketRevocation_closesWith4403: a subscription opened with a token is
// closed with 4403 within ten seconds when that token is revoked (logout),
// its session is ended, or its device is revoked — on the public and on the
// namespace gateway (docs/AUTH.md#open-websockets).
func TestSocketRevocation_closesWith4403(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	w := member(t, n, roleRuntime)
	revokers := map[string]func(s *gw.Session, d *wallet.Device, plain *gw.Session){
		"logout": func(s *gw.Session, _ *wallet.Device, _ *gw.Session) {
			if _, err := c.For(t).Logout(t.Context(), s.AccessToken, s.RefreshToken, n.Name, false); err != nil {
				t.Fatal(err)
			}
		},
		"session ended": func(s *gw.Session, _ *wallet.Device, plain *gw.Session) {
			sid := sessionIDOf(t, c, plain.AccessToken, s.DeviceID)
			if _, err := c.For(t).EndSession(t.Context(), plain.AccessToken, sid, nil); err != nil {
				t.Fatal(err)
			}
		},
		"device revoked": func(_ *gw.Session, d *wallet.Device, plain *gw.Session) {
			if _, err := c.For(t).RevokeDevice(t.Context(), plain.AccessToken, d.ID(), nil); err != nil {
				t.Fatal(err)
			}
		},
	}
	plain := signInSession(t, c, w, n.Name, nil)
	for name, revoke := range revokers {
		d := gw.NewDevice(t, wallet.AlgEd25519)
		s := signInSession(t, c, w, n.Name, d)
		viaMain := mustSubscribe(t, c, s.AccessToken, nil)
		viaNS := mustSubscribe(t, n.Client, s.AccessToken, nil)
		revoke(s, d, plain)
		for gwName, conn := range map[string]*websocket.Conn{"public gateway": viaMain, "namespace gateway": viaNS} {
			if code := closeCode(t, conn, sweepBudget); code != closeRevoked {
				t.Errorf("%s, %s: socket closed with %d, want %d within %s", name, gwName, code, closeRevoked, sweepBudget)
			}
		}
	}
}

// sessionIDOf finds the listed session bound to deviceID.
func sessionIDOf(t testing.TB, c *gw.Client, bearer, deviceID string) int64 {
	t.Helper()
	sessions, _, err := c.For(t).Sessions(t.Context(), bearer)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.DeviceID == deviceID && deviceID != "" {
			return s.ID
		}
	}
	t.Fatalf("no session bound to device %s", deviceID)
	return 0
}

// TestSocketRevocation_keySocketsStayOpen: a socket opened with an API key
// has no token to re-check; revoking the key refuses its next upgrade and
// leaves the open socket alone (docs/AUTH.md#open-websockets). ?jwt= opens a
// socket with a token where a header cannot be set.
func TestSocketRevocation_keySocketsStayOpen(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	var minted struct {
		ID     int64  `json:"id"`
		APIKey string `json:"api_key"`
	}
	resp := postJSON(t, c, pathKeys, n.Owner.Token(), map[string]any{"scope": "pubsub", "label": "e2e-socket"})
	if err := resp.Expect(t, http.StatusCreated).Decode(&minted); err != nil {
		t.Fatal(err)
	}
	protect(t, c, minted.APIKey)
	byKey := mustSubscribe(t, c, "", url.Values{"api_key": {minted.APIKey}})
	mustSubscribe(t, c, "", url.Values{"token": {minted.APIKey}}).Close()
	s := signInSession(t, c, member(t, n, roleRuntime), n.Name, nil)
	mustSubscribe(t, c, "", url.Values{"jwt": {s.AccessToken}}).Close()
	send(t, c, http.MethodDelete, pathKeys+"/"+strconv.FormatInt(minted.ID, 10), n.Owner.Token(), nil).Expect(t, http.StatusOK)
	// The DELETE and the upgrade may reach different gateways: each drops
	// the key from its cache within the revocation list's staleness
	// (core/pkg/gateway/middleware.go).
	eventually.Require(t, pollEvery, revocationStaleness+stalenessSlack, "every gateway to refuse the revoked key's upgrade", func() (bool, error) {
		conn, resp, err := subscribe(t, c, "", url.Values{"api_key": {minted.APIKey}})
		if conn != nil {
			conn.Close()
			return false, errors.New("the revoked key opened a new socket")
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			return false, fmt.Errorf("upgrade with the revoked key: want 401, got %v (err %v)", resp, err)
		}
		return true, nil
	})
	if code := closeCode(t, byKey, sweepBudget); code != 0 {
		t.Errorf("the key's open socket was closed with %d; key sockets are not re-checked", code)
	}
}

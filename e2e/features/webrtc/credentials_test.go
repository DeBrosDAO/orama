//go:build e2e_fleet

package webrtc

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// TestCredentials_restShapeAndAccess: the REST credential is
// "<expiry>:<namespace>" for 24 h with the udp/tcp/turns ladder; it needs a
// signed-in user with the webrtc grant (docs/WEBRTC.md#1-get-turn-credentials,
// #turn-credential-protocol).
func TestCredentials_restShapeAndAccess(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	waitPlaced(t, fx)
	r, cr := restCreds(t, fx.c, fx.token)
	r.Expect(t, http.StatusOK)
	parts := strings.SplitN(cr.Username, ":", 2)
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if len(parts) != 2 || err != nil || parts[1] != fx.n.Name || cr.TTL != int(restTTL.Seconds()) {
		t.Fatalf("credential %q ttl %d, want <expiry>:%s with ttl %d", cr.Username, cr.TTL, fx.n.Name, int(restTTL.Seconds()))
	}
	if left := time.Until(time.Unix(exp, 0)); left < restTTL-time.Minute || left > restTTL+time.Minute {
		t.Errorf("the credential expires in %s, want 24h", left)
	}
	base := fx.f.State.BaseDomain
	want := []string{"turn:turn.ns-" + fx.n.Name + "." + base + ":3478?transport=udp", "turn:turn.ns-" + fx.n.Name + "." + base + ":3478?transport=tcp",
		"turns:turn-" + fx.n.Name + "." + base + ":5349"}
	if !slices.Equal(cr.URIs[:min(len(cr.URIs), 3)], want) {
		t.Errorf("uris %v, want %v first", cr.URIs, want)
	}
	if r := fx.c.MustSend(t, gw.Req{Path: pathCreds, Bearer: fx.token}); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("GET credentials: want 405, got %d", r.Status)
	}
	tenancy.ExpectRefused(t, fx.c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathCreds}), http.StatusUnauthorized, tenancy.CodeMissing)
	reader := member(t, fx.n, "reader")
	tenancy.ExpectRefused(t, fx.c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathCreds, Bearer: reader}), http.StatusForbidden, tenancy.CodeScope)
}

// TestTURN_relayOnlyAuth: the namespace's credentials allocate relays on the
// TURN nodes, from the host-wide 49152-65535 range; a credential naming a
// namespace the server does not serve, a tampered password, or one for an
// expiry that was changed after signing allocates nothing
// (docs/WEBRTC.md#turn-topology: isolation is the per-tenant secret).
func TestTURN_relayOnlyAuth(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	holders := waitPlaced(t, fx)
	_, cr := restCreds(t, fx.c, fx.token)
	cands := gather(t, cr)
	if len(cands) == 0 {
		t.Fatal("valid credentials allocated no relay")
	}
	for _, c := range cands {
		f := strings.Fields(c)
		port, _ := strconv.Atoi(f[1])
		if !slices.ContainsFunc(holders, func(n fleet.Node) bool { return n.PublicIP == f[0] }) || port < relayLow || port > relayHigh {
			t.Errorf("relay %s is not a TURN node's address in %d-%d", c, relayLow, relayHigh)
		}
	}
	parts := strings.SplitN(cr.Username, ":", 2)
	forged := map[string]services.TURNCreds{
		"unknown namespace":  {URIs: cr.URIs, Username: parts[0] + ":e2e-no-such-ns", Password: sign("guess", parts[0]+":e2e-no-such-ns")},
		"tampered password":  {URIs: cr.URIs, Username: cr.Username, Password: sign("guess", cr.Username)},
		"moved expiry":       {URIs: cr.URIs, Username: strconv.FormatInt(time.Now().Add(48*time.Hour).Unix(), 10) + ":" + fx.n.Name, Password: cr.Password},
		"expired, re-signed": {URIs: cr.URIs, Username: "1:" + fx.n.Name, Password: sign("guess", "1:"+fx.n.Name)},
	}
	for name, bad := range forged {
		if got := gather(t, bad); len(got) != 0 {
			t.Errorf("%s: allocated %v", name, got)
		}
	}
}

func gather(t *testing.T, cr services.TURNCreds) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), services.GatherBudget)
	defer cancel()
	c, err := services.RelayCandidates(ctx, cr)
	if err != nil {
		t.Fatalf("gathering: %v", err)
	}
	return c
}

// sign is the TURN REST password for username under secret.
func sign(secret, username string) string {
	h := hmac.New(sha1.New, []byte(secret))
	h.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// TestSignal_accessAndRooms: the signalling socket needs a signed-in user
// (a key alone is refused), the first frame must be a join, and the rooms
// route reports the SFU. docs/WEBRTC.md lists POST/DELETE rooms; the handler
// serves GET only, so creating a room over REST is refused.
func TestSignal_accessAndRooms(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	waitPlaced(t, fx)
	if _, resp, err := fx.c.DialWS(t.Context(), services.SignalPath+"?room=r", "", nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an anonymous signalling socket: %v", err)
	}
	key := tenancy.APIKey(t, fx.n, "app-runtime")
	if _, resp, err := fx.c.DialWS(t.Context(), services.SignalPath+"?room=r", "", http.Header{"X-API-Key": {key}}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a signalling socket with a key alone: %v", err)
	}
	conn, _, err := fx.c.DialWS(t.Context(), services.SignalPath+"?room=r", fx.token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "offer", "data": map[string]string{"sdp": "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(dialBudget)); err != nil {
		t.Fatal(err)
	}
	var m services.SignalMsg
	if err := conn.ReadJSON(&m); err != nil || m.Type != services.MsgError {
		t.Errorf("a first frame that is not a join: %+v %v, want an error frame", m, err)
	}
	var rooms struct{ Status string }
	if err := fx.c.MustSend(t, gw.Req{Path: pathRooms, Bearer: fx.token}).Expect(t, http.StatusOK).Decode(&rooms); err != nil || rooms.Status != "ok" {
		t.Errorf("rooms: %+v %v", rooms, err)
	}
	if r := fx.c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathRooms, Bearer: fx.token, Body: []byte(`{"room_id":"x"}`)}); r.Status < 400 {
		t.Errorf("POST rooms by a runtime member: %d", r.Status)
	}
}

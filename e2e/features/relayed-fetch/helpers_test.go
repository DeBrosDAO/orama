//go:build e2e_fleet

package relayedfetch

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// Routes, headers, codes and limits (docs/API_SURFACE.md#storage and
// #network-and-proxy; core/pkg/gateway/relay_tunnel_handler.go,
// core/pkg/gateway/handlers/storage/fetch_caps_handler.go).
const (
	pathUpload   = "/v1/storage/upload"
	pathGet      = "/v1/storage/get/"
	pathFetchCap = "/v1/storage/fetch-caps"
	pathRelayed  = "/v1/storage/relayed/"
	pathRelay    = "/v1/proxy/relay"
	pathQuery    = "/v1/rqlite/query"

	capHeader       = "X-Orama-Fetch-Cap"
	revokeKeyHeader = "X-Orama-Revoke-Key"

	codeMissing        = "FETCH_CAP_MISSING"
	codeInvalid        = "FETCH_CAP_INVALID"
	codeRevoked        = "FETCH_CAP_REVOKED"
	codeRevokeKey      = "FETCH_CAP_REVOKE_KEY_INVALID"
	codeNotAlone       = "FETCH_CAP_NOT_ALONE"
	codeDeviceRequired = "FETCH_CAP_DEVICE_REQUIRED"
	codeNotAllowed     = "RELAY_DESTINATION_NOT_ALLOWED"

	capTTLSeconds = 2 * 60 * 60
	objectBytes   = 96 << 10

	pollEvery = 3 * time.Second
	// pinPropagation is the window a fresh pin may be invisible on a node.
	pinPropagation = 3 * time.Minute
	// torBudget covers a fresh Tor circuit (and a retry of a slow one).
	torBudget = 3 * time.Minute
	// revocationBound is the revocation list's staleness (docs/AUTH.md) plus slack.
	revocationBound = 30 * time.Second
	// logFlush is the request log batcher's interval plus slack.
	logFlush = 60 * time.Second
	ioBudget = 60 * time.Second
)

// fixture is a namespace whose owner holds a device-bound session, with one
// uploaded object.
type fixture struct {
	n *ns.Namespace
	// token is the bearer of a device-bound session that may upload and mint:
	// the owner's, or a runtime member's.
	token   func() string
	c       *gw.Client // the namespace gateway: S
	relay   *gw.Client // a cluster gateway used as the relay: R
	host    string
	cid     string
	content []byte
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{DeviceAlg: wallet.AlgEd25519})
	return finish(t, f, n, n.Owner.Token)
}

// setupWithCLI is setup for a namespace the run's operator created, so the
// test can use its CLI; a runtime member bound to a device holds the session.
func setupWithCLI(t *testing.T) *fixture {
	t.Helper()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{Via: ns.ViaOperator})
	// The wallet is made a member before it signs in at all: a device binds
	// only to a namespace session, and the lobby refuses one.
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	device := gw.NewDevice(t, wallet.AlgEd25519)
	n.CLI.MustOK(t, "members", "add", w.Address(), "--role", tenancy.RoleRuntime)
	sess, err := gw.ForFleet(t, f).SignIn(t.Context(), w, n.Name, device)
	if err != nil {
		t.Fatal(err)
	}
	return finish(t, f, n, func() string { return sess.AccessToken })
}

func finish(t *testing.T, f *fleet.Fleet, n *ns.Namespace, token func() string) *fixture {
	t.Helper()
	fx := &fixture{n: n, token: token, c: n.Client, relay: harness.GW(t), host: tenancy.NamespaceHost(f, n.Name)}
	fx.content = make([]byte, objectBytes)
	if _, err := rand.Read(fx.content); err != nil {
		t.Fatal(err)
	}
	fx.cid = fx.upload(t, fx.content)
	return fx
}

// upload stores data as the owner and waits until it downloads.
func (fx *fixture) upload(t *testing.T, data []byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "relayed.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := fx.c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathUpload, Bearer: fx.token(),
		Header: http.Header{"Content-Type": {w.FormDataContentType()}}, Body: buf.Bytes()}).Expect(t, http.StatusOK)
	var up struct {
		Cid string `json:"cid"`
	}
	if err := r.Decode(&up); err != nil || up.Cid == "" {
		t.Fatalf("upload answered %s (%v)", r.Body, err)
	}
	eventually.Require(t, pollEvery, pinPropagation, "download of "+up.Cid, func() (bool, error) {
		g := fx.get(t, up.Cid)
		if g.Status != http.StatusOK {
			return false, fmt.Errorf("status %d: %.200s", g.Status, g.Body)
		}
		return true, nil
	})
	return up.Cid
}

func (fx *fixture) get(t testing.TB, cid string) *gw.Response {
	t.Helper()
	return fx.c.MustSend(t, gw.Req{Path: pathGet + cid, Bearer: fx.token()})
}

type fetchCap struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	RevokeKey string `json:"revoke_key"`
	ExpiresAt int64  `json:"expires_at"`
}

type mintBody struct {
	Namespace string     `json:"namespace"`
	CID       string     `json:"cid"`
	Caps      []fetchCap `json:"caps"`
}

// mintRaw posts a mint as bearer.
func (fx *fixture) mintRaw(t testing.TB, bearer string, body any) *gw.Response {
	t.Helper()
	return tenancy.Post(t, fx.c, pathFetchCap, tenancy.Cred{Bearer: bearer}, body)
}

// mint asks for count capabilities for cid as the owner and registers them for
// redaction: they are bearer capabilities and must not reach the evidence.
func (fx *fixture) mint(t testing.TB, cid string, count int) []fetchCap {
	t.Helper()
	r := fx.mintRaw(t, fx.token(), map[string]any{"cid": cid, "count": count, "ttl_seconds": capTTLSeconds}).
		Expect(t, http.StatusOK)
	var out mintBody
	if err := r.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Namespace != fx.n.Name || out.CID != cid || len(out.Caps) != count {
		t.Fatalf("mint answered %+v, want %d capabilities for %s in %s", out, count, cid, fx.n.Name)
	}
	for _, c := range out.Caps {
		protect(t, fx.c, c.Token, c.RevokeKey)
	}
	return out.Caps
}

// revoke asks S to revoke a capability by id, presenting revokeKey when it is
// not empty, as the owner.
func (fx *fixture) revoke(t testing.TB, id, revokeKey string) *gw.Response {
	t.Helper()
	h := http.Header{}
	if revokeKey != "" {
		h.Set(revokeKeyHeader, revokeKey)
	}
	return fx.c.MustSend(t, gw.Req{Method: http.MethodDelete, Path: pathFetchCap + "/" + id, Bearer: fx.token(), Header: h})
}

// direct fetches cid from S with the capability header and no credential.
func (fx *fixture) direct(t testing.TB, cid, token string, extra http.Header) *gw.Response {
	t.Helper()
	h := http.Header{}
	if token != "" {
		h.Set(capHeader, token)
	}
	for k, v := range extra {
		h[k] = v
	}
	return fx.c.MustSend(t, gw.Req{Path: pathRelayed + cid, Header: h})
}

// protect registers values with the evidence redactor.
func protect(t testing.TB, c *gw.Client, values ...string) {
	t.Helper()
	red := c.Recorder().Redactor()
	if red == nil {
		return
	}
	if err := red.Add(values...); err != nil {
		t.Errorf("failed to register a capability for redaction: %v", err)
	}
}

// wsConn adapts the relay's WebSocket to a net.Conn carrying the raw TCP
// stream in binary frames (docs/ARCHITECTURE.md: /v1/proxy/relay framing).
type wsConn struct {
	*websocket.Conn
	pending []byte
}

func (c *wsConn) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		typ, data, err := c.ReadMessage()
		if err != nil {
			return 0, io.EOF
		}
		if typ == websocket.BinaryMessage {
			c.pending = data
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *wsConn) Write(p []byte) (int, error) {
	if err := c.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

var _ net.Conn = (*wsConn)(nil)

func relayPath(host, port string, extra url.Values) string {
	q := url.Values{"host": {host}, "port": {port}}
	for k, v := range extra {
		q[k] = v
	}
	return pathRelay + "?" + q.Encode()
}

// openRelay opens the relay with NO credential. On a refused upgrade it
// returns the status and the response.
func openRelay(t testing.TB, c *gw.Client, host, port string) (*wsConn, *gw.Response) {
	t.Helper()
	conn, resp, err := c.DialWS(t.Context(), relayPath(host, port, nil), "", nil)
	if err == nil {
		t.Cleanup(func() { conn.Close() })
		return &wsConn{Conn: conn}, nil
	}
	if resp == nil {
		t.Fatalf("relay to %s:%s: %v", host, port, err)
	}
	b, rerr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if rerr != nil {
		t.Errorf("reading the refused upgrade's body: %v", rerr)
	}
	return nil, &gw.Response{Status: resp.StatusCode, Header: resp.Header, Body: b}
}

// relayedFetch is the minimal relay client: it opens the relay on R, runs TLS
// to host over the byte stream with the harness's trust, sends the request with
// the capability and reads the response.
func relayedFetch(t testing.TB, r *gw.Client, host, cid, token string) (*http.Response, []byte, error) {
	t.Helper()
	ws, refused := openRelay(t, r, host, "443")
	if refused != nil {
		return nil, nil, fmt.Errorf("the relay refused the upgrade: %d %s", refused.Status, refused.Body)
	}
	cfg := r.TLS.Clone()
	cfg.ServerName = host
	cfg.MinVersion = tls.VersionTLS12
	conn := tls.Client(ws, cfg)
	_ = conn.SetDeadline(time.Now().Add(ioBudget))
	if err := conn.Handshake(); err != nil {
		return nil, nil, fmt.Errorf("TLS to %s through the relay: %w", host, err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+host+pathRelayed+cid, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set(capHeader, token)
	req.Close = true
	if err := req.Write(conn); err != nil {
		return nil, nil, fmt.Errorf("sending the request through the relay: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the response through the relay: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, body, fmt.Errorf("reading the body through the relay: %w", err)
	}
	return resp, body, nil
}

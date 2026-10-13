//go:build e2e_fleet

package anontor

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// Routes and limits (docs/whitepaper/technical-reference/appendices/i-api-surface.md#network-and-proxy;
// core/pkg/gateway/anon_proxy_handler.go, anon_tunnel_handler.go).
const (
	pathAnon       = "/v1/proxy/anon"
	pathTunnel     = "/v1/proxy/tunnel"
	codeNotAllowed = "DESTINATION_NOT_ALLOWED"
	codeJWTNeeded  = "USER_JWT_REQUIRED"
	// publicTarget is a public HTTPS site the Tor exits can reach.
	publicTarget = "example.com"
	torBudget    = 90 * time.Second
	ioBudget     = 60 * time.Second
)

type anonResult struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Error      string            `json:"error"`
}

// anon sends one /v1/proxy/anon request as who.
func anon(t testing.TB, c *gw.Client, who tenancy.Cred, target, method string) (*gw.Response, anonResult) {
	t.Helper()
	r := tenancy.Post(t, c, pathAnon, who, map[string]any{"url": target, "method": method})
	var res anonResult
	if len(r.Body) > 0 && r.Body[0] == '{' {
		if err := json.Unmarshal(r.Body, &res); err != nil {
			t.Fatalf("proxy answer: %v", err)
		}
	}
	return r, res
}

// body decodes the proxied response body (base64).
func body(t testing.TB, res anonResult) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(res.Body)
	if err != nil {
		t.Fatalf("proxied body is not base64: %v", err)
	}
	return string(b)
}

// tunnelPath is the upgrade path for host:port.
func tunnelPath(host, port string) string {
	return pathTunnel + "?" + url.Values{"host": {host}, "port": {port}}.Encode()
}

// wsConn adapts a tunnel WebSocket to a net.Conn carrying the raw TCP stream
// in binary frames, so a TLS client can run end to end through it.
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

// openTunnel dials the tunnel to host:port as bearer; on refusal it returns
// the HTTP status and body.
func openTunnel(t testing.TB, c *gw.Client, bearer, host, port string) (*wsConn, int, string) {
	t.Helper()
	conn, resp, err := c.DialWS(t.Context(), tunnelPath(host, port), bearer, nil)
	if err == nil {
		t.Cleanup(func() { conn.Close() })
		return &wsConn{Conn: conn}, http.StatusSwitchingProtocols, ""
	}
	if resp == nil {
		t.Fatalf("tunnel to %s:%s: %v", host, port, err)
	}
	b, rerr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if rerr != nil {
		t.Errorf("reading the refused upgrade's body: %v", rerr)
	}
	return nil, resp.StatusCode, string(b)
}

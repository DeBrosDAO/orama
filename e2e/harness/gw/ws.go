package gw

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

// WSHandshakeBudget bounds the WebSocket handshake.
const WSHandshakeBudget = 20 * time.Second

// DialWS opens a WebSocket to path (with query) on the client's gateway, over
// the same pinned trust, with bearer auth when token is set. The handshake
// response is returned even when the upgrade is refused, for negative tests.
// A pinned client (PinTo) dials its node, with SNI and Host the gateway name.
func (c *Client) DialWS(ctx context.Context, pathAndQuery, token string, header http.Header) (*websocket.Conn, *http.Response, error) {
	if c.pinErr != nil {
		return nil, nil, c.pinErr
	}
	u := c.BaseURL + pathAndQuery
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	h := http.Header{}
	for k, vs := range header {
		h[k] = append([]string{}, vs...)
	}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	d := websocket.Dialer{TLSClientConfig: c.TLS, HandshakeTimeout: WSHandshakeBudget, Proxy: nil}
	if c.pinIP != "" {
		d.NetDialContext = pinnedDial(c.pinIP)
	}
	start := time.Now()
	conn, resp, err := d.DialContext(ctx, u, h)
	rec := evidence.Record{Kind: evidence.KindHTTP, Test: c.test, Summary: "WS " + u + c.pinNote(),
		DurationMS: time.Since(start).Milliseconds(), Input: dumpHeaders(h)}
	if resp != nil {
		rec.Status, rec.Output = resp.StatusCode, dumpHeaders(resp.Header)
	}
	if err != nil {
		rec.Error = err.Error()
		err = fmt.Errorf("failed to open WebSocket %s: %w", pathAndQuery, err)
	}
	if recErr := c.rec.Add(rec); recErr != nil {
		recErr = errors.Join(err, fmt.Errorf("failed to record WebSocket dial: %w", recErr))
		if conn != nil {
			recErr = errors.Join(recErr, conn.Close())
		}
		return nil, resp, recErr
	}
	return conn, resp, err
}

package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

const tunnelHiddenHost = "hidden-destination.example"

// usesTunnelStub makes the handler think Tor is up and dials through dial, and
// restores the real ones afterwards.
func usesTunnelStub(t *testing.T, dial func(context.Context, string, string) (net.Conn, error)) {
	t.Helper()
	prevRunning, prevDial := anonProxyRunning, anonTunnelDial
	anonProxyRunning = func() bool { return true }
	anonTunnelDial = dial
	t.Cleanup(func() { anonProxyRunning, anonTunnelDial = prevRunning, prevDial })
}

func assertTunnelLogNamesNoDestination(t *testing.T, lines []string) {
	t.Helper()
	if len(lines) == 0 {
		t.Fatal("nothing was logged, so the test checks nothing")
	}
	for _, l := range lines {
		for _, leaked := range []string{tunnelHiddenHost, "host:", "port:"} {
			if strings.Contains(l, leaked) {
				t.Errorf("a tunnel log line names the destination (%q): %s", leaked, l)
			}
		}
	}
}

func tunnelRequestURL(base string) string {
	return base + "/v1/proxy/tunnel?host=" + tunnelHiddenHost + "&port=443"
}

// The tunnel is part of the anonymity surface: the node's log must not say
// which host and port a wallet reached, on open, on close or on a failed dial.
func TestAnonTunnelHandler_openAndCloseLogNameNoDestination(t *testing.T) {
	gwSide, destSide := net.Pipe()
	usesTunnelStub(t, func(context.Context, string, string) (net.Conn, error) { return gwSide, nil })
	g := newTunnelTestGateway(t)
	lines := observed(g)

	finished := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyJWT, &auth.JWTClaims{Sub: "0xabc"}))
		g.anonTunnelHandler(w, r)
	}))
	defer srv.Close()

	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(tunnelRequestURL(srv.URL), "http"), nil)
	if err != nil {
		t.Fatalf("dial the tunnel: %v", err)
	}
	if err := c.WriteMessage(websocket.BinaryMessage, []byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 10)
	if _, err := destSide.Read(buf); err != nil {
		t.Fatalf("destination read: %v", err)
	}
	_ = c.Close()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel did not close")
	}

	logged := lines()
	assertTunnelLogNamesNoDestination(t, logged)
	joined := strings.Join(logged, "\n")
	for _, kept := range []string{"tunnel opened", "tunnel closed", "bytes_to_destination:10", "duration"} {
		if !strings.Contains(joined, kept) {
			t.Errorf("the tunnel log lost %q: %s", kept, joined)
		}
	}
}

func TestAnonTunnelHandler_dialFailureLogNamesNoDestination(t *testing.T) {
	usesTunnelStub(t, func(_ context.Context, addr, _ string) (net.Conn, error) {
		return nil, errors.New("socks connect tcp 127.0.0.1:9050->" + addr + ": host unreachable")
	})
	g := newTunnelTestGateway(t)
	lines := observed(g)

	r := httptest.NewRequest(http.MethodGet, tunnelRequestURL(""), nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyJWT, &auth.JWTClaims{Sub: "0xabc"}))
	rec := httptest.NewRecorder()
	g.anonTunnelHandler(rec, r)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	logged := lines()
	assertTunnelLogNamesNoDestination(t, logged)
	if !strings.Contains(strings.Join(logged, "\n"), "error_class:transport") {
		t.Errorf("the dial failure lost its error class: %v", logged)
	}
}

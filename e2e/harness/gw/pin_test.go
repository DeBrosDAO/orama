package gw

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

func testTLS() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS12} }

// pinnedHost is a name httptest's certificate is valid for and that does not
// resolve to the test server: only a pinned dial reaches it.
const pinnedHost = "example.com"

type seen struct {
	mu        sync.Mutex
	host, sni string
}

func (s *seen) get() (host, sni string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host, s.sni
}

// startNamedGateway serves TLS on 127.0.0.1 with httptest's certificate (valid
// for example.com) and returns a client for https://example.com:<port>.
func startNamedGateway(t *testing.T) (*Client, *seen, string) {
	t.Helper()
	s := &seen{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.host, s.sni = r.Host, r.TLS.ServerName
		s.mu.Unlock()
		if websocket.IsWebSocketUpgrade(r) {
			up := websocket.Upgrader{}
			if conn, err := up.Upgrade(w, r, nil); err == nil {
				_ = conn.Close()
			}
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	srv.StartTLS()
	t.Cleanup(srv.Close)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	evDir := filepath.Join(t.TempDir(), "ev")
	rec, err := evidence.New(evDir, "gw", nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(srv.URL)
	c, err := New("https://"+pinnedHost+":"+u.Port(), caFile, rec)
	if err != nil {
		t.Fatal(err)
	}
	return c, s, evDir
}

func TestPinTo_dialsNodeKeepsHostAndSNI(t *testing.T) {
	c, s, evDir := startNamedGateway(t)
	pinned := c.PinTo("127.0.0.1")
	resp, err := pinned.Send(context.Background(), Req{Path: "/health"})
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if host, sni := s.get(); !strings.HasPrefix(host, pinnedHost+":") || sni != pinnedHost {
		t.Fatalf("Host %q SNI %q", host, sni)
	}
	if pinned.PinnedIP() != "127.0.0.1" || c.PinnedIP() != "" {
		t.Fatal("PinTo changed the original client or lost the address")
	}
	recs, err := evidence.Load(evDir)
	if err != nil || len(recs) != 1 || !strings.Contains(recs[0].Summary, "(pinned to 127.0.0.1)") {
		t.Fatalf("evidence %+v err %v", recs, err)
	}
}

func TestPinTo_webSocketAndRaw(t *testing.T) {
	c, s, _ := startNamedGateway(t)
	pinned := c.PinTo("127.0.0.1")
	conn, _, err := pinned.DialWS(context.Background(), "/ws", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if _, sni := s.get(); sni != pinnedHost {
		t.Fatalf("WebSocket SNI %q", sni)
	}
	out, err := pinned.Raw(context.Background(), []byte("GET / HTTP/1.1\r\nHost: "+pinnedHost+"\r\nConnection: close\r\n\r\n"))
	if err != nil || !strings.HasPrefix(string(out), "HTTP/1.1 200") {
		t.Fatalf("raw %q err %v", out, err)
	}
}

func TestPinTo_invalidAddressFailsEveryRequest(t *testing.T) {
	c, _, _ := startNamedGateway(t)
	bad := c.PinTo("node-1")
	if _, err := bad.Send(context.Background(), Req{Path: "/"}); err == nil || !strings.Contains(err.Error(), "not an IP") {
		t.Fatalf("err %v", err)
	}
	if _, _, err := bad.DialWS(context.Background(), "/ws", "", nil); err == nil {
		t.Fatal("WebSocket through an invalid pin")
	}
	if _, err := bad.Raw(context.Background(), []byte("GET / HTTP/1.1\r\n\r\n")); err == nil {
		t.Fatal("raw through an invalid pin")
	}
	if _, err := bad.PinTo("127.0.0.1").Send(context.Background(), Req{Path: "/"}); err != nil {
		t.Fatalf("re-pinning to a valid address still fails: %v", err)
	}
}

func TestPinTo_unreachableNodeIsAnError(t *testing.T) {
	c, _, _ := startNamedGateway(t)
	// 192.0.2.0/24 is TEST-NET-1: never routed.
	ctx, cancel := context.WithTimeout(context.Background(), RawBudget/100)
	defer cancel()
	if _, err := c.PinTo("192.0.2.1").Send(ctx, Req{Path: "/"}); err == nil {
		t.Fatal("a pinned dial to an unreachable node succeeded")
	}
}

func TestNamespacePinned_urlAndPin(t *testing.T) {
	c, err := NewWithTLS("https://e2e-x.dbrsteting.bid", testTLS(), nil)
	if err != nil {
		t.Fatal(err)
	}
	n := c.NamespacePinned(&fleet.State{BaseDomain: "e2e-x.dbrsteting.bid"}, "alpha", "203.0.113.7")
	if n.BaseURL != "https://ns-alpha.e2e-x.dbrsteting.bid" || n.PinnedIP() != "203.0.113.7" {
		t.Fatalf("base %s pin %s", n.BaseURL, n.PinnedIP())
	}
}

package pubsub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/gateway/wssession"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type denyAll struct{}

func (denyAll) Revoked(*gwauth.JWTClaims) bool     { return true }
func (denyAll) RefreshRevocations(context.Context) {}

// subscribe opens a real subscriber socket authorized by claims, and returns it
// with the registry it was registered in.
func subscribe(t *testing.T, claims *gwauth.JWTClaims) (*websocket.Conn, *wssession.Registry) {
	t.Helper()
	sessions := wssession.NewRegistry(nil)
	p := NewPubSubHandlers(&mockNetworkClient{pubsub: &mockPubSubClient{}}, sessions,
		&logging.ColoredLogger{Logger: zap.NewNop()})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat")
		if claims != nil {
			ctx = context.WithValue(ctx, ctxkeys.JWT, claims)
		}
		p.WebsocketHandler(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/pubsub/ws?topic=chat", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	deadline := time.Now().Add(5 * time.Second)
	for sessions.Len() == 0 && claims != nil {
		if time.Now().After(deadline) {
			t.Fatal("the subscriber socket was never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return conn, sessions
}

func closeCode(t *testing.T, conn *websocket.Conn) int {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			return ce.Code
		}
		t.Fatalf("the socket ended without a close frame: %v", err)
	}
}

// The bug: a subscriber socket had no expiry and no revocation check at all.
func TestWebsocketHandler_aRevokedSessionClosesTheSubscriberSocket(t *testing.T) {
	now := time.Now()
	conn, sessions := subscribe(t, &gwauth.JWTClaims{
		Sub: "0xwallet", Jti: "j", Iat: now.Unix(), Exp: now.Add(15 * time.Minute).Unix(),
	})

	sessions.Sweep(now, denyAll{})

	if code := closeCode(t, conn); code != wssession.CloseRevoked {
		t.Errorf("closed with %d, want %d", code, wssession.CloseRevoked)
	}
}

func TestWebsocketHandler_anExpiredTokenClosesTheSubscriberSocket(t *testing.T) {
	now := time.Now()
	conn, sessions := subscribe(t, &gwauth.JWTClaims{
		Sub: "0xwallet", Jti: "j", Iat: now.Unix(), Exp: now.Add(time.Minute).Unix(),
	})

	sessions.Sweep(now.Add(time.Minute+wssession.ExpiryGrace+time.Second), revokedNothing{})

	if code := closeCode(t, conn); code != wssession.CloseExpired {
		t.Errorf("closed with %d, want %d", code, wssession.CloseExpired)
	}
}

// A socket opened with an API key has no token to hold it to, and is not
// registered.
func TestWebsocketHandler_aKeySocketIsNotRegistered(t *testing.T) {
	_, sessions := subscribe(t, nil)
	if sessions.Len() != 0 {
		t.Errorf("%d sockets registered for a request with no token", sessions.Len())
	}
}

type revokedNothing struct{}

func (revokedNothing) Revoked(*gwauth.JWTClaims) bool     { return false }
func (revokedNothing) RefreshRevocations(context.Context) {}

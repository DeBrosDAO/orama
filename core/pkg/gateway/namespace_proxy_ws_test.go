package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/gorilla/websocket"
)

// echoUpgrader is a namespace gateway that accepts a WebSocket and echoes one
// message.
func echoUpgrader(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		typ, msg, err := c.ReadMessage()
		if err == nil {
			_ = c.WriteMessage(typ, msg)
		}
	})
}

// frontFor serves the cluster gateway's namespace proxy for namespace "acme".
func frontFor(t *testing.T, g *Gateway) *httptest.Server {
	t.Helper()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.proxyToNamespaceGateway(w, r, "acme", namespaceProxyAuth{namespace: "acme"})
	}))
	t.Cleanup(front.Close)
	return front
}

func wsURL(front *httptest.Server) string {
	return "ws" + strings.TrimPrefix(front.URL, "http") + "/v1/webrtc/signal?room=r1"
}

// A member whose gateway refuses the connection must not fail the upgrade: the
// next member takes the socket, as it does for an HTTP request.
func TestNamespaceProxy_aWebSocketUpgradeFailsOverFromARefusedMember(t *testing.T) {
	dead, live := deadThenLive(t, echoUpgrader(t))
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: dead}, gatewayTarget{ip: "127.0.0.1", port: serverPort(live)})
	front := frontFor(t, g)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(front), nil)
	if err != nil {
		t.Fatalf("the upgrade did not fail over to the live member: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != "hello" {
		t.Fatalf("echo = %q, %v; want hello", msg, err)
	}
}

// With no member reachable the client gets a typed, retryable 503 it can tell
// from any other failure, not the old plain-text "Backend unavailable".
func TestNamespaceProxy_aWebSocketUpgradeWithNoReachableMemberAnswersTypedJSON(t *testing.T) {
	g := proxyGateway(t,
		gatewayTarget{ip: "127.0.0.1", port: freePort(t)},
		gatewayTarget{ip: "127.0.0.1", port: freePort(t)})
	front := frontFor(t, g)

	_, resp, err := websocket.DefaultDialer.Dial(wsURL(front), nil)
	if err == nil {
		t.Fatal("the upgrade succeeded with every member down")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("response = %+v, want a 503", resp)
	}
	body, _ := io.ReadAll(resp.Body)
	var env httputil.RPCErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Error == nil {
		t.Fatalf("503 body %q is not the typed error envelope: %v", body, err)
	}
	if env.Error.Code != httputil.ErrCodeNamespaceGatewayUnavailable || !env.Error.Retryable {
		t.Errorf("error = %+v, want retryable %s", env.Error, httputil.ErrCodeNamespaceGatewayUnavailable)
	}
}

// A namespace with a single member that is down still answers typed.
func TestNamespaceProxy_aWebSocketUpgradeToTheOnlyMemberDownIsRetryable(t *testing.T) {
	g := proxyGateway(t, gatewayTarget{ip: "127.0.0.1", port: freePort(t)})
	front := frontFor(t, g)

	_, resp, err := websocket.DefaultDialer.Dial(wsURL(front), nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("dial = %v, %+v; want a 503", err, resp)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
}

// A member whose circuit is open is skipped by the failover without being
// dialed.
func TestNamespaceProxy_aWebSocketFailoverSkipsAMemberWithAnOpenCircuit(t *testing.T) {
	var openHits atomic.Int32
	open := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { openHits.Add(1) }))
	defer open.Close()
	dead, live := deadThenLive(t, echoUpgrader(t))
	// The open-circuit member is addressed as "localhost" so that it has a
	// circuit of its own while every member stays reachable on this host.
	g := proxyGateway(t,
		gatewayTarget{ip: "127.0.0.1", port: dead},
		gatewayTarget{ip: "localhost", port: serverPort(open)},
		gatewayTarget{ip: "127.0.0.1", port: serverPort(live)})
	cb := g.circuitBreakers.Get("ns:localhost")
	for i := 0; i < 50 && cb.Allow(); i++ {
		cb.RecordFailure()
	}
	if cb.Allow() {
		t.Fatal("could not open the circuit of the second member")
	}
	front := frontFor(t, g)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(front), nil)
	if err != nil {
		t.Fatalf("the upgrade did not reach the live member: %v", err)
	}
	conn.Close()
	if n := openHits.Load(); n != 0 {
		t.Errorf("the member with an open circuit was dialed %d times", n)
	}
}

// tunnelWebSocket reports a dial failure without writing to the client, so the
// caller can still try another backend.
func TestTunnelWebSocket_dialFailureWritesNothing(t *testing.T) {
	g := proxyGateway(t)
	addr := "127.0.0.1:" + strconv.Itoa(freePort(t))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied, dialErr := g.tunnelWebSocket(w, r, addr)
		if proxied || dialErr == nil {
			t.Errorf("tunnelWebSocket = %v, %v; want a dial error", proxied, dialErr)
		}
		w.WriteHeader(http.StatusTeapot) // still possible: nothing was written
	})
	front := httptest.NewServer(handler)
	defer front.Close()

	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(front.URL, "http"), nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusTeapot {
		t.Fatalf("dial = %v, %+v; want the caller's 418", err, resp)
	}
}

// proxyWebSocket, which the other routes use for a single backend, answers a
// dial failure with the typed retryable 503 as well.
func TestProxyWebSocket_dialFailureIsTypedRetryable(t *testing.T) {
	g := proxyGateway(t)
	addr := "127.0.0.1:" + strconv.Itoa(freePort(t))
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.proxyWebSocket(w, r, addr) {
			t.Error("proxyWebSocket reported success for a dead backend")
		}
	}))
	defer front.Close()

	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(front.URL, "http"), nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("dial = %v, %+v; want a 503", err, resp)
	}
	body, _ := io.ReadAll(resp.Body)
	var env httputil.RPCErrorEnvelope
	if json.Unmarshal(body, &env) != nil || env.Error == nil || !env.Error.Retryable {
		t.Errorf("503 body %q is not a retryable typed error", body)
	}
}

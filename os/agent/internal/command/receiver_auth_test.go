package command

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const testAgentToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// Restarting any service on any OramaOS node took one unauthenticated POST
// from anywhere that could route to it. The receiver bound every interface
// while a comment claimed it was WireGuard only, and no handler checked
// anything at all.
func TestAuthenticated_refusesWithoutAToken(t *testing.T) {
	r := NewReceiver(nil, "10.0.0.4", testAgentToken)

	var reached bool
	handler := r.authenticated(func(w http.ResponseWriter, _ *http.Request) { reached = true })

	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest(http.MethodPost, "/v1/agent/command", strings.NewReader(`{"action":"restart"}`)))

	if reached {
		t.Fatal("an unauthenticated request reached the handler")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
}

func TestAuthenticated_refusesTheWrongToken(t *testing.T) {
	r := NewReceiver(nil, "10.0.0.4", testAgentToken)

	var reached bool
	handler := r.authenticated(func(w http.ResponseWriter, _ *http.Request) { reached = true })

	for _, presented := range []string{
		"Bearer wrong",
		"Bearer ",
		testAgentToken[:len(testAgentToken)-1] + "0",
		"Basic " + testAgentToken,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/agent/command", nil)
		req.Header.Set(HeaderAuthorization, presented)
		handler(w, req)

		if reached {
			t.Fatalf("%q was accepted", presented)
		}
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%q answered %d, want 401", presented, w.Code)
		}
	}
}

func TestAuthenticated_acceptsTheNodesToken(t *testing.T) {
	r := NewReceiver(nil, "10.0.0.4", testAgentToken)

	var reached bool
	handler := r.authenticated(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/command", nil)
	req.Header.Set(HeaderAuthorization, "Bearer "+testAgentToken)
	handler(w, req)

	if !reached {
		t.Fatalf("the gateway's own token was refused: %d %s", w.Code, w.Body.String())
	}
}

// The address it binds is the node's overlay address, not every interface.
// The constant it used was ":9998", under a comment claiming WireGuard only.
func TestNewReceiver_keepsTheOverlayAddress(t *testing.T) {
	r := NewReceiver(nil, " 10.0.0.4 ", testAgentToken)

	if r.wgIP != "10.0.0.4" {
		t.Errorf("overlay address %q, want it trimmed and kept", r.wgIP)
	}
}

// A receiver with no address or no token must not start. Listening on
// everything with no credential is the state this change exists to end, so
// falling back to it on a misconfiguration would undo the whole thing.
//
// The address is checked before it is joined with the port on purpose:
// net.JoinHostPort("", "9998") is ":9998", a perfectly non-empty string that
// binds every interface. Guarding the joined value would have let an empty
// overlay address through.
func TestListen_refusesToStartWithoutAnAddressOrAToken(t *testing.T) {
	for _, r := range []*Receiver{
		NewReceiver(nil, "", testAgentToken),
		NewReceiver(nil, "  ", testAgentToken),
		NewReceiver(nil, "10.0.0.4", ""),
		NewReceiver(nil, "", ""),
	} {
		done := make(chan struct{})
		go func() {
			r.Listen()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("a receiver with address %q and token %q started serving", r.wgIP, r.token)
		}
		if r.server != nil {
			t.Errorf("a receiver with address %q and token %q built a server", r.wgIP, r.token)
		}
	}
}

// Every route the receiver serves has to be authenticated, not just the one
// that restarts services. /v1/agent/logs reads service logs and
// /v1/agent/status enumerates what is running on the node.
//
// This walks the source rather than the mux, because a route registered
// without the wrapper is exactly the mistake it exists to catch and a mux
// cannot be asked which of its handlers is wrapped.
func TestEveryRouteIsAuthenticated(t *testing.T) {
	src, err := os.ReadFile("receiver.go")
	if err != nil {
		t.Fatalf("read receiver.go: %v", err)
	}

	routes := 0
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "mux.HandleFunc(") {
			continue
		}
		routes++
		if !strings.Contains(trimmed, "r.authenticated(") {
			t.Errorf("this route is served without a credential:\n  %s", trimmed)
		}
	}

	if routes != 4 {
		t.Errorf("found %d routes, expected 4 (command, status, health, logs) — "+
			"if one was added or removed, say so here", routes)
	}
}

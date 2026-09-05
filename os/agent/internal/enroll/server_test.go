package enroll

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func completeRequest(t *testing.T, code string, payload any) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sealed, err := Seal(testCode, body)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/agent/enroll/complete", strings.NewReader(sealed))
	if code != "" {
		r.Header.Set(HeaderEnrollmentCode, code)
	}
	return r
}

// The endpoint used to accept any POST at all: reaching a booting node before
// its operator's gateway did was enough to enrol it into another cluster, with
// another cluster's WireGuard peers and cluster secret.
func TestCompleteHandler_refusesWithoutTheCode(t *testing.T) {
	s := NewServer("")
	enrolled := make(chan *Result, 1)
	w := httptest.NewRecorder()

	s.completeHandler(testCode, "token", enrolled)(w, completeRequest(t, "", Result{NodeID: "attacker"}))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
	select {
	case r := <-enrolled:
		t.Errorf("the node was enrolled as %q by a caller with no code", r.NodeID)
	default:
	}
}

func TestCompleteHandler_refusesTheWrongCode(t *testing.T) {
	s := NewServer("")
	enrolled := make(chan *Result, 1)
	w := httptest.NewRecorder()

	s.completeHandler(testCode, "token", enrolled)(w,
		completeRequest(t, "00000000000000000000", Result{NodeID: "attacker"}))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
	select {
	case <-enrolled:
		t.Error("the node was enrolled by a caller with the wrong code")
	default:
	}
}

func TestCompleteHandler_acceptsTheOperatorsGateway(t *testing.T) {
	s := NewServer("")
	enrolled := make(chan *Result, 1)
	w := httptest.NewRecorder()

	want := Result{NodeID: "node-1", ClusterSecret: "the-secret", WireGuardConfig: "[Interface]"}
	s.completeHandler(testCode, "the-agent-token", enrolled)(w, completeRequest(t, testCode, want))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", w.Code, w.Body.String())
	}

	select {
	case got := <-enrolled:
		if got.NodeID != want.NodeID || got.ClusterSecret != want.ClusterSecret {
			t.Errorf("enrolled with %+v, want %+v", got, want)
		}
	default:
		t.Fatal("the configuration never reached the boot sequence")
	}
}

// The agent mints its own credential and hands it back sealed. The gateway
// presents it on every later command; anyone watching the exchange must not
// learn it.
func TestCompleteHandler_returnsTheAgentTokenSealed(t *testing.T) {
	s := NewServer("")
	enrolled := make(chan *Result, 1)
	w := httptest.NewRecorder()

	s.completeHandler(testCode, "the-agent-token", enrolled)(w,
		completeRequest(t, testCode, Result{NodeID: "node-1"}))

	if strings.Contains(w.Body.String(), "the-agent-token") {
		t.Fatal("the agent token is readable in the response")
	}

	opened, err := Open(testCode, w.Body.String())
	if err != nil {
		t.Fatalf("the response did not open: %v", err)
	}
	var resp completionResponse
	if err := json.Unmarshal(opened, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AgentToken != "the-agent-token" {
		t.Errorf("agent token %q", resp.AgentToken)
	}
}

// A payload sealed under a different code cannot be opened, so a caller who
// somehow learned the code header but not the code itself gets nowhere.
func TestCompleteHandler_refusesAPayloadItCannotOpen(t *testing.T) {
	s := NewServer("")
	enrolled := make(chan *Result, 1)
	w := httptest.NewRecorder()

	sealed, err := Seal("00000000000000000000", []byte(`{"node_id":"attacker"}`))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/agent/enroll/complete", strings.NewReader(sealed))
	r.Header.Set(HeaderEnrollmentCode, testCode)

	s.completeHandler(testCode, "token", enrolled)(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
	select {
	case <-enrolled:
		t.Error("a payload that did not decrypt enrolled the node")
	default:
	}
}

func TestCompleteHandler_refusesOtherMethods(t *testing.T) {
	s := NewServer("")
	enrolled := make(chan *Result, 1)
	w := httptest.NewRecorder()

	r := httptest.NewRequest(http.MethodGet, "/v1/agent/enroll/complete", nil)
	r.Header.Set(HeaderEnrollmentCode, testCode)
	s.completeHandler(testCode, "token", enrolled)(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status %d, want 405", w.Code)
	}
}

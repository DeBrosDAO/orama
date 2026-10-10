package enroll

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func completeRequest(t *testing.T, sealUnder string, payload any) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sealed, err := Seal(sealUnder, body)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return httptest.NewRequest(http.MethodPost, "/v1/agent/enroll/complete", strings.NewReader(sealed))
}

// The endpoint used to accept any POST at all: reaching a booting node before
// its operator's gateway did was enough to enrol it into another cluster, with
// another cluster's WireGuard peers and cluster secret.
func TestCompleteHandler_refusesAPayloadItCannotOpen(t *testing.T) {
	s := NewServer()
	enrolled := make(chan *Result, 1)
	w := httptest.NewRecorder()

	s.completeHandler(testCode, "token", enrolled)(w,
		completeRequest(t, "00000000000000000000", Result{NodeID: "attacker"}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
	select {
	case r := <-enrolled:
		t.Errorf("the node was enrolled as %q by a caller who did not know the code", r.NodeID)
	default:
	}
}

func TestCompleteHandler_acceptsTheOperatorsGateway(t *testing.T) {
	s := NewServer()
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

// Successful decrypt is the proof. A header carrying the code used to put the
// seal key on the wire next to the ciphertext; the handler must not need one.
func TestCompleteHandler_doesNotRequireACodeHeader(t *testing.T) {
	s := NewServer()
	enrolled := make(chan *Result, 1)
	w := httptest.NewRecorder()

	r := completeRequest(t, testCode, Result{NodeID: "node-1"})
	if r.Header.Get("X-Orama-Enrollment-Code") != "" {
		t.Fatal("the test request still carries the enrollment-code header")
	}
	s.completeHandler(testCode, "token", enrolled)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", w.Code, w.Body.String())
	}
	select {
	case <-enrolled:
	default:
		t.Fatal("a payload that decrypted was refused because no header was sent")
	}
}

// The agent mints its own credential and hands it back sealed. The gateway
// presents it on every later command; anyone watching the exchange must not
// learn it.
func TestCompleteHandler_returnsTheAgentTokenSealed(t *testing.T) {
	s := NewServer()
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

func TestCompleteHandler_refusesOtherMethods(t *testing.T) {
	s := NewServer()
	enrolled := make(chan *Result, 1)
	w := httptest.NewRecorder()

	r := httptest.NewRequest(http.MethodGet, "/v1/agent/enroll/complete", nil)
	s.completeHandler(testCode, "token", enrolled)(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status %d, want 405", w.Code)
	}
}

// A second POST that decrypts used to land on a size-1 channel after a 200,
// so the first writer won and the second blocked. The second is 409.
func TestCompleteHandler_refusesASecondComplete(t *testing.T) {
	s := NewServer()
	enrolled := make(chan *Result, 2)
	h := s.completeHandler(testCode, "token", enrolled)

	first := httptest.NewRecorder()
	h(first, completeRequest(t, testCode, Result{NodeID: "first"}))
	if first.Code != http.StatusOK {
		t.Fatalf("first status %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	h(second, completeRequest(t, testCode, Result{NodeID: "second"}))
	if second.Code != http.StatusConflict {
		t.Fatalf("second status %d, want 409", second.Code)
	}

	select {
	case got := <-enrolled:
		if got.NodeID != "first" {
			t.Errorf("enrolled as %q, want first", got.NodeID)
		}
	default:
		t.Fatal("the first completion never enrolled the node")
	}
	select {
	case got := <-enrolled:
		t.Errorf("a second completion enrolled the node as %q", got.NodeID)
	default:
	}
}

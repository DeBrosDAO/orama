package webrtc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/turn"
)

func testHandlers() *WebRTCHandlers {
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	return NewWebRTCHandlers(
		logger,
		"", // defaults to 127.0.0.1 in tests
		8443,
		"turn.ns-test.dbrs.space",
		"test-secret-key-32bytes-long!!!!",
		nil, // No actual proxy in tests
	)
}

func requestWithNamespace(method, path, namespace string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, namespace)
	return req.WithContext(ctx)
}

// --- Credentials handler tests ---

func TestCredentialsHandler_Success(t *testing.T) {
	h := testHandlers()
	req := requestWithNamespace("POST", "/v1/webrtc/turn/credentials", "test-ns")
	w := httptest.NewRecorder()

	h.CredentialsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["username"] == nil || result["username"] == "" {
		t.Error("expected non-empty username")
	}
	if result["password"] == nil || result["password"] == "" {
		t.Error("expected non-empty password")
	}
	if result["ttl"] == nil {
		t.Error("expected ttl field")
	}
	// bugboard #155: TTL must be the long-lived shared default (24h), not the
	// old 10-minute value that expired mid-call under relay-only media.
	wantTTL := float64(int(turn.DefaultCredentialTTL.Seconds()))
	ttl, ok := result["ttl"].(float64)
	if !ok || ttl != wantTTL {
		t.Errorf("ttl = %v, want %v", result["ttl"], wantTTL)
	}
	uris, ok := result["uris"].([]interface{})
	if !ok || len(uris) != 3 {
		t.Errorf("uris count = %v, want 3", result["uris"])
	}
}

// TURNS cert fix: turns:…:5349 must advertise the single-label TLS host (covered
// by the *.<base> wildcard cert) while plain turn:…:3478 keeps the legacy host.
func TestCredentialsHandler_TURNSUsesTLSHost(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	h := NewWebRTCHandlers(logger, "", 8443, "turn.ns-test.dbrs.space", "test-secret-key-32bytes-long!!!!", nil)
	h.SetTURNSTLSDomain(turn.TLSHostFromLegacyTURNHost("turn.ns-test.dbrs.space")) // "turn-test.dbrs.space"

	req := requestWithNamespace("POST", "/v1/webrtc/turn/credentials", "test-ns")
	w := httptest.NewRecorder()
	h.CredentialsHandler(w, req)

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	uris, _ := result["uris"].([]interface{})
	var udp, turns string
	for _, u := range uris {
		s, _ := u.(string)
		switch {
		case strings.HasPrefix(s, "turn:") && strings.Contains(s, "udp"):
			udp = s
		case strings.HasPrefix(s, "turns:"):
			turns = s
		}
	}
	if udp != "turn:turn.ns-test.dbrs.space:3478?transport=udp" {
		t.Errorf("UDP URI should keep the legacy host; got %q", udp)
	}
	if turns != "turns:turn-test.dbrs.space:5349" {
		t.Errorf("TURNS URI must use the single-label TLS host; got %q", turns)
	}
}

// When no TLS host is set, turns:5349 falls back to the legacy host (pre-fix
// behavior) — so a partial rollout never emits an empty/malformed TURNS URI.
func TestCredentialsHandler_TURNSFallsBackWhenTLSHostUnset(t *testing.T) {
	h := testHandlers() // no SetTURNSTLSDomain call
	req := requestWithNamespace("POST", "/v1/webrtc/turn/credentials", "test-ns")
	w := httptest.NewRecorder()
	h.CredentialsHandler(w, req)

	var result map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&result)
	uris, _ := result["uris"].([]interface{})
	found := false
	for _, u := range uris {
		if s, _ := u.(string); s == "turns:turn.ns-test.dbrs.space:5349" {
			found = true
		}
	}
	if !found {
		t.Errorf("TURNS URI should fall back to the legacy host when TLS host unset; uris=%v", uris)
	}
}

// TestCredentialsHandler_ExpiryEncodesLongTTL asserts the issued coturn-style
// username encodes an expiry ~24h out (bugboard #155 verify signal: the leading
// unix timestamp must be now+DefaultCredentialTTL, not now+10min).
func TestCredentialsHandler_ExpiryEncodesLongTTL(t *testing.T) {
	h := testHandlers()
	req := requestWithNamespace("POST", "/v1/webrtc/turn/credentials", "test-ns")
	w := httptest.NewRecorder()

	before := time.Now()
	h.CredentialsHandler(w, req)

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	username, _ := result["username"].(string)
	parts := strings.SplitN(username, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("username %q is not <expiry>:<namespace>", username)
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("expiry %q not an int: %v", parts[0], err)
	}
	wantMin := before.Add(turn.DefaultCredentialTTL).Unix() - 5
	wantMax := time.Now().Add(turn.DefaultCredentialTTL).Unix() + 5
	if expiry < wantMin || expiry > wantMax {
		t.Errorf("expiry = %d, want ~now+%s (in [%d,%d])", expiry, turn.DefaultCredentialTTL, wantMin, wantMax)
	}
}

func TestCredentialsHandler_MethodNotAllowed(t *testing.T) {
	h := testHandlers()
	req := requestWithNamespace("GET", "/v1/webrtc/turn/credentials", "test-ns")
	w := httptest.NewRecorder()

	h.CredentialsHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestCredentialsHandler_NoNamespace(t *testing.T) {
	h := testHandlers()
	req := httptest.NewRequest("POST", "/v1/webrtc/turn/credentials", nil)
	w := httptest.NewRecorder()

	h.CredentialsHandler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestCredentialsHandler_NoTURNSecret(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	h := NewWebRTCHandlers(logger, "", 8443, "turn.test.dbrs.space", "", nil)

	req := requestWithNamespace("POST", "/v1/webrtc/turn/credentials", "test-ns")
	w := httptest.NewRecorder()

	h.CredentialsHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// --- Signal handler tests ---

func TestSignalHandler_NoNamespace(t *testing.T) {
	h := testHandlers()
	req := httptest.NewRequest("GET", "/v1/webrtc/signal", nil)
	w := httptest.NewRecorder()

	h.SignalHandler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestSignalHandler_NoSFUPort(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	h := NewWebRTCHandlers(logger, "", 0, "", "secret", nil)

	req := requestWithNamespace("GET", "/v1/webrtc/signal", "test-ns")
	w := httptest.NewRecorder()

	h.SignalHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestSignalHandler_NoProxyFunc(t *testing.T) {
	h := testHandlers() // proxyWebSocket is nil
	req := requestWithNamespace("GET", "/v1/webrtc/signal", "test-ns")
	w := httptest.NewRecorder()

	h.SignalHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// --- Rooms handler tests ---

func TestRoomsHandler_MethodNotAllowed(t *testing.T) {
	h := testHandlers()
	req := requestWithNamespace("POST", "/v1/webrtc/rooms", "test-ns")
	w := httptest.NewRecorder()

	h.RoomsHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestRoomsHandler_NoNamespace(t *testing.T) {
	h := testHandlers()
	req := httptest.NewRequest("GET", "/v1/webrtc/rooms", nil)
	w := httptest.NewRecorder()

	h.RoomsHandler(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestRoomsHandler_NoSFUPort(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	h := NewWebRTCHandlers(logger, "", 0, "", "secret", nil)

	req := requestWithNamespace("GET", "/v1/webrtc/rooms", "test-ns")
	w := httptest.NewRecorder()

	h.RoomsHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestRoomsHandler_SFUProxySuccess(t *testing.T) {
	// Start a mock SFU health endpoint
	mockSFU := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","rooms":3}`))
	}))
	defer mockSFU.Close()

	// Extract port from mock server
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	// Parse port from mockSFU.URL (format: http://127.0.0.1:PORT)
	var port int
	for i := len(mockSFU.URL) - 1; i >= 0; i-- {
		if mockSFU.URL[i] == ':' {
			p := mockSFU.URL[i+1:]
			for _, c := range p {
				port = port*10 + int(c-'0')
			}
			break
		}
	}

	h := NewWebRTCHandlers(logger, "", port, "", "secret", nil)
	req := requestWithNamespace("GET", "/v1/webrtc/rooms", "test-ns")
	w := httptest.NewRecorder()

	h.RoomsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if body != `{"status":"ok","rooms":3}` {
		t.Errorf("body = %q, want %q", body, `{"status":"ok","rooms":3}`)
	}
}

// --- Helper tests ---

func TestResolveNamespaceFromRequest(t *testing.T) {
	// With namespace
	req := requestWithNamespace("GET", "/test", "my-namespace")
	ns := resolveNamespaceFromRequest(req)
	if ns != "my-namespace" {
		t.Errorf("namespace = %q, want %q", ns, "my-namespace")
	}

	// Without namespace
	req = httptest.NewRequest("GET", "/test", nil)
	ns = resolveNamespaceFromRequest(req)
	if ns != "" {
		t.Errorf("namespace = %q, want empty", ns)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "bad request")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var result map[string]string
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if result["error"] != "bad request" {
		t.Errorf("error = %q, want %q", result["error"], "bad request")
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

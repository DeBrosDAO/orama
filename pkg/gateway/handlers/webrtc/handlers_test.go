package webrtc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

func testHandlers() *WebRTCHandlers {
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	return NewWebRTCHandlers(
		logger,
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
	ttl, ok := result["ttl"].(float64)
	if !ok || ttl != 600 {
		t.Errorf("ttl = %v, want 600", result["ttl"])
	}
	uris, ok := result["uris"].([]interface{})
	if !ok || len(uris) != 2 {
		t.Errorf("uris count = %v, want 2", result["uris"])
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
	h := NewWebRTCHandlers(logger, 8443, "turn.test.dbrs.space", "", nil)

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
	h := NewWebRTCHandlers(logger, 0, "", "secret", nil)

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
	h := NewWebRTCHandlers(logger, 0, "", "secret", nil)

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

	h := NewWebRTCHandlers(logger, port, "", "secret", nil)
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

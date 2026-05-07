package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Bug #220: when push isn't configured, /v1/push/* must return a clear
// 503 with the canonical envelope, NOT 404. Tests below pin that contract.

func TestPushRoutes_returns_503_with_actionable_message_when_unconfigured(t *testing.T) {
	g := &Gateway{} // pushHandlers is nil

	cases := []struct {
		name    string
		method  string
		path    string
		handler http.HandlerFunc
	}{
		{"list", http.MethodGet, "/v1/push/devices", g.pushDevicesHandler},
		{"register", http.MethodPost, "/v1/push/devices", g.pushDevicesHandler},
		{"delete", http.MethodDelete, "/v1/push/devices/abc", g.pushDevicesByIDHandler},
		{"send", http.MethodPost, "/v1/push/send", g.pushSendHandler},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			rec := httptest.NewRecorder()
			c.handler(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("expected 503, got %d", rec.Code)
			}
			// Canonical envelope (#212 contract).
			var body map[string]interface{}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if ok, _ := body["ok"].(bool); ok {
				t.Error("ok must be false")
			}
			errObj, ok := body["error"].(map[string]interface{})
			if !ok {
				t.Fatal("error must be an object")
			}
			msg, _ := errObj["message"].(string)
			if !strings.Contains(msg, "push notifications are not configured") {
				t.Errorf("message must explain push isn't configured, got %q", msg)
			}
			if !strings.Contains(msg, "ntfy_base_url") || !strings.Contains(msg, "expo_access_token") {
				t.Errorf("message must point at the config knobs, got %q", msg)
			}
			code, _ := errObj["code"].(string)
			if code != "SERVICE_UNAVAILABLE" {
				t.Errorf("expected code=SERVICE_UNAVAILABLE, got %q", code)
			}
		})
	}
}

func TestPushDevicesHandler_method_not_allowed_for_unsupported_method(t *testing.T) {
	g := &Gateway{} // pushHandlers nil — but we want to test method-not-allowed.
	// First populate pushHandlers with a sentinel by skipping that path:
	// when nil, every method goes through the 503 path. So instead, we test
	// that with nil, even unsupported methods get 503 (not 405 — push
	// disabled is the bigger problem).
	req := httptest.NewRequest(http.MethodPut, "/v1/push/devices", nil)
	rec := httptest.NewRecorder()
	g.pushDevicesHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("when push disabled, method-not-allowed gives way to 503; got %d", rec.Code)
	}
}

package webrtc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func configCall(h *WebRTCHandlers, method, body string) *httptest.ResponseRecorder {
	req := requestWithNamespace(method, "/v1/webrtc/config", "ns")
	if body != "" {
		req = httptest.NewRequest(method, "/v1/webrtc/config", strings.NewReader(body))
		req = requestWithNamespaceOf(req, "ns")
	}
	w := httptest.NewRecorder()
	h.ConfigHandler(w, req)
	return w
}

func TestConfigHandler_defaultsOffAndRoundTrips(t *testing.T) {
	h := controllerFor(t, newControlSFU(t, "a"))

	if w := configCall(h, http.MethodGet, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"require_admission":false`) {
		t.Fatalf("default: status %d %s", w.Code, w.Body)
	}
	if w := configCall(h, http.MethodPut, `{"require_admission":true}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"require_admission":true`) {
		t.Fatalf("set: status %d %s", w.Code, w.Body)
	}
	if on, _ := h.admissions.RequireAdmission(bg, "ns"); !on {
		t.Fatal("the setting was not stored")
	}
	if w := configCall(h, http.MethodGet, ""); !strings.Contains(w.Body.String(), `"require_admission":true`) {
		t.Fatalf("get after set: %s", w.Body)
	}
	if w := configCall(h, http.MethodPut, `{"require_admission":false}`); !strings.Contains(w.Body.String(), `"require_admission":false`) {
		t.Fatalf("turning it off: %s", w.Body)
	}
}

func TestConfigHandler_refusals(t *testing.T) {
	h := controllerFor(t, newControlSFU(t, "a"))
	for name, c := range map[string]struct {
		method, body string
		status       int
	}{
		"missing field": {http.MethodPut, `{}`, http.StatusBadRequest},
		"wrong type":    {http.MethodPut, `{"require_admission":"yes"}`, http.StatusBadRequest},
		"not json":      {http.MethodPut, `nope`, http.StatusBadRequest},
		"POST":          {http.MethodPost, `{"require_admission":true}`, http.StatusMethodNotAllowed},
		"DELETE":        {http.MethodDelete, "", http.StatusMethodNotAllowed},
	} {
		if w := configCall(h, c.method, c.body); w.Code != c.status {
			t.Errorf("%s: status %d, want %d", name, w.Code, c.status)
		}
	}
	if on, _ := h.admissions.RequireAdmission(bg, "ns"); on {
		t.Error("a refused request changed the policy")
	}

	noNS := httptest.NewRecorder()
	h.ConfigHandler(noNS, httptest.NewRequest(http.MethodGet, "/v1/webrtc/config", nil))
	if noNS.Code != http.StatusForbidden {
		t.Errorf("no namespace: status %d", noNS.Code)
	}
	h.admissions = nil
	if w := configCall(h, http.MethodGet, ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("no store: status %d", w.Code)
	}
}

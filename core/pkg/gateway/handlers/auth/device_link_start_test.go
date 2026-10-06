package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A device with no wallet starts a link with its public key and is told the id
// the gateway derived from it, so it can check that id against its own
// thumbprint before it shows the user code anywhere.
func TestDeviceAuthorizationHandler_aKeyedStartNamesTheDevice(t *testing.T) {
	f := newFlow(t)
	d := newTestDeviceKey(t)

	code, body := f.do(f.h.DeviceAuthorizationHandler, http.MethodPost, "/v1/auth/device",
		map[string]any{"namespace": flowNamespace, "device_key": json.RawMessage(d.jwk), "device_label": "phone"}, nil)

	if code != http.StatusOK {
		t.Fatalf("status = %d, body %v", code, body)
	}
	if body["device_id"] != d.id {
		t.Errorf("device_id = %v, want %s", body["device_id"], d.id)
	}
	if body["device_code"] == nil || body["user_code"] == nil {
		t.Errorf("a pending link carries its codes: %v", body)
	}
}

func TestDeviceAuthorizationHandler_aKeylessStartNamesNoDevice(t *testing.T) {
	f := newFlow(t)

	code, body := f.do(f.h.DeviceAuthorizationHandler, http.MethodPost, "/v1/auth/device",
		map[string]any{"namespace": flowNamespace}, nil)

	if code != http.StatusOK {
		t.Fatalf("status = %d, body %v", code, body)
	}
	if _, present := body["device_id"]; present {
		t.Errorf("a wallet login has no device: %v", body)
	}
}

func TestDeviceAuthorizationHandler_aMalformedKeyIsRefused(t *testing.T) {
	f := newFlow(t)

	code, body := f.do(f.h.DeviceAuthorizationHandler, http.MethodPost, "/v1/auth/device",
		map[string]any{"namespace": flowNamespace, "device_key": map[string]any{"kty": "oct"}}, nil)

	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, body %v, want 400", code, body)
	}
}

// startChunked posts body to the device start with no declared length (-1), as
// a chunked or proxied body arrives.
func (f *flow) startChunked(body string) (int, map[string]any) {
	f.t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/device", strings.NewReader(body))
	r.ContentLength = -1
	r = r.WithContext(context.WithValue(r.Context(), CtxKeyNamespaceOverride, flowNamespace))
	rec := httptest.NewRecorder()
	f.h.DeviceAuthorizationHandler(rec, r)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// A chunked body is read, so the device key in it is not lost. Judging the body
// by ContentLength > 0 dropped it: the start succeeded but named no device.
func TestDeviceAuthorizationHandler_aChunkedStartKeepsItsKey(t *testing.T) {
	f := newFlow(t)
	d := newTestDeviceKey(t)
	raw, _ := json.Marshal(map[string]any{"namespace": flowNamespace, "device_key": json.RawMessage(d.jwk)})

	code, body := f.startChunked(string(raw))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %v", code, body)
	}
	if body["device_id"] != d.id {
		t.Errorf("device_id = %v, want %s", body["device_id"], d.id)
	}
}

func TestDeviceAuthorizationHandler_anEmptyChunkedBodyIsAKeylessStart(t *testing.T) {
	f := newFlow(t)
	if code, body := f.startChunked(""); code != http.StatusOK {
		t.Fatalf("status = %d, body %v", code, body)
	}
}

func TestDeviceAuthorizationHandler_aMalformedChunkedBodyIsRefused(t *testing.T) {
	f := newFlow(t)
	if code, body := f.startChunked(`{"namespace":`); code != http.StatusBadRequest {
		t.Fatalf("status = %d, body %v, want 400", code, body)
	}
}

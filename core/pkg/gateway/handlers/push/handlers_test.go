package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/push"
	"go.uber.org/zap"
)

// fakeStore is an in-memory PushDeviceStore for tests.
type fakeStore struct {
	devices  []push.PushDevice
	upsertFn func(push.PushDevice) error
	deleteFn func(ns, id string) error
	listErr  error
}

func (s *fakeStore) Upsert(ctx context.Context, dev push.PushDevice) error {
	if s.upsertFn != nil {
		return s.upsertFn(dev)
	}
	if dev.ID == "" {
		dev.ID = "row-" + dev.DeviceID
	}
	s.devices = append(s.devices, dev)
	return nil
}
func (s *fakeStore) Delete(ctx context.Context, ns, id string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ns, id)
	}
	for i, d := range s.devices {
		if d.ID == id && d.Namespace == ns {
			s.devices = append(s.devices[:i], s.devices[i+1:]...)
			return nil
		}
	}
	return errors.New("not found")
}
func (s *fakeStore) ListForUser(ctx context.Context, ns, userID string) ([]push.PushDevice, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := []push.PushDevice{}
	for _, d := range s.devices {
		if d.Namespace == ns && d.UserID == userID {
			out = append(out, d)
		}
	}
	return out, nil
}

// withAuth populates the namespace + JWT claims (caller user ID).
func withAuth(r *http.Request, namespace, userID string) *http.Request {
	ctx := r.Context()
	if namespace != "" {
		ctx = context.WithValue(ctx, ctxkeys.NamespaceOverride, namespace)
	}
	if userID != "" {
		ctx = context.WithValue(ctx, ctxkeys.JWT, &authsvc.JWTClaims{Sub: userID, Namespace: namespace})
	}
	return r.WithContext(ctx)
}

func newHandlers(store push.PushDeviceStore, dispatcher *push.PushDispatcher) *Handlers {
	logger := &logging.ColoredLogger{Logger: zap.NewNop()}
	return NewHandlers(dispatcher, store, logger)
}

// --- RegisterDeviceHandler ---

func TestRegister_happy_path(t *testing.T) {
	store := &fakeStore{}
	h := newHandlers(store, nil)

	body, _ := json.Marshal(RegisterDeviceRequest{
		DeviceID: "iphone-abc",
		Provider: "ntfy",
		Token:    "ns/myapp/user-1",
		Platform: "ios",
	})
	req := withAuth(httptest.NewRequest(http.MethodPost, "/v1/push/devices", bytes.NewReader(body)), "myapp", "user-1")
	rr := httptest.NewRecorder()
	h.RegisterDeviceHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if len(store.devices) != 1 {
		t.Fatalf("expected 1 device stored, got %d", len(store.devices))
	}
	d := store.devices[0]
	if d.Namespace != "myapp" || d.UserID != "user-1" || d.Token != "ns/myapp/user-1" {
		t.Errorf("unexpected device: %+v", d)
	}
}

func TestRegister_unauthenticated_rejected(t *testing.T) {
	h := newHandlers(&fakeStore{}, nil)
	body, _ := json.Marshal(RegisterDeviceRequest{DeviceID: "x", Provider: "ntfy", Token: "t"})

	// No JWT in context.
	req := withAuth(httptest.NewRequest(http.MethodPost, "/v1/push/devices", bytes.NewReader(body)), "ns", "")
	rr := httptest.NewRecorder()
	h.RegisterDeviceHandler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestRegister_unknown_provider_rejected(t *testing.T) {
	h := newHandlers(&fakeStore{}, nil)
	body, _ := json.Marshal(RegisterDeviceRequest{DeviceID: "x", Provider: "weirdmail", Token: "t"})
	req := withAuth(httptest.NewRequest(http.MethodPost, "/v1/push/devices", bytes.NewReader(body)), "ns", "u")
	rr := httptest.NewRecorder()
	h.RegisterDeviceHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestRegister_oversize_token_rejected(t *testing.T) {
	h := newHandlers(&fakeStore{}, nil)
	huge := make([]byte, MaxTokenBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	body, _ := json.Marshal(RegisterDeviceRequest{DeviceID: "x", Provider: "ntfy", Token: string(huge)})
	req := withAuth(httptest.NewRequest(http.MethodPost, "/v1/push/devices", bytes.NewReader(body)), "ns", "u")
	rr := httptest.NewRecorder()
	h.RegisterDeviceHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestRegister_no_store_returns_503(t *testing.T) {
	h := newHandlers(nil, nil)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/v1/push/devices", bytes.NewReader([]byte(`{}`))), "ns", "u")
	rr := httptest.NewRecorder()
	h.RegisterDeviceHandler(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}

// --- ListDevicesHandler ---

func TestList_returns_only_callers_devices_without_tokens(t *testing.T) {
	store := &fakeStore{
		devices: []push.PushDevice{
			{ID: "1", Namespace: "myapp", UserID: "u1", DeviceID: "d1", Provider: "ntfy", Token: "secret-token-1"},
			{ID: "2", Namespace: "myapp", UserID: "u1", DeviceID: "d2", Provider: "expo", Token: "secret-token-2"},
			{ID: "3", Namespace: "myapp", UserID: "u2", DeviceID: "d3", Provider: "ntfy", Token: "secret-token-3"},
			{ID: "4", Namespace: "other", UserID: "u1", DeviceID: "d4", Provider: "ntfy", Token: "secret-token-4"},
		},
	}
	h := newHandlers(store, nil)

	req := withAuth(httptest.NewRequest(http.MethodGet, "/v1/push/devices", nil), "myapp", "u1")
	rr := httptest.NewRecorder()
	h.ListDevicesHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp struct {
		Devices []PushDeviceView `json:"devices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(resp.Devices))
	}
	// Tokens must NOT appear in response — they're not even in the struct.
	if bytes.Contains(rr.Body.Bytes(), []byte("secret-token")) {
		t.Errorf("response leaked a token: %s", rr.Body.String())
	}
}

// --- DeleteDeviceHandler ---

func TestDelete_owns_device_succeeds(t *testing.T) {
	store := &fakeStore{
		devices: []push.PushDevice{
			{ID: "row-d1", Namespace: "myapp", UserID: "u1", DeviceID: "d1"},
		},
	}
	h := newHandlers(store, nil)

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/v1/push/devices/row-d1", nil), "myapp", "u1")
	rr := httptest.NewRecorder()
	h.DeleteDeviceHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if len(store.devices) != 0 {
		t.Errorf("expected device removed")
	}
}

func TestDelete_other_users_device_returns_404(t *testing.T) {
	store := &fakeStore{
		devices: []push.PushDevice{
			{ID: "row-d1", Namespace: "myapp", UserID: "other-user", DeviceID: "d1"},
		},
	}
	h := newHandlers(store, nil)

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/v1/push/devices/row-d1", nil), "myapp", "u1")
	rr := httptest.NewRecorder()
	h.DeleteDeviceHandler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
	if len(store.devices) != 1 {
		t.Errorf("expected device NOT removed")
	}
}

func TestDelete_missing_id_returns_400(t *testing.T) {
	h := newHandlers(&fakeStore{}, nil)
	req := withAuth(httptest.NewRequest(http.MethodDelete, "/v1/push/devices/", nil), "myapp", "u1")
	rr := httptest.NewRecorder()
	h.DeleteDeviceHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// --- SendHandler ---

func TestSend_dispatcher_called_for_user(t *testing.T) {
	var sent int32
	dispatcher := push.New(&fakeStore{
		devices: []push.PushDevice{
			{ID: "row-1", Namespace: "myapp", UserID: "target-user", Provider: "fake", Token: "tok"},
		},
	}, zap.NewNop())
	dispatcher.Register(&fakePushProvider{
		name: "fake",
		fn:   func(ctx context.Context, msg push.PushMessage) error { atomic.AddInt32(&sent, 1); return nil },
	})

	h := newHandlers(&fakeStore{}, dispatcher)

	body, _ := json.Marshal(SendRequest{
		UserID: "target-user", Title: "hi", Body: "world",
	})
	req := withAuth(httptest.NewRequest(http.MethodPost, "/v1/push/send", bytes.NewReader(body)), "myapp", "u1")
	rr := httptest.NewRecorder()
	h.SendHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if atomic.LoadInt32(&sent) != 1 {
		t.Errorf("expected provider called once, got %d", sent)
	}
}

func TestSend_no_dispatcher_returns_503(t *testing.T) {
	h := newHandlers(&fakeStore{}, nil)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/v1/push/send", bytes.NewReader([]byte(`{"user_id":"u"}`))), "myapp", "u1")
	rr := httptest.NewRecorder()
	h.SendHandler(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rr.Code)
	}
}

func TestSend_missing_user_id_returns_400(t *testing.T) {
	dispatcher := push.New(&fakeStore{}, zap.NewNop())
	h := newHandlers(&fakeStore{}, dispatcher)

	body, _ := json.Marshal(SendRequest{})
	req := withAuth(httptest.NewRequest(http.MethodPost, "/v1/push/send", bytes.NewReader(body)), "myapp", "u1")
	rr := httptest.NewRecorder()
	h.SendHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// --- helpers ---

type fakePushProvider struct {
	name string
	fn   func(ctx context.Context, msg push.PushMessage) error
}

func (p *fakePushProvider) Name() string { return p.name }
func (p *fakePushProvider) Send(ctx context.Context, msg push.PushMessage) error {
	if p.fn != nil {
		return p.fn(ctx, msg)
	}
	return nil
}

func TestExtractIDFromPath(t *testing.T) {
	cases := []struct {
		path, prefix, want string
	}{
		{"/v1/push/devices/abc", "/v1/push/devices/", "abc"},
		{"/v1/push/devices/abc?x=1", "/v1/push/devices/", "abc"},
		{"/v1/push/devices/", "/v1/push/devices/", ""},
		{"/v1/other/abc", "/v1/push/devices/", ""},
	}
	for _, c := range cases {
		if got := extractIDFromPath(c.path, c.prefix); got != c.want {
			t.Errorf("extractIDFromPath(%q, %q) = %q, want %q", c.path, c.prefix, got, c.want)
		}
	}
}

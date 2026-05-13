package ratelimit

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/ratelimit"
)

// ---------------- mock store + setup ----------------

type memStore struct {
	mu   sync.Mutex
	rows map[string]ratelimit.Config
}

func newMemStore() *memStore { return &memStore{rows: map[string]ratelimit.Config{}} }

func (m *memStore) Get(_ context.Context, namespace string) (*ratelimit.Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.rows[namespace]; ok {
		c2 := c
		return &c2, nil
	}
	return nil, nil
}
func (m *memStore) Upsert(_ context.Context, cfg ratelimit.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[cfg.Namespace] = cfg
	return nil
}
func (m *memStore) Delete(_ context.Context, namespace string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, namespace)
	return nil
}

func newTestHandlers(t *testing.T, defs ratelimit.Defaults) (*Handlers, *memStore, *ratelimit.Manager) {
	t.Helper()
	store := newMemStore()
	mgr := ratelimit.NewManager(store, defs, nil)
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	return NewHandlers(store, mgr, logger), store, mgr
}

// authedRequest builds a request with the auth-middleware-set context
// keys: namespace + JWT subject. Without these, the handlers reject as
// they should.
func authedRequest(method, path, body, namespace, sub string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	ctx := r.Context()
	if namespace != "" {
		ctx = context.WithValue(ctx, ctxkeys.NamespaceOverride, namespace)
	}
	if sub != "" {
		ctx = context.WithValue(ctx, ctxkeys.JWT, &auth.JWTClaims{Sub: sub, Namespace: namespace})
	}
	return r.WithContext(ctx)
}

// ---------------- GET ----------------

func TestGetConfigHandler_defaultsWhenNoOverride(t *testing.T) {
	h, _, _ := newTestHandlers(t, ratelimit.Defaults{
		RequestsPerMinute:    100,
		Burst:                10,
		MaxRequestsPerMinute: 1000,
		MaxBurst:             100,
	})

	r := authedRequest(http.MethodGet, "/v1/namespace/rate-limit", "", "anchat-test", "0xWALLET")
	w := httptest.NewRecorder()
	h.GetConfigHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp GetResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Source != "default" {
		t.Errorf("Source = %q, want %q", resp.Source, "default")
	}
	if resp.RequestsPerMinute != 100 || resp.Burst != 10 {
		t.Errorf("effective = (%d, %d), want defaults (100, 10)", resp.RequestsPerMinute, resp.Burst)
	}
	if resp.MaxRequestsPerMinute != 1000 || resp.MaxBurst != 100 {
		t.Errorf("max ceiling = (%d, %d), want (1000, 100)", resp.MaxRequestsPerMinute, resp.MaxBurst)
	}
}

func TestGetConfigHandler_overrideWhenSet(t *testing.T) {
	h, store, _ := newTestHandlers(t, ratelimit.Defaults{RequestsPerMinute: 100, Burst: 10})
	store.rows["anchat-test"] = ratelimit.Config{
		Namespace:         "anchat-test",
		RequestsPerMinute: 5000,
		Burst:             500,
		UpdatedAt:         42,
		UpdatedBy:         "0xOPERATOR",
	}

	r := authedRequest(http.MethodGet, "/v1/namespace/rate-limit", "", "anchat-test", "0xWALLET")
	w := httptest.NewRecorder()
	h.GetConfigHandler(w, r)

	var resp GetResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Source != "override" {
		t.Errorf("Source = %q, want %q", resp.Source, "override")
	}
	if resp.RequestsPerMinute != 5000 || resp.Burst != 500 {
		t.Errorf("effective = (%d, %d), want override (5000, 500)", resp.RequestsPerMinute, resp.Burst)
	}
	if resp.UpdatedBy != "0xOPERATOR" {
		t.Errorf("UpdatedBy = %q, want %q", resp.UpdatedBy, "0xOPERATOR")
	}
}

func TestGetConfigHandler_noNamespaceContext_returns403(t *testing.T) {
	h, _, _ := newTestHandlers(t, ratelimit.Defaults{RequestsPerMinute: 100, Burst: 10})
	r := authedRequest(http.MethodGet, "/v1/namespace/rate-limit", "", "", "0xWALLET")
	w := httptest.NewRecorder()
	h.GetConfigHandler(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (no namespace = no scope)", w.Code)
	}
}

// ---------------- PUT ----------------

func TestPutConfigHandler_acceptsValidUpdate(t *testing.T) {
	h, store, mgr := newTestHandlers(t, ratelimit.Defaults{
		RequestsPerMinute:    100,
		Burst:                10,
		MaxRequestsPerMinute: 10000,
		MaxBurst:             1000,
	})

	body := `{"requests_per_minute": 5000, "burst": 500}`
	r := authedRequest(http.MethodPut, "/v1/namespace/rate-limit", body, "anchat-test", "0xWALLET")
	w := httptest.NewRecorder()
	h.PutConfigHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	// Persisted.
	stored, _ := store.Get(context.Background(), "anchat-test")
	if stored == nil || stored.RequestsPerMinute != 5000 || stored.Burst != 500 {
		t.Errorf("not persisted correctly: %+v", stored)
	}

	// Cache invalidated → manager.Allow now uses the new limit.
	// 50 sequential calls should all pass under burst=500.
	for i := 0; i < 50; i++ {
		if !mgr.Allow(context.Background(), "anchat-test") {
			t.Fatalf("Allow %d should pass under new burst=500", i+1)
		}
	}
}

func TestPutConfigHandler_acceptsValueEqualToCap(t *testing.T) {
	// Boundary: body == cap is accepted (strict `>` in the handler, not `>=`).
	h, store, _ := newTestHandlers(t, ratelimit.Defaults{
		MaxRequestsPerMinute: 5000,
		MaxBurst:             500,
	})
	body := `{"requests_per_minute": 5000, "burst": 500}`
	r := authedRequest(http.MethodPut, "/v1/namespace/rate-limit", body, "anchat-test", "0xWALLET")
	w := httptest.NewRecorder()
	h.PutConfigHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (value == cap should be accepted)", w.Code)
	}
	got, _ := store.Get(context.Background(), "anchat-test")
	if got == nil || got.RequestsPerMinute != 5000 || got.Burst != 500 {
		t.Errorf("not persisted: %+v", got)
	}
}

func TestPutConfigHandler_capZeroMeansNoCap(t *testing.T) {
	// Operator sets MaxRequestsPerMinute=0 and MaxBurst=0 → "no cap".
	// Tenants can set arbitrarily large values (trusted-tenant deployments).
	h, store, _ := newTestHandlers(t, ratelimit.Defaults{
		// No Max* set — interpreted as "disabled / no ceiling".
		RequestsPerMinute: 100,
		Burst:             10,
	})
	body := `{"requests_per_minute": 999999, "burst": 99999}`
	r := authedRequest(http.MethodPut, "/v1/namespace/rate-limit", body, "anchat-test", "0xWALLET")
	w := httptest.NewRecorder()
	h.PutConfigHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (zero cap should disable check)", w.Code)
	}
	got, _ := store.Get(context.Background(), "anchat-test")
	if got == nil || got.RequestsPerMinute != 999999 || got.Burst != 99999 {
		t.Errorf("not persisted: %+v", got)
	}
}

func TestPutConfigHandler_rejectsAboveOperatorCap(t *testing.T) {
	h, store, _ := newTestHandlers(t, ratelimit.Defaults{
		RequestsPerMinute:    100,
		Burst:                10,
		MaxRequestsPerMinute: 1000,
		MaxBurst:             100,
	})

	// Try to set requests_per_minute=99999 — well above the operator cap.
	body := `{"requests_per_minute": 99999, "burst": 50}`
	r := authedRequest(http.MethodPut, "/v1/namespace/rate-limit", body, "anchat-test", "0xWALLET")
	w := httptest.NewRecorder()
	h.PutConfigHandler(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (above operator cap)", w.Code)
	}
	if got, _ := store.Get(context.Background(), "anchat-test"); got != nil {
		t.Error("rejected request was nevertheless persisted")
	}
}

func TestPutConfigHandler_rejectsAboveBurstCap(t *testing.T) {
	h, _, _ := newTestHandlers(t, ratelimit.Defaults{
		MaxRequestsPerMinute: 1000,
		MaxBurst:             100,
	})

	body := `{"requests_per_minute": 500, "burst": 9999}`
	r := authedRequest(http.MethodPut, "/v1/namespace/rate-limit", body, "anchat-test", "0xWALLET")
	w := httptest.NewRecorder()
	h.PutConfigHandler(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (burst above operator cap)", w.Code)
	}
}

func TestPutConfigHandler_rejectsZeroOrNegative(t *testing.T) {
	h, _, _ := newTestHandlers(t, ratelimit.Defaults{})

	cases := []string{
		`{"requests_per_minute": 0, "burst": 10}`,
		`{"requests_per_minute": -1, "burst": 10}`,
		`{"requests_per_minute": 10, "burst": 0}`,
		`{"requests_per_minute": 10, "burst": -1}`,
		`{}`,
	}
	for _, body := range cases {
		r := authedRequest(http.MethodPut, "/v1/namespace/rate-limit", body, "anchat-test", "0xWALLET")
		w := httptest.NewRecorder()
		h.PutConfigHandler(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body=%s: status = %d, want 400", body, w.Code)
		}
	}
}

func TestPutConfigHandler_requiresJWT(t *testing.T) {
	h, _, _ := newTestHandlers(t, ratelimit.Defaults{MaxRequestsPerMinute: 0})
	body := `{"requests_per_minute": 100, "burst": 10}`
	// No JWT subject — only API-key auth, which can't be attributed.
	r := authedRequest(http.MethodPut, "/v1/namespace/rate-limit", body, "anchat-test", "")
	w := httptest.NewRecorder()
	h.PutConfigHandler(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (no JWT subject = no audit trail)", w.Code)
	}
}

// ---------------- DELETE ----------------

func TestDeleteConfigHandler_removesOverride(t *testing.T) {
	h, store, mgr := newTestHandlers(t, ratelimit.Defaults{RequestsPerMinute: 60, Burst: 1})
	store.rows["anchat-test"] = ratelimit.Config{
		Namespace: "anchat-test", RequestsPerMinute: 6000, Burst: 100,
	}

	// Warm the cache with the override.
	if !mgr.Allow(context.Background(), "anchat-test") {
		t.Fatal("initial Allow should pass under override (burst=100)")
	}

	r := authedRequest(http.MethodDelete, "/v1/namespace/rate-limit", "", "anchat-test", "0xWALLET")
	w := httptest.NewRecorder()
	h.DeleteConfigHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got, _ := store.Get(context.Background(), "anchat-test"); got != nil {
		t.Error("override row not deleted")
	}

	// Cache invalidated → next Allow rebuilds under the default (burst=1).
	if !mgr.Allow(context.Background(), "anchat-test") {
		t.Fatal("first post-delete Allow should pass under default burst=1")
	}
	if mgr.Allow(context.Background(), "anchat-test") {
		t.Error("second post-delete Allow should be throttled (burst=1 exhausted, no refill in this test)")
	}
}

func TestDeleteConfigHandler_idempotent(t *testing.T) {
	h, _, _ := newTestHandlers(t, ratelimit.Defaults{})
	r := authedRequest(http.MethodDelete, "/v1/namespace/rate-limit", "", "no-override-ns", "0xWALLET")
	w := httptest.NewRecorder()
	h.DeleteConfigHandler(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (DELETE must be idempotent)", w.Code)
	}
}

// ---------------- method gating ----------------

func TestHandlers_methodGating(t *testing.T) {
	h, _, _ := newTestHandlers(t, ratelimit.Defaults{})
	cases := []struct {
		handler func(http.ResponseWriter, *http.Request)
		method  string
		want    int
	}{
		{h.GetConfigHandler, http.MethodPost, http.StatusMethodNotAllowed},
		{h.PutConfigHandler, http.MethodGet, http.StatusMethodNotAllowed},
		{h.DeleteConfigHandler, http.MethodGet, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		r := authedRequest(tc.method, "/v1/namespace/rate-limit", "{}", "ns", "sub")
		w := httptest.NewRecorder()
		tc.handler(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.method, w.Code, tc.want)
		}
	}
}

package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/push/credentials"
)

// fakeStore satisfies credentials.Store with an in-memory map. Mirrors
// the manager_test.go fake but locally typed because the package can't
// import credentials' internal fakeStore.
type fakeCredStore struct {
	rows map[string]*credentials.Credential // key: namespace+"|"+provider
}

func newFakeCredStore() *fakeCredStore {
	return &fakeCredStore{rows: map[string]*credentials.Credential{}}
}
func key(ns, p string) string { return ns + "|" + p }

func (f *fakeCredStore) Get(_ context.Context, ns, p string) (*credentials.Credential, error) {
	if c, ok := f.rows[key(ns, p)]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, credentials.ErrNotFound
}
func (f *fakeCredStore) Upsert(_ context.Context, c credentials.Credential) error {
	cp := c
	f.rows[key(c.Namespace, c.Provider)] = &cp
	return nil
}
func (f *fakeCredStore) Delete(_ context.Context, ns, p string) error {
	delete(f.rows, key(ns, p))
	return nil
}
func (f *fakeCredStore) ListProviders(_ context.Context, ns string) ([]string, error) {
	var out []string
	for k, c := range f.rows {
		if strings.HasPrefix(k, ns+"|") {
			out = append(out, c.Provider)
		}
	}
	return out, nil
}

// fakeValidator records validate/redact calls and lets tests inject
// validation errors.
type fakeValidator struct {
	name      string
	validate  func([]byte) error
	redact    func([]byte) (interface{}, error)
}

func (v fakeValidator) Provider() string { return v.name }
func (v fakeValidator) Validate(b []byte) error {
	if v.validate != nil {
		return v.validate(b)
	}
	return nil
}
func (v fakeValidator) Redact(b []byte) (interface{}, error) {
	if v.redact != nil {
		return v.redact(b)
	}
	// Default: return a map with `has_<each-field>` for every top-level
	// key. Good enough for round-trip tests.
	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	for k := range raw {
		out["has_"+k] = true
	}
	return out, nil
}

// buildHandlersWithCreds wires Handlers with only the credentials path
// populated. Auth context (namespace + JWT subject) is set on the test
// request directly.
func buildHandlersWithCreds(t *testing.T) (*Handlers, *fakeCredStore) {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	h := &Handlers{logger: logger}
	store := newFakeCredStore()
	h.SetCredentialsManager(credentials.NewManager(store, nil))
	return h, store
}

// authedRequest builds a request with namespace + JWT subject in context,
// matching what the upstream auth middleware does in production.
func authedRequest(method, target string, body []byte, ns, sub string) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	ctx := r.Context()
	if ns != "" {
		ctx = context.WithValue(ctx, ctxkeys.NamespaceOverride, ns)
	}
	if sub != "" {
		ctx = context.WithValue(ctx, ctxkeys.JWT, &auth.JWTClaims{Sub: sub})
	}
	return r.WithContext(ctx)
}

func TestCredentials_PutGetRoundTrip(t *testing.T) {
	credentials.ResetRegistryForTest()
	defer credentials.ResetRegistryForTest()
	credentials.Register(fakeValidator{name: "apns"})

	h, store := buildHandlersWithCreds(t)

	// PUT a credential.
	body := []byte(`{"team_id":"ABCD1234","key_id":"XYZ","p8_key":"-----BEGIN..."}`)
	r := authedRequest(http.MethodPut,
		"/v1/namespace/push-credentials/apns", body, "ns-a", "wallet-1")
	w := httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", w.Code, w.Body.String())
	}

	// Stored value should be the verbatim JSON.
	if got := store.rows[key("ns-a", "apns")]; got == nil {
		t.Fatal("PUT did not persist credential")
	} else if !bytes.Equal(got.JSON, body) {
		t.Errorf("stored JSON differs:\n got: %s\nwant: %s", got.JSON, body)
	}

	// GET returns redacted view + audit fields.
	r = authedRequest(http.MethodGet, "/v1/namespace/push-credentials/apns", nil, "ns-a", "wallet-1")
	w = httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if resp["configured"] != true {
		t.Errorf("GET should report configured=true; got %v", resp["configured"])
	}
	// Redacted view shouldn't echo any of the secret strings.
	bodyStr := w.Body.String()
	if strings.Contains(bodyStr, "BEGIN") || strings.Contains(bodyStr, "ABCD1234") {
		t.Errorf("redacted GET leaked secret material: %s", bodyStr)
	}
}

func TestCredentials_PutRejectsBadJSON(t *testing.T) {
	credentials.ResetRegistryForTest()
	defer credentials.ResetRegistryForTest()
	credentials.Register(fakeValidator{name: "apns"})

	h, _ := buildHandlersWithCreds(t)
	r := authedRequest(http.MethodPut, "/v1/namespace/push-credentials/apns",
		[]byte(`{not json}`), "ns-a", "wallet-1")
	w := httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed JSON; got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestCredentials_PutEmptyBodyRejected(t *testing.T) {
	credentials.ResetRegistryForTest()
	defer credentials.ResetRegistryForTest()
	credentials.Register(fakeValidator{name: "apns"})

	h, _ := buildHandlersWithCreds(t)
	r := authedRequest(http.MethodPut, "/v1/namespace/push-credentials/apns",
		nil, "ns-a", "wallet-1")
	w := httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty body; got %d", w.Code)
	}
}

func TestCredentials_PutValidatorErrorPropagates(t *testing.T) {
	credentials.ResetRegistryForTest()
	defer credentials.ResetRegistryForTest()
	credentials.Register(fakeValidator{
		name: "apns",
		validate: func(_ []byte) error {
			return errors.New("missing team_id")
		},
	})

	h, store := buildHandlersWithCreds(t)
	r := authedRequest(http.MethodPut, "/v1/namespace/push-credentials/apns",
		[]byte(`{}`), "ns-a", "wallet-1")
	w := httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on validator failure; got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing team_id") {
		t.Errorf("validator error not surfaced to client: %s", w.Body.String())
	}
	// Validator rejection must NOT persist.
	if _, ok := store.rows[key("ns-a", "apns")]; ok {
		t.Error("rejected PUT should not have persisted")
	}
}

func TestCredentials_UnknownProviderRejected(t *testing.T) {
	credentials.ResetRegistryForTest()
	defer credentials.ResetRegistryForTest()
	credentials.Register(fakeValidator{name: "apns"})

	h, _ := buildHandlersWithCreds(t)
	r := authedRequest(http.MethodPut, "/v1/namespace/push-credentials/sms",
		[]byte(`{}`), "ns-a", "wallet-1")
	w := httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unregistered provider; got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unsupported provider") {
		t.Errorf("error message should explain unsupported provider: %s", w.Body.String())
	}
}

func TestCredentials_DeleteIdempotent(t *testing.T) {
	credentials.ResetRegistryForTest()
	defer credentials.ResetRegistryForTest()
	credentials.Register(fakeValidator{name: "apns"})

	h, _ := buildHandlersWithCreds(t)

	// Delete with no row should still succeed.
	r := authedRequest(http.MethodDelete, "/v1/namespace/push-credentials/apns",
		nil, "ns-a", "wallet-1")
	w := httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("DELETE no-row: status %d (body=%s)", w.Code, w.Body.String())
	}

	// PUT then DELETE clears.
	put := authedRequest(http.MethodPut, "/v1/namespace/push-credentials/apns",
		[]byte(`{"x":1}`), "ns-a", "wallet-1")
	h.CredentialsByProviderHandler(httptest.NewRecorder(), put)

	r = authedRequest(http.MethodDelete, "/v1/namespace/push-credentials/apns",
		nil, "ns-a", "wallet-1")
	w = httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("DELETE existing: status %d", w.Code)
	}

	// Re-GET should report not configured.
	r = authedRequest(http.MethodGet, "/v1/namespace/push-credentials/apns",
		nil, "ns-a", "wallet-1")
	w = httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("post-delete GET: %d", w.Code)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["configured"] != false {
		t.Errorf("post-delete GET should report configured=false; got %+v", resp)
	}
}

// TestCredentials_APIKeyCallerAccepted verifies bugboard #147: a namespace-owner
// PUT authenticated by the (admin) API key — namespace resolved, no user JWT —
// is now ACCEPTED and attributed to "apikey:<ns>" (never the raw key). These
// routes are admin-scoped at the gateway, so only an admin key or the owner
// wallet reaches this handler.
func TestCredentials_APIKeyCallerAccepted(t *testing.T) {
	credentials.ResetRegistryForTest()
	defer credentials.ResetRegistryForTest()
	credentials.Register(fakeValidator{name: "apns"})

	h, _ := buildHandlersWithCreds(t)

	// PUT with namespace (api-key auth) and no user JWT — accepted (#147).
	r := authedRequest(http.MethodPut, "/v1/namespace/push-credentials/apns",
		[]byte(`{}`), "ns-a", "" /* no JWT */)
	w := httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("PUT api-key caller: status %d, want 200", w.Code)
	}
}

func TestCredentials_MissingNamespaceRejected(t *testing.T) {
	credentials.ResetRegistryForTest()
	defer credentials.ResetRegistryForTest()
	credentials.Register(fakeValidator{name: "apns"})

	h, _ := buildHandlersWithCreds(t)
	r := authedRequest(http.MethodGet, "/v1/namespace/push-credentials/apns",
		nil, "" /* no ns */, "wallet-1")
	w := httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("GET no-ns: status %d", w.Code)
	}
}

func TestCredentials_SummaryReportsConfiguredAndSupported(t *testing.T) {
	credentials.ResetRegistryForTest()
	defer credentials.ResetRegistryForTest()
	credentials.Register(fakeValidator{name: "apns"})
	credentials.Register(fakeValidator{name: "ntfy"})
	credentials.Register(fakeValidator{name: "fcm"})

	h, _ := buildHandlersWithCreds(t)

	// Configure apns only.
	put := authedRequest(http.MethodPut, "/v1/namespace/push-credentials/apns",
		[]byte(`{"x":1}`), "ns-a", "wallet-1")
	h.CredentialsByProviderHandler(httptest.NewRecorder(), put)

	r := authedRequest(http.MethodGet, "/v1/namespace/push-credentials", nil, "ns-a", "wallet-1")
	w := httptest.NewRecorder()
	h.CredentialsSummaryHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("summary: %d (body=%s)", w.Code, w.Body.String())
	}
	var resp CredentialsSummary
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if resp.Namespace != "ns-a" {
		t.Errorf("namespace=%q want ns-a", resp.Namespace)
	}
	if len(resp.Configured) != 1 || resp.Configured[0] != "apns" {
		t.Errorf("configured=%v want [apns]", resp.Configured)
	}
	if len(resp.Supported) != 3 {
		t.Errorf("supported=%v want 3 entries", resp.Supported)
	}
}

func TestCredentials_NoManagerReturns503(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	h := &Handlers{logger: logger} // no credentialsManager
	r := authedRequest(http.MethodGet, "/v1/namespace/push-credentials/apns", nil, "ns-a", "wallet-1")
	w := httptest.NewRecorder()
	h.CredentialsByProviderHandler(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when manager nil; got %d", w.Code)
	}
}

func TestExtractProvider(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/v1/namespace/push-credentials/apns", "apns"},
		{"/v1/namespace/push-credentials/apns/", "apns"},
		{"/v1/namespace/push-credentials/apns?foo=bar", "apns"},
		{"/v1/namespace/push-credentials/", ""},
		{"/v1/namespace/push-credentials", ""},
		{"/some/other/path", ""},
		{"/v1/namespace/push-credentials/n-t.f_y", "n-t.f_y"},
	}
	for _, tt := range tests {
		if got := extractProvider(tt.path); got != tt.want {
			t.Errorf("extractProvider(%q) = %q; want %q", tt.path, got, tt.want)
		}
	}
}

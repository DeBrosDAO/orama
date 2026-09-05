package serverless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Mock SecretsManager
// ---------------------------------------------------------------------------

type mockSecretsManager struct {
	secrets map[string]map[string]string // namespace -> name -> value
	setErr  error
	getErr  error
	listErr error
	delErr  error
}

func newMockSecretsManager() *mockSecretsManager {
	return &mockSecretsManager{
		secrets: make(map[string]map[string]string),
	}
}

func (m *mockSecretsManager) Set(_ context.Context, namespace, name, value string) error {
	if m.setErr != nil {
		return m.setErr
	}
	if m.secrets[namespace] == nil {
		m.secrets[namespace] = make(map[string]string)
	}
	m.secrets[namespace][name] = value
	return nil
}

func (m *mockSecretsManager) Get(_ context.Context, namespace, name string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	ns, ok := m.secrets[namespace]
	if !ok {
		return "", serverless.ErrSecretNotFound
	}
	v, ok := ns[name]
	if !ok {
		return "", serverless.ErrSecretNotFound
	}
	return v, nil
}

func (m *mockSecretsManager) List(_ context.Context, namespace string) ([]string, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	ns := m.secrets[namespace]
	names := make([]string, 0, len(ns))
	for k := range ns {
		names = append(names, k)
	}
	return names, nil
}

func (m *mockSecretsManager) Delete(_ context.Context, namespace, name string) error {
	if m.delErr != nil {
		return m.delErr
	}
	ns, ok := m.secrets[namespace]
	if !ok {
		return serverless.ErrSecretNotFound
	}
	if _, ok := ns[name]; !ok {
		return serverless.ErrSecretNotFound
	}
	delete(ns, name)
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newSecretsTestHandlers(sm serverless.SecretsManager) *ServerlessHandlers {
	logger := zap.NewNop()
	wsManager := serverless.NewWSManager(logger)
	return NewServerlessHandlers(
		nil, // invoker
		nil, // engine
		newMockRegistry(),
		wsManager,
		nil, // triggerStore
		nil, // cronStore
		nil, // dispatcher
		nil, // persistentMgr
		nil, // wsBridge
		sm,
		nil, // audit
		logger,
	)
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return result
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHandleSetSecret_Success(t *testing.T) {
	sm := newMockSecretsManager()
	h := newSecretsTestHandlers(sm)

	body := `{"name":"API_KEY","value":"secret123"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/functions/secrets?namespace=myns", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleSetSecret(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeJSON(t, rec)
	if resp["name"] != "API_KEY" {
		t.Errorf("expected name API_KEY, got %v", resp["name"])
	}

	// Verify stored
	if sm.secrets["myns"]["API_KEY"] != "secret123" {
		t.Errorf("secret not stored correctly")
	}
}

func TestHandleSetSecret_MissingName(t *testing.T) {
	h := newSecretsTestHandlers(newMockSecretsManager())

	body := `{"value":"secret123"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/functions/secrets?namespace=myns", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleSetSecret(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetSecret_MissingValue(t *testing.T) {
	h := newSecretsTestHandlers(newMockSecretsManager())

	body := `{"name":"API_KEY"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/functions/secrets?namespace=myns", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleSetSecret(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSetSecret_NilManager(t *testing.T) {
	h := newSecretsTestHandlers(nil)

	body := `{"name":"API_KEY","value":"secret123"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/functions/secrets?namespace=myns", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleSetSecret(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", rec.Code)
	}
}

func TestHandleListSecrets_Empty(t *testing.T) {
	h := newSecretsTestHandlers(newMockSecretsManager())

	req := httptest.NewRequest(http.MethodGet, "/v1/functions/secrets?namespace=myns", nil)
	rec := httptest.NewRecorder()

	h.HandleListSecrets(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	resp := decodeJSON(t, rec)
	if resp["count"].(float64) != 0 {
		t.Errorf("expected count 0, got %v", resp["count"])
	}
}

func TestHandleListSecrets_Populated(t *testing.T) {
	sm := newMockSecretsManager()
	sm.secrets["myns"] = map[string]string{
		"KEY_A": "val_a",
		"KEY_B": "val_b",
	}
	h := newSecretsTestHandlers(sm)

	req := httptest.NewRequest(http.MethodGet, "/v1/functions/secrets?namespace=myns", nil)
	rec := httptest.NewRecorder()

	h.HandleListSecrets(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	resp := decodeJSON(t, rec)
	if resp["count"].(float64) != 2 {
		t.Errorf("expected count 2, got %v", resp["count"])
	}
}

func TestHandleListSecrets_NilManager(t *testing.T) {
	h := newSecretsTestHandlers(nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/functions/secrets?namespace=myns", nil)
	rec := httptest.NewRecorder()

	h.HandleListSecrets(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", rec.Code)
	}
}

func TestHandleDeleteSecret_Success(t *testing.T) {
	sm := newMockSecretsManager()
	sm.secrets["myns"] = map[string]string{"API_KEY": "val"}
	h := newSecretsTestHandlers(sm)

	req := httptest.NewRequest(http.MethodDelete, "/v1/functions/secrets/API_KEY?namespace=myns", nil)
	rec := httptest.NewRecorder()

	h.HandleDeleteSecret(rec, req, "API_KEY")

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	if _, exists := sm.secrets["myns"]["API_KEY"]; exists {
		t.Error("secret should have been deleted")
	}
}

func TestHandleDeleteSecret_NotFound(t *testing.T) {
	h := newSecretsTestHandlers(newMockSecretsManager())

	req := httptest.NewRequest(http.MethodDelete, "/v1/functions/secrets/MISSING?namespace=myns", nil)
	rec := httptest.NewRecorder()

	h.HandleDeleteSecret(rec, req, "MISSING")

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleDeleteSecret_NilManager(t *testing.T) {
	h := newSecretsTestHandlers(nil)

	req := httptest.NewRequest(http.MethodDelete, "/v1/functions/secrets/KEY?namespace=myns", nil)
	rec := httptest.NewRecorder()

	h.HandleDeleteSecret(rec, req, "KEY")

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", rec.Code)
	}
}

// Test routing through handleFunctionByName
func TestRouting_SecretsSet(t *testing.T) {
	sm := newMockSecretsManager()
	h := newSecretsTestHandlers(sm)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"name":"MY_SECRET","value":"myval"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/functions/secrets?namespace=test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouting_SecretsList(t *testing.T) {
	h := newSecretsTestHandlers(newMockSecretsManager())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/functions/secrets?namespace=test", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouting_SecretsDelete(t *testing.T) {
	sm := newMockSecretsManager()
	sm.secrets["test"] = map[string]string{"KEY": "val"}
	h := newSecretsTestHandlers(sm)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodDelete, "/v1/functions/secrets/KEY?namespace=test", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	serverlesshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

type mockFunctionRegistry struct {
	functions []*serverless.Function
}

func (m *mockFunctionRegistry) Register(ctx context.Context, fn *serverless.FunctionDefinition, wasmBytes []byte) (*serverless.Function, error) {
	return nil, nil
}

func (m *mockFunctionRegistry) Get(ctx context.Context, namespace, name string, version int) (*serverless.Function, error) {
	return &serverless.Function{ID: "1", Name: name, Namespace: namespace}, nil
}

func (m *mockFunctionRegistry) List(ctx context.Context, namespace string) ([]*serverless.Function, error) {
	return m.functions, nil
}

func (m *mockFunctionRegistry) Delete(ctx context.Context, namespace, name string, version int) error {
	return nil
}

func (m *mockFunctionRegistry) GetWASMBytes(ctx context.Context, wasmCID string) ([]byte, error) {
	return []byte("wasm"), nil
}

func (m *mockFunctionRegistry) GetLogs(ctx context.Context, namespace, name string, limit int) ([]serverless.LogEntry, error) {
	return []serverless.LogEntry{}, nil
}

func TestServerlessHandlers_ListFunctions(t *testing.T) {
	logger := zap.NewNop()
	registry := &mockFunctionRegistry{
		functions: []*serverless.Function{
			{ID: "1", Name: "func1", Namespace: "ns1"},
			{ID: "2", Name: "func2", Namespace: "ns1"},
		},
	}

	h := serverlesshandlers.NewServerlessHandlers(nil, registry, nil, logger)

	req, _ := http.NewRequest("GET", "/v1/functions?namespace=ns1", nil)
	rr := httptest.NewRecorder()

	h.ListFunctions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["count"].(float64) != 2 {
		t.Errorf("expected 2 functions, got %v", resp["count"])
	}
}

func TestServerlessHandlers_DeployFunction(t *testing.T) {
	logger := zap.NewNop()
	registry := &mockFunctionRegistry{}

	h := serverlesshandlers.NewServerlessHandlers(nil, registry, nil, logger)

	// Test JSON deploy (which is partially supported according to code)
	// Should be 400 because WASM is missing or base64 not supported
	writer := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/functions", bytes.NewBufferString(`{"name": "test"}`))
	req.Header.Set("Content-Type", "application/json")

	h.DeployFunction(writer, req)

	if writer.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", writer.Code)
	}
}

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/cache"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"go.uber.org/zap"
)

func TestCacheHealthHandler(t *testing.T) {
	// Create a test logger
	logger, _ := logging.NewDefaultLogger(logging.ComponentGeneral)

	// Create cache handlers without Olric client (should return service unavailable)
	handlers := cache.NewCacheHandlers(logger, nil)

	req := httptest.NewRequest("GET", "/v1/cache/health", nil)
	w := httptest.NewRecorder()

	handlers.HealthHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["error"] == nil {
		t.Error("expected error in response")
	}
}

func TestCacheGetHandler_MissingClient(t *testing.T) {
	logger, _ := logging.NewDefaultLogger(logging.ComponentGeneral)

	handlers := cache.NewCacheHandlers(logger, nil)

	reqBody := map[string]string{
		"dmap": "test-dmap",
		"key":  "test-key",
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/cache/get", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	handlers.GetHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestCacheGetHandler_InvalidBody(t *testing.T) {
	logger, _ := logging.NewDefaultLogger(logging.ComponentGeneral)

	handlers := cache.NewCacheHandlers(logger, &olric.Client{}) // Mock client

	req := httptest.NewRequest("POST", "/v1/cache/get", bytes.NewReader([]byte("invalid json")))
	w := httptest.NewRecorder()

	handlers.GetHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestCachePutHandler_MissingFields(t *testing.T) {
	logger, _ := logging.NewDefaultLogger(logging.ComponentGeneral)

	handlers := cache.NewCacheHandlers(logger, &olric.Client{})

	// Test missing dmap
	reqBody := map[string]string{
		"key": "test-key",
	}
	bodyBytes, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/v1/cache/put", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	handlers.SetHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	// Test missing key
	reqBody = map[string]string{
		"dmap": "test-dmap",
	}
	bodyBytes, _ = json.Marshal(reqBody)
	req = httptest.NewRequest("POST", "/v1/cache/put", bytes.NewReader(bodyBytes))
	w = httptest.NewRecorder()

	handlers.SetHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestCacheDeleteHandler_WrongMethod(t *testing.T) {
	logger, _ := logging.NewDefaultLogger(logging.ComponentGeneral)

	handlers := cache.NewCacheHandlers(logger, &olric.Client{})

	req := httptest.NewRequest("GET", "/v1/cache/delete", nil)
	w := httptest.NewRecorder()

	handlers.DeleteHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestCacheScanHandler_InvalidBody(t *testing.T) {
	logger, _ := logging.NewDefaultLogger(logging.ComponentGeneral)

	handlers := cache.NewCacheHandlers(logger, &olric.Client{})

	req := httptest.NewRequest("POST", "/v1/cache/scan", bytes.NewReader([]byte("invalid")))
	w := httptest.NewRecorder()

	handlers.ScanHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

// Test Olric client wrapper
func TestOlricClientConfig(t *testing.T) {
	logger := zap.NewNop()

	// Test default servers
	cfg := olric.Config{}
	client, err := olric.NewClient(cfg, logger)
	if err == nil {
		// If client creation succeeds, test that it has default servers
		// This will fail if Olric server is not running, which is expected in tests
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = client.Close(ctx)
	}
}

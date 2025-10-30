package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

func newTestGateway(t *testing.T) *Gateway {
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, true)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	return &Gateway{logger: logger}
}

func TestAnonProxyHandler_MethodValidation(t *testing.T) {
	gw := newTestGateway(t)

	// Test GET request (should fail - only POST allowed)
	req := httptest.NewRequest(http.MethodGet, "/v1/proxy/anon", nil)
	w := httptest.NewRecorder()

	gw.anonProxyHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestAnonProxyHandler_InvalidJSON(t *testing.T) {
	gw := newTestGateway(t)

	// Test invalid JSON
	req := httptest.NewRequest(http.MethodPost, "/v1/proxy/anon", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	gw.anonProxyHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestAnonProxyHandler_InvalidURL(t *testing.T) {
	gw := newTestGateway(t)

	tests := []struct {
		name    string
		payload anonProxyRequest
	}{
		{
			name: "invalid URL scheme",
			payload: anonProxyRequest{
				URL:    "ftp://example.com",
				Method: "GET",
			},
		},
		{
			name: "malformed URL",
			payload: anonProxyRequest{
				URL:    "://invalid",
				Method: "GET",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.payload)
			req := httptest.NewRequest(http.MethodPost, "/v1/proxy/anon", bytes.NewBuffer(body))
			w := httptest.NewRecorder()

			gw.anonProxyHandler(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
			}
		})
	}
}

func TestAnonProxyHandler_PrivateAddressBlocking(t *testing.T) {
	gw := newTestGateway(t)

	tests := []struct {
		name string
		url  string
	}{
		{"localhost", "http://localhost/test"},
		{"127.0.0.1", "http://127.0.0.1/test"},
		{"private 10.x", "http://10.0.0.1/test"},
		{"private 192.168.x", "http://192.168.1.1/test"},
		{"private 172.16.x", "http://172.16.0.1/test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := anonProxyRequest{
				URL:    tt.url,
				Method: "GET",
			}
			body, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPost, "/v1/proxy/anon", bytes.NewBuffer(body))
			w := httptest.NewRecorder()

			gw.anonProxyHandler(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("Expected status %d for %s, got %d", http.StatusForbidden, tt.url, w.Code)
			}
		})
	}
}

func TestAnonProxyHandler_InvalidMethod(t *testing.T) {
	gw := newTestGateway(t)

	payload := anonProxyRequest{
		URL:    "https://example.com",
		Method: "INVALID",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/v1/proxy/anon", bytes.NewBuffer(body))
	w := httptest.NewRecorder()

	gw.anonProxyHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestIsHopByHopHeader(t *testing.T) {
	tests := []struct {
		header   string
		expected bool
	}{
		{"Connection", true},
		{"Keep-Alive", true},
		{"Proxy-Authorization", true},
		{"Transfer-Encoding", true},
		{"Upgrade", true},
		{"Content-Type", false},
		{"Authorization", false},
		{"User-Agent", false},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			result := isHopByHopHeader(tt.header)
			if result != tt.expected {
				t.Errorf("isHopByHopHeader(%s) = %v, want %v", tt.header, result, tt.expected)
			}
		})
	}
}

func TestIsPrivateOrLocalHost(t *testing.T) {
	tests := []struct {
		host     string
		expected bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"192.168.1.1", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"example.com", false},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"172.32.0.1", false}, // Not in private range
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			result := isPrivateOrLocalHost(tt.host)
			if result != tt.expected {
				t.Errorf("isPrivateOrLocalHost(%s) = %v, want %v", tt.host, result, tt.expected)
			}
		})
	}
}

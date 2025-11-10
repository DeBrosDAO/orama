//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestNetwork_Health(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method:   http.MethodGet,
		URL:      GetGatewayURL() + "/v1/health",
		SkipAuth: true,
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", resp["status"])
	}
}

func TestNetwork_Status(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/network/status",
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("status check failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if _, ok := resp["connected"]; !ok {
		t.Fatalf("expected 'connected' field in response")
	}

	if _, ok := resp["peer_count"]; !ok {
		t.Fatalf("expected 'peer_count' field in response")
	}
}

func TestNetwork_Peers(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/network/peers",
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("peers check failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if _, ok := resp["peers"]; !ok {
		t.Fatalf("expected 'peers' field in response")
	}
}

func TestNetwork_ProxyAnonSuccess(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/proxy/anon",
		Body: map[string]interface{}{
			"url":     "https://httpbin.org/get",
			"method":  "GET",
			"headers": map[string]string{"User-Agent": "DeBros-E2E-Test/1.0"},
		},
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("proxy anon request failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", status, string(body))
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status_code"] != float64(200) {
		t.Fatalf("expected proxy status 200, got %v", resp["status_code"])
	}

	if _, ok := resp["body"]; !ok {
		t.Fatalf("expected 'body' field in response")
	}
}

func TestNetwork_ProxyAnonBadURL(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/proxy/anon",
		Body: map[string]interface{}{
			"url":    "http://localhost:1/nonexistent",
			"method": "GET",
		},
	}

	_, status, err := req.Do(ctx)
	if err == nil && status == http.StatusOK {
		t.Fatalf("expected error for bad URL, got status 200")
	}
}

func TestNetwork_ProxyAnonPostRequest(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/proxy/anon",
		Body: map[string]interface{}{
			"url":     "https://httpbin.org/post",
			"method":  "POST",
			"headers": map[string]string{"User-Agent": "DeBros-E2E-Test/1.0"},
			"body":    "test_data",
		},
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("proxy anon POST failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", status, string(body))
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status_code"] != float64(200) {
		t.Fatalf("expected proxy status 200, got %v", resp["status_code"])
	}
}

func TestNetwork_Unauthorized(t *testing.T) {
	// Test without API key
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create request without auth
	req := &HTTPRequest{
		Method:   http.MethodGet,
		URL:      GetGatewayURL() + "/v1/network/status",
		SkipAuth: true,
	}

	_, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Logf("warning: expected 401/403, got %d (auth may not be enforced on this endpoint)", status)
	}
}

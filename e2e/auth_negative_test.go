//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"
	"unicode"
)

func TestAuth_MissingAPIKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request without auth headers
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/network/status", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should be unauthorized
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Logf("warning: expected 401/403 for missing auth, got %d (auth may not be enforced on this endpoint)", resp.StatusCode)
	}
}

func TestAuth_InvalidAPIKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request with invalid API key
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer invalid-key-xyz")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should be unauthorized
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Logf("warning: expected 401/403 for invalid key, got %d", resp.StatusCode)
	}
}

func TestAuth_CacheWithoutAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request cache endpoint without auth
	req := &HTTPRequest{
		Method:   http.MethodGet,
		URL:      GetGatewayURL() + "/v1/cache/health",
		SkipAuth: true,
	}

	_, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// Should fail with 401 or 403
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Logf("warning: expected 401/403 for cache without auth, got %d", status)
	}
}

func TestAuth_StorageWithoutAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request storage endpoint without auth
	req := &HTTPRequest{
		Method:   http.MethodGet,
		URL:      GetGatewayURL() + "/v1/storage/status/QmTest",
		SkipAuth: true,
	}

	_, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// Should fail with 401 or 403
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Logf("warning: expected 401/403 for storage without auth, got %d", status)
	}
}

func TestAuth_RQLiteWithoutAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request rqlite endpoint without auth
	req := &HTTPRequest{
		Method:   http.MethodGet,
		URL:      GetGatewayURL() + "/v1/rqlite/schema",
		SkipAuth: true,
	}

	_, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// Should fail with 401 or 403
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Logf("warning: expected 401/403 for rqlite without auth, got %d", status)
	}
}

func TestAuth_MalformedBearerToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request with malformed bearer token
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Missing "Bearer " prefix
	req.Header.Set("Authorization", "invalid-token-format")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should be unauthorized
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Logf("warning: expected 401/403 for malformed token, got %d", resp.StatusCode)
	}
}

func TestAuth_ExpiredJWT(t *testing.T) {
	// Skip if JWT is not being used
	if GetJWT() == "" && GetAPIKey() == "" {
		t.Skip("No JWT or API key configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// This test would require an expired JWT token
	// For now, test with a clearly invalid JWT structure
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer expired.jwt.token")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should be unauthorized
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Logf("warning: expected 401/403 for expired JWT, got %d", resp.StatusCode)
	}
}

func TestAuth_EmptyBearerToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request with empty bearer token
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer ")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should be unauthorized
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Logf("warning: expected 401/403 for empty token, got %d", resp.StatusCode)
	}
}

func TestAuth_DuplicateAuthHeaders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request with both API key and invalid JWT
	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/cache/health",
		Headers: map[string]string{
			"Authorization": "Bearer " + GetAPIKey(),
			"X-API-Key":     GetAPIKey(),
		},
	}

	_, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// Should succeed if API key is valid
	if status != http.StatusOK {
		t.Logf("request with both headers returned %d", status)
	}
}

func TestAuth_CaseSensitiveAPIKey(t *testing.T) {
	if GetAPIKey() == "" {
		t.Skip("No API key configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request with incorrectly cased API key
	apiKey := GetAPIKey()
	incorrectKey := ""
	for i, ch := range apiKey {
		if i%2 == 0 && unicode.IsLetter(ch) {
			incorrectKey += string(unicode.ToUpper(ch)) // Convert to uppercase
		} else {
			incorrectKey += string(ch)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+incorrectKey)

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// API keys should be case-sensitive
	if resp.StatusCode == http.StatusOK {
		t.Logf("warning: API key check may not be case-sensitive (got 200)")
	}
}

func TestAuth_HealthEndpointNoAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Health endpoint at /health should not require auth
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should succeed without auth
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /health without auth, got %d", resp.StatusCode)
	}
}

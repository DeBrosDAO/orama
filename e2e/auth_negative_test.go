//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/require"
)

// =============================================================================
// STRICT AUTHENTICATION NEGATIVE TESTS
// These tests verify that authentication is properly enforced.
// Tests FAIL if unauthenticated/invalid requests are allowed through.
// =============================================================================

func TestAuth_MissingAPIKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request protected endpoint without auth headers
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	require.NoError(t, err, "FAIL: Could not create request")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	require.NoError(t, err, "FAIL: Request failed")
	defer resp.Body.Close()

	// STRICT: Must reject requests without authentication
	require.True(t, resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden,
		"FAIL: Protected endpoint allowed request without auth - expected 401/403, got %d", resp.StatusCode)
	t.Logf("  ✓ Missing API key correctly rejected with status %d", resp.StatusCode)
}

func TestAuth_InvalidAPIKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request with invalid API key
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	require.NoError(t, err, "FAIL: Could not create request")

	req.Header.Set("Authorization", "Bearer invalid-key-xyz-123456789")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	require.NoError(t, err, "FAIL: Request failed")
	defer resp.Body.Close()

	// STRICT: Must reject invalid API keys
	require.True(t, resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden,
		"FAIL: Invalid API key was accepted - expected 401/403, got %d", resp.StatusCode)
	t.Logf("  ✓ Invalid API key correctly rejected with status %d", resp.StatusCode)
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
	require.NoError(t, err, "FAIL: Request failed")

	// STRICT: Cache endpoint must require authentication
	require.True(t, status == http.StatusUnauthorized || status == http.StatusForbidden,
		"FAIL: Cache endpoint accessible without auth - expected 401/403, got %d", status)
	t.Logf("  ✓ Cache endpoint correctly requires auth (status %d)", status)
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
	require.NoError(t, err, "FAIL: Request failed")

	// STRICT: Storage endpoint must require authentication
	require.True(t, status == http.StatusUnauthorized || status == http.StatusForbidden,
		"FAIL: Storage endpoint accessible without auth - expected 401/403, got %d", status)
	t.Logf("  ✓ Storage endpoint correctly requires auth (status %d)", status)
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
	require.NoError(t, err, "FAIL: Request failed")

	// STRICT: RQLite endpoint must require authentication
	require.True(t, status == http.StatusUnauthorized || status == http.StatusForbidden,
		"FAIL: RQLite endpoint accessible without auth - expected 401/403, got %d", status)
	t.Logf("  ✓ RQLite endpoint correctly requires auth (status %d)", status)
}

func TestAuth_MalformedBearerToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request with malformed bearer token (missing "Bearer " prefix)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	require.NoError(t, err, "FAIL: Could not create request")

	req.Header.Set("Authorization", "invalid-token-format-no-bearer")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	require.NoError(t, err, "FAIL: Request failed")
	defer resp.Body.Close()

	// STRICT: Must reject malformed authorization headers
	require.True(t, resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden,
		"FAIL: Malformed auth header accepted - expected 401/403, got %d", resp.StatusCode)
	t.Logf("  ✓ Malformed bearer token correctly rejected (status %d)", resp.StatusCode)
}

func TestAuth_ExpiredJWT(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test with a clearly invalid JWT structure
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	require.NoError(t, err, "FAIL: Could not create request")

	req.Header.Set("Authorization", "Bearer expired.jwt.token.invalid")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	require.NoError(t, err, "FAIL: Request failed")
	defer resp.Body.Close()

	// STRICT: Must reject invalid/expired JWT tokens
	require.True(t, resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden,
		"FAIL: Invalid JWT accepted - expected 401/403, got %d", resp.StatusCode)
	t.Logf("  ✓ Invalid JWT correctly rejected (status %d)", resp.StatusCode)
}

func TestAuth_EmptyBearerToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request with empty bearer token
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	require.NoError(t, err, "FAIL: Could not create request")

	req.Header.Set("Authorization", "Bearer ")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	require.NoError(t, err, "FAIL: Request failed")
	defer resp.Body.Close()

	// STRICT: Must reject empty bearer tokens
	require.True(t, resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden,
		"FAIL: Empty bearer token accepted - expected 401/403, got %d", resp.StatusCode)
	t.Logf("  ✓ Empty bearer token correctly rejected (status %d)", resp.StatusCode)
}

func TestAuth_DuplicateAuthHeaders(t *testing.T) {
	if GetAPIKey() == "" {
		t.Skip("No API key configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request with both valid API key in Authorization header
	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/cache/health",
		Headers: map[string]string{
			"Authorization": "Bearer " + GetAPIKey(),
			"X-API-Key":     GetAPIKey(),
		},
	}

	_, status, err := req.Do(ctx)
	require.NoError(t, err, "FAIL: Request failed")

	// Should succeed since we have a valid API key
	require.Equal(t, http.StatusOK, status,
		"FAIL: Valid API key rejected when multiple auth headers present - got %d", status)
	t.Logf("  ✓ Duplicate auth headers with valid key succeeds (status %d)", status)
}

func TestAuth_CaseSensitiveAPIKey(t *testing.T) {
	apiKey := GetAPIKey()
	if apiKey == "" {
		t.Skip("No API key configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create incorrectly cased API key
	incorrectKey := ""
	for i, ch := range apiKey {
		if i%2 == 0 && unicode.IsLetter(ch) {
			if unicode.IsLower(ch) {
				incorrectKey += string(unicode.ToUpper(ch))
			} else {
				incorrectKey += string(unicode.ToLower(ch))
			}
		} else {
			incorrectKey += string(ch)
		}
	}

	// Skip if the key didn't change (no letters)
	if incorrectKey == apiKey {
		t.Skip("API key has no letters to change case")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/cache/health", nil)
	require.NoError(t, err, "FAIL: Could not create request")

	req.Header.Set("Authorization", "Bearer "+incorrectKey)

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	require.NoError(t, err, "FAIL: Request failed")
	defer resp.Body.Close()

	// STRICT: API keys MUST be case-sensitive
	require.True(t, resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden,
		"FAIL: API key check is not case-sensitive - modified key accepted with status %d", resp.StatusCode)
	t.Logf("  ✓ Case-modified API key correctly rejected (status %d)", resp.StatusCode)
}

func TestAuth_HealthEndpointNoAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Health endpoint at /v1/health should NOT require auth
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/health", nil)
	require.NoError(t, err, "FAIL: Could not create request")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	require.NoError(t, err, "FAIL: Request failed")
	defer resp.Body.Close()

	// Health endpoint should be publicly accessible
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"FAIL: Health endpoint should not require auth - expected 200, got %d", resp.StatusCode)
	t.Logf("  ✓ Health endpoint correctly accessible without auth")
}

func TestAuth_StatusEndpointNoAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Status endpoint at /v1/status should NOT require auth
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/status", nil)
	require.NoError(t, err, "FAIL: Could not create request")

	client := NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	require.NoError(t, err, "FAIL: Request failed")
	defer resp.Body.Close()

	// Status endpoint should be publicly accessible
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"FAIL: Status endpoint should not require auth - expected 200, got %d", resp.StatusCode)
	t.Logf("  ✓ Status endpoint correctly accessible without auth")
}

func TestAuth_DeploymentsWithoutAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request deployments endpoint without auth
	req := &HTTPRequest{
		Method:   http.MethodGet,
		URL:      GetGatewayURL() + "/v1/deployments/list",
		SkipAuth: true,
	}

	_, status, err := req.Do(ctx)
	require.NoError(t, err, "FAIL: Request failed")

	// STRICT: Deployments endpoint must require authentication
	require.True(t, status == http.StatusUnauthorized || status == http.StatusForbidden,
		"FAIL: Deployments endpoint accessible without auth - expected 401/403, got %d", status)
	t.Logf("  ✓ Deployments endpoint correctly requires auth (status %d)", status)
}

func TestAuth_SQLiteWithoutAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Request SQLite endpoint without auth
	req := &HTTPRequest{
		Method:   http.MethodGet,
		URL:      GetGatewayURL() + "/v1/db/sqlite/list",
		SkipAuth: true,
	}

	_, status, err := req.Do(ctx)
	require.NoError(t, err, "FAIL: Request failed")

	// STRICT: SQLite endpoint must require authentication
	require.True(t, status == http.StatusUnauthorized || status == http.StatusForbidden,
		"FAIL: SQLite endpoint accessible without auth - expected 401/403, got %d", status)
	t.Logf("  ✓ SQLite endpoint correctly requires auth (status %d)", status)
}

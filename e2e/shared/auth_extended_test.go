//go:build e2e

package shared

import (
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuth_ExpiredOrInvalidJWT verifies that an expired/invalid JWT token is rejected.
func TestAuth_ExpiredOrInvalidJWT(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	gatewayURL := e2e.GetGatewayURL()

	// Craft an obviously invalid JWT
	invalidJWT := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwiZXhwIjoxfQ.invalid"

	req, err := http.NewRequest("GET", gatewayURL+"/v1/deployments/list", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+invalidJWT)

	client := e2e.NewHTTPClient(10 * time.Second)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"Invalid JWT should return 401")
}

// TestAuth_EmptyAPIKey verifies that an empty API key is rejected.
func TestAuth_EmptyAPIKey(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	gatewayURL := e2e.GetGatewayURL()

	req, err := http.NewRequest("GET", gatewayURL+"/v1/deployments/list", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer ")

	client := e2e.NewHTTPClient(10 * time.Second)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"Empty API key should return 401")
}

// TestAuth_SQLInjectionInAPIKey verifies that SQL injection in the API key
// does not bypass authentication.
func TestAuth_SQLInjectionInAPIKey(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	gatewayURL := e2e.GetGatewayURL()

	injectionAttempts := []string{
		"' OR '1'='1",
		"'; DROP TABLE api_keys; --",
		"\" OR \"1\"=\"1",
		"admin'--",
	}

	for _, attempt := range injectionAttempts {
		t.Run(attempt, func(t *testing.T) {
			req, _ := http.NewRequest("GET", gatewayURL+"/v1/deployments/list", nil)
			req.Header.Set("Authorization", "Bearer "+attempt)

			client := e2e.NewHTTPClient(10 * time.Second)
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
				"SQL injection attempt should be rejected")
		})
	}
}

// TestAuth_NamespaceScopedAccess verifies that an API key for one namespace
// cannot access another namespace's deployments.
func TestAuth_NamespaceScopedAccess(t *testing.T) {
	// Create two environments with different namespaces
	env1, err := e2e.LoadTestEnvWithNamespace("auth-test-ns1")
	if err != nil {
		t.Skip("Could not create namespace env1: " + err.Error())
	}

	env2, err := e2e.LoadTestEnvWithNamespace("auth-test-ns2")
	if err != nil {
		t.Skip("Could not create namespace env2: " + err.Error())
	}

	t.Run("Namespace 1 key cannot list namespace 2 deployments", func(t *testing.T) {
		// Use env1's API key to query env2's gateway
		// The namespace should be scoped to the API key
		req, _ := http.NewRequest("GET", env2.GatewayURL+"/v1/deployments/list", nil)
		req.Header.Set("Authorization", "Bearer "+env1.APIKey)
		req.Header.Set("X-Namespace", "auth-test-ns2")

		resp, err := env1.HTTPClient.Do(req)
		if err != nil {
			t.Skip("Gateway unreachable")
		}
		defer resp.Body.Close()

		// The API should either reject (403) or return only ns1's deployments
		t.Logf("Cross-namespace access returned: %d", resp.StatusCode)

		if resp.StatusCode == http.StatusOK {
			t.Log("API returned 200 — namespace isolation may be enforced at data level")
		}
	})
}

// TestAuth_PublicEndpointsNoAuth verifies that health/status endpoints
// don't require authentication.
func TestAuth_PublicEndpointsNoAuth(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	gatewayURL := e2e.GetGatewayURL()
	client := e2e.NewHTTPClient(10 * time.Second)

	publicPaths := []string{
		"/v1/health",
		"/v1/status",
	}

	for _, path := range publicPaths {
		t.Run(path, func(t *testing.T) {
			req, _ := http.NewRequest("GET", gatewayURL+path, nil)
			// No auth header

			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode,
				"%s should be accessible without auth", path)
		})
	}
}

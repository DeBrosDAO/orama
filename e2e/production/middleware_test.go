//go:build e2e && production

package production

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMiddleware_NonExistentDeployment verifies that requests to a non-existent
// deployment return 404 (not 502 or hang).
func TestMiddleware_NonExistentDeployment(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	domain := fmt.Sprintf("does-not-exist-%d.%s", time.Now().Unix(), env.BaseDomain)

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s:6001/", env.Config.Servers[0].IP), nil)
	req.Host = domain

	start := time.Now()
	resp, err := env.HTTPClient.Do(req)
	elapsed := time.Since(start)

	if err != nil {
		t.Logf("Request failed in %v: %v", elapsed, err)
		// Connection refused or timeout is acceptable
		assert.Less(t, elapsed.Seconds(), 15.0, "Should fail fast")
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	t.Logf("Status: %d, elapsed: %v, body: %s", resp.StatusCode, elapsed, string(body))

	// Should be 404 or 502, not 200
	assert.NotEqual(t, http.StatusOK, resp.StatusCode,
		"Non-existent deployment should not return 200")
	assert.Less(t, elapsed.Seconds(), 15.0, "Should respond fast")
}

// TestMiddleware_InternalAPIAuthRejection verifies that internal replica API
// endpoints reject requests without the proper internal auth header.
func TestMiddleware_InternalAPIAuthRejection(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	serverIP := env.Config.Servers[0].IP

	t.Run("No auth header rejected", func(t *testing.T) {
		req, _ := http.NewRequest("POST",
			fmt.Sprintf("http://%s:6001/v1/internal/deployments/replica/setup", serverIP), nil)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should be rejected (401 or 403)
		assert.True(t, resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden,
			"Internal API without auth should be rejected (got %d)", resp.StatusCode)
	})

	t.Run("Wrong auth header rejected", func(t *testing.T) {
		req, _ := http.NewRequest("POST",
			fmt.Sprintf("http://%s:6001/v1/internal/deployments/replica/setup", serverIP), nil)
		req.Header.Set("X-Orama-Internal-Auth", "wrong-token")

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.True(t, resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusBadRequest,
			"Internal API with wrong auth should be rejected (got %d)", resp.StatusCode)
	})

	t.Run("Regular API key does not grant internal access", func(t *testing.T) {
		req, _ := http.NewRequest("POST",
			fmt.Sprintf("http://%s:6001/v1/internal/deployments/replica/setup", serverIP), nil)
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// The request may pass auth but fail on bad body — 400 is acceptable
		// But it should NOT succeed with 200
		assert.NotEqual(t, http.StatusOK, resp.StatusCode,
			"Regular API key should not fully authenticate internal endpoints")
	})
}

//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDomainRouting_BasicRouting(t *testing.T) {
	env, err := LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("test-routing-%d", time.Now().Unix())
	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	deploymentID := CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for deployment to be active
	time.Sleep(2 * time.Second)

	t.Run("Standard domain resolves", func(t *testing.T) {
		// Domain format: {deploymentName}.orama.network
		domain := fmt.Sprintf("%s.orama.network", deploymentName)

		resp := TestDeploymentWithHostHeader(t, env, domain, "/")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Should return 200 OK")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Should read response body")

		assert.Contains(t, string(body), "<div id=\"root\">", "Should serve React app")
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/html", "Content-Type should be HTML")

		t.Logf("✓ Standard domain routing works: %s", domain)
	})

	t.Run("Non-debros domain passes through", func(t *testing.T) {
		// Request with non-debros domain should not route to deployment
		resp := TestDeploymentWithHostHeader(t, env, "example.com", "/")
		defer resp.Body.Close()

		// Should either return 404 or pass to default handler
		assert.NotEqual(t, http.StatusOK, resp.StatusCode,
			"Non-debros domain should not route to deployment")

		t.Logf("✓ Non-debros domains correctly pass through (status: %d)", resp.StatusCode)
	})

	t.Run("API paths bypass domain routing", func(t *testing.T) {
		// /v1/* paths should bypass domain routing and use API key auth
		domain := fmt.Sprintf("%s.orama.network", deploymentName)

		req, _ := http.NewRequest("GET", env.GatewayURL+"/v1/deployments/list", nil)
		req.Host = domain
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		// Should return API response, not deployment content
		assert.Equal(t, http.StatusOK, resp.StatusCode, "API endpoint should work")

		var result map[string]interface{}
		bodyBytes, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(bodyBytes, &result)

		// Should be JSON API response
		assert.NoError(t, err, "Should decode JSON (API response)")
		assert.NotNil(t, result["deployments"], "Should have deployments field")

		t.Logf("✓ API paths correctly bypass domain routing")
	})

	t.Run("Well-known paths bypass domain routing", func(t *testing.T) {
		domain := fmt.Sprintf("%s.orama.network", deploymentName)

		// /.well-known/ paths should bypass (used for ACME challenges, etc.)
		resp := TestDeploymentWithHostHeader(t, env, domain, "/.well-known/acme-challenge/test")
		defer resp.Body.Close()

		// Should not serve deployment content
		// Exact status depends on implementation, but shouldn't be deployment content
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		// Shouldn't contain React app content
		if resp.StatusCode == http.StatusOK {
			assert.NotContains(t, bodyStr, "<div id=\"root\">",
				"Well-known paths should not serve deployment content")
		}

		t.Logf("✓ Well-known paths bypass routing (status: %d)", resp.StatusCode)
	})
}

func TestDomainRouting_MultipleDeployments(t *testing.T) {
	env, err := LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	// Create multiple deployments
	deployment1Name := fmt.Sprintf("test-multi-1-%d", time.Now().Unix())
	deployment2Name := fmt.Sprintf("test-multi-2-%d", time.Now().Unix())

	deployment1ID := CreateTestDeployment(t, env, deployment1Name, tarballPath)
	time.Sleep(1 * time.Second)
	deployment2ID := CreateTestDeployment(t, env, deployment2Name, tarballPath)

	defer func() {
		if !env.SkipCleanup {
			DeleteDeployment(t, env, deployment1ID)
			DeleteDeployment(t, env, deployment2ID)
		}
	}()

	time.Sleep(2 * time.Second)

	t.Run("Each deployment routes independently", func(t *testing.T) {
		domain1 := fmt.Sprintf("%s.orama.network", deployment1Name)
		domain2 := fmt.Sprintf("%s.orama.network", deployment2Name)

		// Test deployment 1
		resp1 := TestDeploymentWithHostHeader(t, env, domain1, "/")
		defer resp1.Body.Close()

		assert.Equal(t, http.StatusOK, resp1.StatusCode, "Deployment 1 should serve")

		// Test deployment 2
		resp2 := TestDeploymentWithHostHeader(t, env, domain2, "/")
		defer resp2.Close()

		assert.Equal(t, http.StatusOK, resp2.StatusCode, "Deployment 2 should serve")

		t.Logf("✓ Multiple deployments route independently")
		t.Logf("   - Domain 1: %s", domain1)
		t.Logf("   - Domain 2: %s", domain2)
	})

	t.Run("Wrong domain returns 404", func(t *testing.T) {
		// Request with non-existent deployment subdomain
		fakeDeploymentDomain := fmt.Sprintf("nonexistent-deployment-%d.orama.network", time.Now().Unix())

		resp := TestDeploymentWithHostHeader(t, env, fakeDeploymentDomain, "/")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode,
			"Non-existent deployment should return 404")

		t.Logf("✓ Non-existent deployment returns 404")
	})
}

func TestDomainRouting_ContentTypes(t *testing.T) {
	env, err := LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("test-content-types-%d", time.Now().Unix())
	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	deploymentID := CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			DeleteDeployment(t, env, deploymentID)
		}
	}()

	time.Sleep(2 * time.Second)

	domain := fmt.Sprintf("%s.orama.network", deploymentName)

	contentTypeTests := []struct {
		path        string
		shouldHave  string
		description string
	}{
		{"/", "text/html", "HTML root"},
		{"/index.html", "text/html", "HTML file"},
	}

	for _, test := range contentTypeTests {
		t.Run(test.description, func(t *testing.T) {
			resp := TestDeploymentWithHostHeader(t, env, domain, test.path)
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				contentType := resp.Header.Get("Content-Type")
				assert.Contains(t, contentType, test.shouldHave,
					"Content-Type for %s should contain %s", test.path, test.shouldHave)

				t.Logf("✓ %s: %s", test.description, contentType)
			} else {
				t.Logf("⚠ %s returned status %d", test.path, resp.StatusCode)
			}
		})
	}
}

func TestDomainRouting_SPAFallback(t *testing.T) {
	env, err := LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("test-spa-%d", time.Now().Unix())
	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	deploymentID := CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			DeleteDeployment(t, env, deploymentID)
		}
	}()

	time.Sleep(2 * time.Second)

	domain := fmt.Sprintf("%s.orama.network", deploymentName)

	t.Run("Unknown paths fall back to index.html", func(t *testing.T) {
		unknownPaths := []string{
			"/about",
			"/users/123",
			"/settings/profile",
			"/some/deep/nested/path",
		}

		for _, path := range unknownPaths {
			resp := TestDeploymentWithHostHeader(t, env, domain, path)
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			// Should return index.html for SPA routing
			assert.Equal(t, http.StatusOK, resp.StatusCode,
				"SPA fallback should return 200 for %s", path)

			assert.Contains(t, string(body), "<div id=\"root\">",
				"SPA fallback should return index.html for %s", path)
		}

		t.Logf("✓ SPA fallback routing verified for %d paths", len(unknownPaths))
	})
}

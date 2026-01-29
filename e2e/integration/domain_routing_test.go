//go:build e2e

package integration_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDomainRouting_BasicRouting(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("test-routing-%d", time.Now().Unix())
	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for deployment to be active
	time.Sleep(2 * time.Second)

	// Get deployment details for debugging
	deployment := e2e.GetDeployment(t, env, deploymentID)
	t.Logf("Deployment created: ID=%s, CID=%s, Name=%s, Status=%s",
		deploymentID, deployment["content_cid"], deployment["name"], deployment["status"])

	t.Run("Standard domain resolves", func(t *testing.T) {
		// Domain format: {deploymentName}.{baseDomain}
		domain := env.BuildDeploymentDomain(deploymentName)

		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/")
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
		resp := e2e.TestDeploymentWithHostHeader(t, env, "example.com", "/")
		defer resp.Body.Close()

		// Should either return 404 or pass to default handler
		assert.NotEqual(t, http.StatusOK, resp.StatusCode,
			"Non-debros domain should not route to deployment")

		t.Logf("✓ Non-debros domains correctly pass through (status: %d)", resp.StatusCode)
	})

	t.Run("API paths bypass domain routing", func(t *testing.T) {
		// /v1/* paths should bypass domain routing and use API key auth
		domain := env.BuildDeploymentDomain(deploymentName)

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
		domain := env.BuildDeploymentDomain(deploymentName)

		// /.well-known/ paths should bypass (used for ACME challenges, etc.)
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/.well-known/acme-challenge/test")
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
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	// Create multiple deployments
	deployment1Name := fmt.Sprintf("test-multi-1-%d", time.Now().Unix())
	deployment2Name := fmt.Sprintf("test-multi-2-%d", time.Now().Unix())

	deployment1ID := e2e.CreateTestDeployment(t, env, deployment1Name, tarballPath)
	time.Sleep(1 * time.Second)
	deployment2ID := e2e.CreateTestDeployment(t, env, deployment2Name, tarballPath)

	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deployment1ID)
			e2e.DeleteDeployment(t, env, deployment2ID)
		}
	}()

	time.Sleep(2 * time.Second)

	t.Run("Each deployment routes independently", func(t *testing.T) {
		domain1 := env.BuildDeploymentDomain(deployment1Name)
		domain2 := env.BuildDeploymentDomain(deployment2Name)

		// Test deployment 1
		resp1 := e2e.TestDeploymentWithHostHeader(t, env, domain1, "/")
		defer resp1.Body.Close()

		assert.Equal(t, http.StatusOK, resp1.StatusCode, "Deployment 1 should serve")

		// Test deployment 2
		resp2 := e2e.TestDeploymentWithHostHeader(t, env, domain2, "/")
		defer resp2.Body.Close()

		assert.Equal(t, http.StatusOK, resp2.StatusCode, "Deployment 2 should serve")

		t.Logf("✓ Multiple deployments route independently")
		t.Logf("   - Domain 1: %s", domain1)
		t.Logf("   - Domain 2: %s", domain2)
	})

	t.Run("Wrong domain returns 404", func(t *testing.T) {
		// Request with non-existent deployment subdomain
		fakeDeploymentDomain := env.BuildDeploymentDomain(fmt.Sprintf("nonexistent-deployment-%d", time.Now().Unix()))

		resp := e2e.TestDeploymentWithHostHeader(t, env, fakeDeploymentDomain, "/")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode,
			"Non-existent deployment should return 404")

		t.Logf("✓ Non-existent deployment returns 404")
	})
}

func TestDomainRouting_ContentTypes(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("test-content-types-%d", time.Now().Unix())
	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	time.Sleep(2 * time.Second)

	domain := env.BuildDeploymentDomain(deploymentName)

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
			resp := e2e.TestDeploymentWithHostHeader(t, env, domain, test.path)
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
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("test-spa-%d", time.Now().Unix())
	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	time.Sleep(2 * time.Second)

	domain := env.BuildDeploymentDomain(deploymentName)

	t.Run("Unknown paths fall back to index.html", func(t *testing.T) {
		unknownPaths := []string{
			"/about",
			"/users/123",
			"/settings/profile",
			"/some/deep/nested/path",
		}

		for _, path := range unknownPaths {
			resp := e2e.TestDeploymentWithHostHeader(t, env, domain, path)
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

// TestDeployment_DomainFormat verifies that deployment URLs use the correct format:
// - CORRECT: {name}-{random}.{baseDomain} (e.g., "myapp-f3o4if.dbrs.space")
// - WRONG: {name}.node-{shortID}.{baseDomain} (should NOT exist)
func TestDeployment_DomainFormat(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("format-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for deployment
	time.Sleep(2 * time.Second)

	t.Run("Deployment URL has correct format", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		// Get the deployment URLs
		urls, ok := deployment["urls"].([]interface{})
		if !ok || len(urls) == 0 {
			// Fall back to single url field
			if url, ok := deployment["url"].(string); ok && url != "" {
				urls = []interface{}{url}
			}
		}

		// Get the subdomain from deployment response
		subdomain, _ := deployment["subdomain"].(string)
		t.Logf("Deployment subdomain: %s", subdomain)
		t.Logf("Deployment URLs: %v", urls)

		foundCorrectFormat := false
		for _, u := range urls {
			urlStr, ok := u.(string)
			if !ok {
				continue
			}

			// URL should start with https://{name}-
			expectedPrefix := fmt.Sprintf("https://%s-", deploymentName)
			if strings.HasPrefix(urlStr, expectedPrefix) {
				foundCorrectFormat = true
			}

			// URL should contain base domain
			assert.Contains(t, urlStr, env.BaseDomain,
				"URL should contain base domain %s", env.BaseDomain)

			// URL should NOT contain node identifier pattern
			assert.NotContains(t, urlStr, ".node-",
				"URL should NOT have node identifier (got: %s)", urlStr)
		}

		if len(urls) > 0 {
			assert.True(t, foundCorrectFormat, "Should find URL with correct domain format (https://{name}-{random}.{baseDomain})")
		}

		t.Logf("✓ Domain format verification passed")
		t.Logf("   - Format: {name}-{random}.{baseDomain}")
	})

	t.Run("Domain resolves via Host header", func(t *testing.T) {
		// Get the actual subdomain from the deployment
		deployment := e2e.GetDeployment(t, env, deploymentID)
		subdomain, _ := deployment["subdomain"].(string)
		if subdomain == "" {
			t.Skip("No subdomain set, skipping host header test")
		}
		domain := subdomain + "." + env.BaseDomain

		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"Domain %s should resolve successfully", domain)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Contains(t, string(body), "<div id=\"root\">",
			"Should serve deployment content")

		t.Logf("✓ Domain %s resolves correctly", domain)
	})
}

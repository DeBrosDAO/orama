//go:build e2e

package deployments_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNextJSDeployment_SSR tests Next.js deployment with SSR and API routes
// 1. Deploy Next.js app
// 2. Test SSR page (verify server-rendered HTML)
// 3. Test API routes (/api/hello, /api/data)
// 4. Test static assets
// 5. Cleanup
func TestNextJSDeployment_SSR(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("nextjs-ssr-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/nextjs-ssr.tar.gz")
	var deploymentID string

	// Check if tarball exists
	if _, err := os.Stat(tarballPath); os.IsNotExist(err) {
		t.Skip("Next.js SSR tarball not found at " + tarballPath)
	}

	// Cleanup after test
	defer func() {
		if !env.SkipCleanup && deploymentID != "" {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Deploy Next.js SSR app", func(t *testing.T) {
		deploymentID = createNextJSDeployment(t, env, deploymentName, tarballPath)
		require.NotEmpty(t, deploymentID, "Deployment ID should not be empty")
		t.Logf("Created Next.js deployment: %s (ID: %s)", deploymentName, deploymentID)
	})

	t.Run("Wait for deployment to become healthy", func(t *testing.T) {
		healthy := e2e.WaitForHealthy(t, env, deploymentID, 120*time.Second)
		require.True(t, healthy, "Deployment should become healthy")
		t.Logf("Deployment is healthy")
	})

	t.Run("Verify deployment in database", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		assert.Equal(t, deploymentName, deployment["name"], "Deployment name should match")

		deploymentType, ok := deployment["type"].(string)
		require.True(t, ok, "Type should be a string")
		assert.Contains(t, deploymentType, "nextjs", "Type should be nextjs")

		t.Logf("Deployment type: %s", deploymentType)
	})

	t.Run("Test SSR page - verify server-rendered HTML", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "SSR page should return 200")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Should read response body")
		bodyStr := string(body)

		// Verify HTML is server-rendered (contains actual content, not just loading state)
		assert.Contains(t, bodyStr, "Orama Network Next.js Test", "Should contain app title")
		assert.Contains(t, bodyStr, "Server-Side Rendering Test", "Should contain SSR test marker")
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/html", "Should be HTML content")

		t.Logf("SSR page loaded successfully")
		t.Logf("Content-Type: %s", resp.Header.Get("Content-Type"))
	})

	t.Run("Test API route - /api/hello", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/api/hello")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "API route should return 200")

		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result), "Should decode JSON response")

		assert.Contains(t, result["message"], "Hello", "Should contain hello message")
		assert.NotEmpty(t, result["timestamp"], "Should have timestamp")

		t.Logf("API /hello response: %+v", result)
	})

	t.Run("Test API route - /api/data", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/api/data")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "API data route should return 200")

		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result), "Should decode JSON response")

		// Just verify it returns valid JSON
		t.Logf("API /data response: %+v", result)
	})

	t.Run("Test static asset - _next directory", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)

		// First, get the main page to find the actual static asset path
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/")
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		// Look for _next/static references in the HTML
		if strings.Contains(bodyStr, "_next/static") {
			t.Logf("Found _next/static references in HTML")

			// Try to fetch a common static chunk
			// The exact path depends on Next.js build output
			// We'll just verify the _next directory structure is accessible
			chunkResp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/_next/static/chunks/main.js")
			defer chunkResp.Body.Close()

			// It's OK if specific files don't exist (they have hashed names)
			// Just verify we don't get a 500 error
			assert.NotEqual(t, http.StatusInternalServerError, chunkResp.StatusCode,
				"Static asset request should not cause server error")

			t.Logf("Static asset request status: %d", chunkResp.StatusCode)
		} else {
			t.Logf("No _next/static references found (may be using different bundling)")
		}
	})

	t.Run("Test 404 handling", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/nonexistent-page-xyz")
		defer resp.Body.Close()

		// Next.js should handle 404 gracefully
		// Could be 404 or 200 depending on catch-all routes
		assert.Contains(t, []int{200, 404}, resp.StatusCode,
			"Should return either 200 (catch-all) or 404")

		t.Logf("404 handling: status=%d", resp.StatusCode)
	})
}

// createNextJSDeployment creates a Next.js deployment
func createNextJSDeployment(t *testing.T, env *e2e.E2ETestEnv, name, tarballPath string) string {
	t.Helper()

	file, err := os.Open(tarballPath)
	if err != nil {
		t.Fatalf("failed to open tarball: %v", err)
	}
	defer file.Close()

	// Create multipart form
	body := &bytes.Buffer{}
	boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

	// Write name field
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"name\"\r\n\r\n")
	body.WriteString(name + "\r\n")

	// Write ssr field (enable SSR mode)
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"ssr\"\r\n\r\n")
	body.WriteString("true\r\n")

	// Write tarball file
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"tarball\"; filename=\"app.tar.gz\"\r\n")
	body.WriteString("Content-Type: application/gzip\r\n\r\n")

	fileData, _ := io.ReadAll(file)
	body.Write(fileData)
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/nextjs/upload", body)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	// Use a longer timeout for large Next.js uploads (can be 50MB+)
	uploadClient := e2e.NewHTTPClient(5 * time.Minute)
	resp, err := uploadClient.Do(req)
	if err != nil {
		t.Fatalf("failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Deployment upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if id, ok := result["deployment_id"].(string); ok {
		return id
	}
	if id, ok := result["id"].(string); ok {
		return id
	}
	t.Fatalf("Deployment response missing id field: %+v", result)
	return ""
}

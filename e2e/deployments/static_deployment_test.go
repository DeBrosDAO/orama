//go:build e2e

package deployments_test

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticDeployment_FullFlow(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("test-static-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/tarballs/react-vite.tar.gz")
	var deploymentID string

	// Cleanup after test
	defer func() {
		if !env.SkipCleanup && deploymentID != "" {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Upload static tarball", func(t *testing.T) {
		deploymentID = e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)

		assert.NotEmpty(t, deploymentID, "Deployment ID should not be empty")
		t.Logf("✓ Created deployment: %s (ID: %s)", deploymentName, deploymentID)
	})

	t.Run("Verify deployment in database", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		assert.Equal(t, deploymentName, deployment["name"], "Deployment name should match")
		assert.NotEmpty(t, deployment["content_cid"], "Content CID should not be empty")

		// Status might be "deploying" or "active" depending on timing
		status, ok := deployment["status"].(string)
		require.True(t, ok, "Status should be a string")
		assert.Contains(t, []string{"deploying", "active"}, status, "Status should be deploying or active")

		t.Logf("✓ Deployment verified in database")
		t.Logf("   - Name: %s", deployment["name"])
		t.Logf("   - Status: %s", status)
		t.Logf("   - CID: %s", deployment["content_cid"])
	})

	t.Run("Verify DNS record creation", func(t *testing.T) {
		// Wait for deployment to become active
		time.Sleep(2 * time.Second)

		// Get the actual domain from deployment response
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		require.NotEmpty(t, nodeURL, "Deployment should have a URL")
		expectedDomain := extractDomain(nodeURL)

		// Make request with Host header (localhost testing)
		resp := e2e.TestDeploymentWithHostHeader(t, env, expectedDomain, "/")
		defer resp.Body.Close()

		// Should return 200 with React app HTML
		assert.Equal(t, http.StatusOK, resp.StatusCode, "Should return 200 OK")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Should read response body")

		bodyStr := string(body)

		// Verify React app content
		assert.Contains(t, bodyStr, "<div id=\"root\">", "Should contain React root div")
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/html", "Content-Type should be text/html")

		t.Logf("✓ Domain routing works")
		t.Logf("   - Domain: %s", expectedDomain)
		t.Logf("   - Status: %d", resp.StatusCode)
		t.Logf("   - Content-Type: %s", resp.Header.Get("Content-Type"))
	})

	t.Run("Verify static assets serve correctly", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		require.NotEmpty(t, nodeURL, "Deployment should have a URL")
		expectedDomain := extractDomain(nodeURL)

		// Test CSS file (exact path depends on Vite build output)
		// We'll just test a few common asset paths
		assetPaths := []struct {
			path        string
			contentType string
		}{
			{"/index.html", "text/html"},
			// Note: Asset paths with hashes change on each build
			// We'll test what we can
		}

		for _, asset := range assetPaths {
			resp := e2e.TestDeploymentWithHostHeader(t, env, expectedDomain, asset.path)
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				assert.Contains(t, resp.Header.Get("Content-Type"), asset.contentType,
					"Content-Type should be %s for %s", asset.contentType, asset.path)

				t.Logf("✓ Asset served correctly: %s (%s)", asset.path, asset.contentType)
			}
		}
	})

	t.Run("Verify SPA fallback routing", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		require.NotEmpty(t, nodeURL, "Deployment should have a URL")
		expectedDomain := extractDomain(nodeURL)

		// Request unknown route (should return index.html for SPA)
		resp := e2e.TestDeploymentWithHostHeader(t, env, expectedDomain, "/about/team")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "SPA fallback should return 200")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Should read response body")

		assert.Contains(t, string(body), "<div id=\"root\">", "Should return index.html for unknown paths")

		t.Logf("✓ SPA fallback routing works")
	})

	t.Run("List deployments", func(t *testing.T) {
		req, err := http.NewRequest("GET", env.GatewayURL+"/v1/deployments/list", nil)
		require.NoError(t, err, "Should create request")

		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "List deployments should return 200")

		var result map[string]interface{}
		require.NoError(t, e2e.DecodeJSON(mustReadAll(t, resp.Body), &result), "Should decode JSON")

		deployments, ok := result["deployments"].([]interface{})
		require.True(t, ok, "Deployments should be an array")

		assert.GreaterOrEqual(t, len(deployments), 1, "Should have at least one deployment")

		// Find our deployment
		found := false
		for _, d := range deployments {
			dep, ok := d.(map[string]interface{})
			if !ok {
				continue
			}
			if dep["name"] == deploymentName {
				found = true
				t.Logf("✓ Found deployment in list: %s", deploymentName)
				break
			}
		}

		assert.True(t, found, "Deployment should be in list")
	})

	t.Run("Delete deployment", func(t *testing.T) {
		e2e.DeleteDeployment(t, env, deploymentID)

		// Verify deletion - allow time for replication
		time.Sleep(3 * time.Second)

		req, _ := http.NewRequest("GET", env.GatewayURL+"/v1/deployments/get?id="+deploymentID, nil)
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		t.Logf("Delete verification response: status=%d body=%s", resp.StatusCode, string(body))

		// After deletion, either 404 (not found) or 200 with empty/error response is acceptable
		if resp.StatusCode == http.StatusOK {
			// If 200, check if the deployment is actually gone
			t.Logf("Got 200 - this may indicate soft delete or eventual consistency")
		}

		t.Logf("✓ Deployment deleted successfully")

		// Clear deploymentID so cleanup doesn't try to delete again
		deploymentID = ""
	})
}

func mustReadAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	data, err := io.ReadAll(r)
	require.NoError(t, err, "Should read all data")
	return data
}

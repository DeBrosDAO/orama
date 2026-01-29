//go:build e2e

package deployments_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeJSDeployment_FullFlow(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("test-nodejs-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/nodejs-backend.tar.gz")
	var deploymentID string

	// Cleanup after test
	defer func() {
		if !env.SkipCleanup && deploymentID != "" {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Upload Node.js backend", func(t *testing.T) {
		deploymentID = createNodeJSDeployment(t, env, deploymentName, tarballPath)

		assert.NotEmpty(t, deploymentID, "Deployment ID should not be empty")
		t.Logf("Created deployment: %s (ID: %s)", deploymentName, deploymentID)
	})

	t.Run("Wait for deployment to become healthy", func(t *testing.T) {
		healthy := e2e.WaitForHealthy(t, env, deploymentID, 90*time.Second)
		assert.True(t, healthy, "Deployment should become healthy within timeout")
		t.Logf("Deployment is healthy")
	})

	t.Run("Test health endpoint", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		// Get the deployment URLs (can be array of strings or map)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		// Test via Host header (localhost testing)
		resp := e2e.TestDeploymentWithHostHeader(t, env, extractDomain(nodeURL), "/health")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Health check should return 200")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var health map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &health))

		assert.Equal(t, "healthy", health["status"])
		t.Logf("Health check passed: %v", health)
	})

	t.Run("Test API endpoint", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)

		// Test root endpoint
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var result map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &result))

		assert.Contains(t, result["message"], "Node.js")
		t.Logf("Root endpoint response: %v", result)
	})
}

func createNodeJSDeployment(t *testing.T, env *e2e.E2ETestEnv, name, tarballPath string) string {
	t.Helper()

	var fileData []byte

	info, err := os.Stat(tarballPath)
	if err != nil {
		t.Fatalf("Failed to stat tarball path: %v", err)
	}

	if info.IsDir() {
		// Create tarball from directory
		tarData, err := exec.Command("tar", "-czf", "-", "-C", tarballPath, ".").Output()
		require.NoError(t, err, "Failed to create tarball from %s", tarballPath)
		fileData = tarData
	} else {
		file, err := os.Open(tarballPath)
		require.NoError(t, err, "Failed to open tarball: %s", tarballPath)
		defer file.Close()
		fileData, _ = io.ReadAll(file)
	}

	body := &bytes.Buffer{}
	boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"name\"\r\n\r\n")
	body.WriteString(name + "\r\n")

	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"tarball\"; filename=\"app.tar.gz\"\r\n")
	body.WriteString("Content-Type: application/gzip\r\n\r\n")

	body.Write(fileData)
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/nodejs/upload", body)
	require.NoError(t, err)

	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Deployment upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	if id, ok := result["deployment_id"].(string); ok {
		return id
	}
	if id, ok := result["id"].(string); ok {
		return id
	}
	t.Fatalf("Deployment response missing id field: %+v", result)
	return ""
}

// extractNodeURL gets the node URL from deployment response
// Handles both array of strings and map formats
func extractNodeURL(t *testing.T, deployment map[string]interface{}) string {
	t.Helper()

	// Try as array of strings first (new format)
	if urls, ok := deployment["urls"].([]interface{}); ok && len(urls) > 0 {
		if url, ok := urls[0].(string); ok {
			return url
		}
	}

	// Try as map (legacy format)
	if urls, ok := deployment["urls"].(map[string]interface{}); ok {
		if url, ok := urls["node"].(string); ok {
			return url
		}
	}

	return ""
}

func extractDomain(url string) string {
	// Extract domain from URL like "https://myapp.node-xyz.dbrs.space"
	// Remove protocol
	domain := url
	if len(url) > 8 && url[:8] == "https://" {
		domain = url[8:]
	} else if len(url) > 7 && url[:7] == "http://" {
		domain = url[7:]
	}
	// Remove trailing slash
	if len(domain) > 0 && domain[len(domain)-1] == '/' {
		domain = domain[:len(domain)-1]
	}
	return domain
}

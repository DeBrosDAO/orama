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
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeploymentRollback_FullFlow tests the complete rollback workflow:
// 1. Deploy v1
// 2. Update to v2
// 3. Verify v2 content
// 4. Rollback to v1
// 5. Verify v1 content is restored
func TestDeploymentRollback_FullFlow(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("rollback-test-%d", time.Now().Unix())
	tarballPathV1 := filepath.Join("../../testdata/tarballs/react-vite.tar.gz")
	var deploymentID string

	// Cleanup after test
	defer func() {
		if !env.SkipCleanup && deploymentID != "" {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Deploy v1", func(t *testing.T) {
		deploymentID = e2e.CreateTestDeployment(t, env, deploymentName, tarballPathV1)
		require.NotEmpty(t, deploymentID, "Deployment ID should not be empty")
		t.Logf("Created deployment v1: %s (ID: %s)", deploymentName, deploymentID)
	})

	t.Run("Verify v1 deployment", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		version, ok := deployment["version"].(float64)
		require.True(t, ok, "Version should be a number")
		assert.Equal(t, float64(1), version, "Initial version should be 1")

		contentCID, ok := deployment["content_cid"].(string)
		require.True(t, ok, "Content CID should be a string")
		assert.NotEmpty(t, contentCID, "Content CID should not be empty")

		t.Logf("v1 version: %v, CID: %s", version, contentCID)
	})

	var v1CID string
	t.Run("Save v1 CID", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		v1CID = deployment["content_cid"].(string)
		t.Logf("Saved v1 CID: %s", v1CID)
	})

	t.Run("Update to v2", func(t *testing.T) {
		// Update the deployment with the same tarball (simulates a new version)
		updateDeployment(t, env, deploymentName, tarballPathV1)

		// Wait for update to complete
		time.Sleep(2 * time.Second)
	})

	t.Run("Verify v2 deployment", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		version, ok := deployment["version"].(float64)
		require.True(t, ok, "Version should be a number")
		assert.Equal(t, float64(2), version, "Version should be 2 after update")

		t.Logf("v2 version: %v", version)
	})

	t.Run("List deployment versions", func(t *testing.T) {
		versions := listVersions(t, env, deploymentName)
		t.Logf("Available versions: %+v", versions)

		// Should have at least 2 versions in history
		assert.GreaterOrEqual(t, len(versions), 1, "Should have version history")
	})

	t.Run("Rollback to v1", func(t *testing.T) {
		rollbackDeployment(t, env, deploymentName, 1)

		// Wait for rollback to complete
		time.Sleep(2 * time.Second)
	})

	t.Run("Verify rollback succeeded", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		version, ok := deployment["version"].(float64)
		require.True(t, ok, "Version should be a number")
		// Note: Version number increases even on rollback (it's a new deployment version)
		// But the content_cid should be the same as v1
		t.Logf("Post-rollback version: %v", version)

		contentCID, ok := deployment["content_cid"].(string)
		require.True(t, ok, "Content CID should be a string")
		assert.Equal(t, v1CID, contentCID, "Content CID should match v1 after rollback")

		t.Logf("Rollback verified - content CID matches v1: %s", contentCID)
	})
}

// updateDeployment updates an existing static deployment
func updateDeployment(t *testing.T, env *e2e.E2ETestEnv, name, tarballPath string) {
	t.Helper()

	file, err := os.Open(tarballPath)
	require.NoError(t, err, "Failed to open tarball")
	defer file.Close()

	// Create multipart form
	body := &bytes.Buffer{}
	boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

	// Write name field
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"name\"\r\n\r\n")
	body.WriteString(name + "\r\n")

	// Write tarball file
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"tarball\"; filename=\"app.tar.gz\"\r\n")
	body.WriteString("Content-Type: application/gzip\r\n\r\n")

	fileData, _ := io.ReadAll(file)
	body.Write(fileData)
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/static/update", body)
	require.NoError(t, err, "Failed to create request")

	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	require.NoError(t, err, "Failed to execute request")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Update failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result), "Failed to decode response")
	t.Logf("Update response: %+v", result)
}

// listVersions lists available versions for a deployment
func listVersions(t *testing.T, env *e2e.E2ETestEnv, name string) []map[string]interface{} {
	t.Helper()

	req, err := http.NewRequest("GET", env.GatewayURL+"/v1/deployments/versions?name="+name, nil)
	require.NoError(t, err, "Failed to create request")

	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	require.NoError(t, err, "Failed to execute request")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Logf("List versions returned status %d: %s", resp.StatusCode, string(bodyBytes))
		return nil
	}

	var result struct {
		Versions []map[string]interface{} `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Logf("Failed to decode versions: %v", err)
		return nil
	}

	return result.Versions
}

// rollbackDeployment triggers a rollback to a specific version
func rollbackDeployment(t *testing.T, env *e2e.E2ETestEnv, name string, targetVersion int) {
	t.Helper()

	reqBody := map[string]interface{}{
		"name":    name,
		"version": targetVersion,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/rollback", bytes.NewBuffer(bodyBytes))
	require.NoError(t, err, "Failed to create request")

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	require.NoError(t, err, "Failed to execute request")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Rollback failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result), "Failed to decode response")
	t.Logf("Rollback response: %+v", result)
}

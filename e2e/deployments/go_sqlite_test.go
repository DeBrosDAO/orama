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

// TestGoBackendWithSQLite tests Go backend deployment with hosted SQLite connectivity
// 1. Create hosted SQLite database
// 2. Deploy Go backend with DATABASE_NAME env var
// 3. POST /api/users → verify insert
// 4. GET /api/users → verify read
// 5. Cleanup
func TestGoBackendWithSQLite(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("go-sqlite-test-%d", time.Now().Unix())
	dbName := fmt.Sprintf("test-db-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/go-backend.tar.gz")
	var deploymentID string

	// Cleanup after test
	defer func() {
		if !env.SkipCleanup {
			if deploymentID != "" {
				e2e.DeleteDeployment(t, env, deploymentID)
			}
			// Delete the test database
			deleteSQLiteDB(t, env, dbName)
		}
	}()

	t.Run("Create SQLite database", func(t *testing.T) {
		e2e.CreateSQLiteDB(t, env, dbName)
		t.Logf("Created database: %s", dbName)
	})

	t.Run("Deploy Go backend with DATABASE_NAME", func(t *testing.T) {
		deploymentID = createGoDeployment(t, env, deploymentName, tarballPath, map[string]string{
			"DATABASE_NAME": dbName,
			"GATEWAY_URL":   env.GatewayURL,
			"API_KEY":       env.APIKey,
		})
		require.NotEmpty(t, deploymentID, "Deployment ID should not be empty")
		t.Logf("Created Go deployment: %s (ID: %s)", deploymentName, deploymentID)
	})

	t.Run("Wait for deployment to become healthy", func(t *testing.T) {
		healthy := e2e.WaitForHealthy(t, env, deploymentID, 90*time.Second)
		require.True(t, healthy, "Deployment should become healthy")
		t.Logf("Deployment is healthy")
	})

	t.Run("Test health endpoint", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/health")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Health check should return 200")

		body, _ := io.ReadAll(resp.Body)
		var health map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &health))

		assert.Equal(t, "healthy", health["status"])
		t.Logf("Health response: %+v", health)
	})

	t.Run("POST /api/users - create user", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)

		// Create a test user
		userData := map[string]string{
			"name":  "Test User",
			"email": "test@example.com",
		}
		body, _ := json.Marshal(userData)

		req, err := http.NewRequest("POST", env.GatewayURL+"/api/users", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Host = domain

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode, "Should create user successfully")

		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

		assert.True(t, result["success"].(bool), "Success should be true")
		user := result["user"].(map[string]interface{})
		assert.Equal(t, "Test User", user["name"])
		assert.Equal(t, "test@example.com", user["email"])

		t.Logf("Created user: %+v", user)
	})

	t.Run("GET /api/users - list users", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/api/users")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

		users := result["users"].([]interface{})
		total := int(result["total"].(float64))

		assert.GreaterOrEqual(t, total, 1, "Should have at least one user")

		// Find our test user
		found := false
		for _, u := range users {
			user := u.(map[string]interface{})
			if user["email"] == "test@example.com" {
				found = true
				assert.Equal(t, "Test User", user["name"])
				break
			}
		}
		assert.True(t, found, "Test user should be in the list")

		t.Logf("Users response: total=%d", total)
	})

	t.Run("DELETE /api/users - delete user", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)

		// First get the user ID
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/api/users")
		defer resp.Body.Close()

		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

		users := result["users"].([]interface{})
		var userID int
		for _, u := range users {
			user := u.(map[string]interface{})
			if user["email"] == "test@example.com" {
				userID = int(user["id"].(float64))
				break
			}
		}
		require.NotZero(t, userID, "Should find test user ID")

		// Delete the user
		req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/api/users?id=%d", env.GatewayURL, userID), nil)
		require.NoError(t, err)
		req.Host = domain

		deleteResp, err := env.HTTPClient.Do(req)
		require.NoError(t, err)
		defer deleteResp.Body.Close()

		assert.Equal(t, http.StatusOK, deleteResp.StatusCode, "Should delete user successfully")

		t.Logf("Deleted user ID: %d", userID)
	})
}

// createGoDeployment creates a Go backend deployment with environment variables
func createGoDeployment(t *testing.T, env *e2e.E2ETestEnv, name, tarballPath string, envVars map[string]string) string {
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

	// Write environment variables
	for key, value := range envVars {
		body.WriteString("--" + boundary + "\r\n")
		body.WriteString(fmt.Sprintf("Content-Disposition: form-data; name=\"env_%s\"\r\n\r\n", key))
		body.WriteString(value + "\r\n")
	}

	// Write tarball file
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"tarball\"; filename=\"app.tar.gz\"\r\n")
	body.WriteString("Content-Type: application/gzip\r\n\r\n")

	fileData, _ := io.ReadAll(file)
	body.Write(fileData)
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/go/upload", body)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
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

// deleteSQLiteDB deletes a SQLite database
func deleteSQLiteDB(t *testing.T, env *e2e.E2ETestEnv, dbName string) {
	t.Helper()

	req, err := http.NewRequest("DELETE", env.GatewayURL+"/v1/db/"+dbName, nil)
	if err != nil {
		t.Logf("warning: failed to create delete request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Logf("warning: failed to delete database: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Logf("warning: delete database returned status %d", resp.StatusCode)
	}
}

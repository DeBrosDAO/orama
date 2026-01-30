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
	tarballPath := filepath.Join("../../testdata/apps/go-api")
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

		assert.Contains(t, []string{"healthy", "ok"}, health["status"])
		t.Logf("Health response: %+v", health)
	})

	t.Run("POST /api/notes - create note", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)

		noteData := map[string]string{
			"title":   "Test Note",
			"content": "This is a test note",
		}
		body, _ := json.Marshal(noteData)

		req, err := http.NewRequest("POST", env.GatewayURL+"/api/notes", bytes.NewBuffer(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Host = domain

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode, "Should create note successfully")

		var note map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&note))

		assert.Equal(t, "Test Note", note["title"])
		assert.Equal(t, "This is a test note", note["content"])
		t.Logf("Created note: %+v", note)
	})

	t.Run("GET /api/notes - list notes", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/api/notes")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var notes []map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&notes))

		assert.GreaterOrEqual(t, len(notes), 1, "Should have at least one note")

		found := false
		for _, note := range notes {
			if note["title"] == "Test Note" {
				found = true
				break
			}
		}
		assert.True(t, found, "Test note should be in the list")
		t.Logf("Notes count: %d", len(notes))
	})

	t.Run("DELETE /api/notes - delete note", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}

		domain := extractDomain(nodeURL)

		// First get the note ID
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/api/notes")
		defer resp.Body.Close()

		var notes []map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&notes))

		var noteID int
		for _, note := range notes {
			if note["title"] == "Test Note" {
				noteID = int(note["id"].(float64))
				break
			}
		}
		require.NotZero(t, noteID, "Should find test note ID")

		req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/api/notes/%d", env.GatewayURL, noteID), nil)
		require.NoError(t, err)
		req.Host = domain

		deleteResp, err := env.HTTPClient.Do(req)
		require.NoError(t, err)
		defer deleteResp.Body.Close()

		assert.Equal(t, http.StatusOK, deleteResp.StatusCode, "Should delete note successfully")
		t.Logf("Deleted note ID: %d", noteID)
	})
}

// createGoDeployment creates a Go backend deployment with environment variables
func createGoDeployment(t *testing.T, env *e2e.E2ETestEnv, name, tarballPath string, envVars map[string]string) string {
	t.Helper()

	var fileData []byte
	info, err := os.Stat(tarballPath)
	if err != nil {
		t.Fatalf("failed to stat tarball path: %v", err)
	}
	if info.IsDir() {
		// Build Go binary for linux/amd64, then tar it
		tmpDir, err := os.MkdirTemp("", "go-deploy-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		binaryPath := filepath.Join(tmpDir, "app")
		buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
		buildCmd.Dir = tarballPath
		buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to build Go app: %v\n%s", err, string(out))
		}

		fileData, err = exec.Command("tar", "-czf", "-", "-C", tmpDir, ".").Output()
		if err != nil {
			t.Fatalf("failed to create tarball: %v", err)
		}
	} else {
		file, err := os.Open(tarballPath)
		if err != nil {
			t.Fatalf("failed to open tarball: %v", err)
		}
		defer file.Close()
		fileData, _ = io.ReadAll(file)
	}

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

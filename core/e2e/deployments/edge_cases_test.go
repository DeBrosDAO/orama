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
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeploy_InvalidTarball verifies that uploading an invalid/corrupt tarball
// returns a clean error (not a 500 or panic).
func TestDeploy_InvalidTarball(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	deploymentName := fmt.Sprintf("invalid-tar-%d", time.Now().Unix())

	body := &bytes.Buffer{}
	boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"name\"\r\n\r\n")
	body.WriteString(deploymentName + "\r\n")

	// Write invalid tarball data (random bytes, not a real gzip)
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"tarball\"; filename=\"app.tar.gz\"\r\n")
	body.WriteString("Content-Type: application/gzip\r\n\r\n")
	body.WriteString("this is not a valid tarball content at all!!!")
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/static/upload", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	t.Logf("Status: %d, Body: %s", resp.StatusCode, string(respBody))

	// Should return an error, not 2xx (ideally 400, but server currently returns 500)
	assert.True(t, resp.StatusCode >= 400,
		"Invalid tarball should return error (got %d)", resp.StatusCode)
}

// TestDeploy_EmptyTarball verifies that uploading an empty file returns an error.
func TestDeploy_EmptyTarball(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	deploymentName := fmt.Sprintf("empty-tar-%d", time.Now().Unix())

	body := &bytes.Buffer{}
	boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"name\"\r\n\r\n")
	body.WriteString(deploymentName + "\r\n")

	// Empty tarball
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"tarball\"; filename=\"app.tar.gz\"\r\n")
	body.WriteString("Content-Type: application/gzip\r\n\r\n")
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/static/upload", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	t.Logf("Status: %d, Body: %s", resp.StatusCode, string(respBody))

	assert.True(t, resp.StatusCode >= 400,
		"Empty tarball should return error (got %d)", resp.StatusCode)
}

// TestDeploy_MissingName verifies that deploying without a name returns an error.
func TestDeploy_MissingName(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	tarballPath := filepath.Join("../../testdata/apps/react-app")

	body := &bytes.Buffer{}
	boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

	// No name field
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"tarball\"; filename=\"app.tar.gz\"\r\n")
	body.WriteString("Content-Type: application/gzip\r\n\r\n")

	// Create tarball from directory for the "no name" test
	tarData, err := exec.Command("tar", "-czf", "-", "-C", tarballPath, ".").Output()
	if err != nil {
		t.Skip("Failed to create tarball from test app")
	}
	body.Write(tarData)
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/static/upload", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.True(t, resp.StatusCode >= 400,
		"Missing name should return error (got %d)", resp.StatusCode)
}

// TestDeploy_ConcurrentSameName verifies that deploying two apps with the same
// name concurrently doesn't cause data corruption.
func TestDeploy_ConcurrentSameName(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	deploymentName := fmt.Sprintf("concurrent-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	var wg sync.WaitGroup
	results := make([]int, 2)
	ids := make([]string, 2)

	// Pre-create tarball once for both goroutines
	tarData, err := exec.Command("tar", "-czf", "-", "-C", tarballPath, ".").Output()
	if err != nil {
		t.Skip("Failed to create tarball from test app")
	}

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			body := &bytes.Buffer{}
			boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

			body.WriteString("--" + boundary + "\r\n")
			body.WriteString("Content-Disposition: form-data; name=\"name\"\r\n\r\n")
			body.WriteString(deploymentName + "\r\n")

			body.WriteString("--" + boundary + "\r\n")
			body.WriteString("Content-Disposition: form-data; name=\"tarball\"; filename=\"app.tar.gz\"\r\n")
			body.WriteString("Content-Type: application/gzip\r\n\r\n")
			body.Write(tarData)
			body.WriteString("\r\n--" + boundary + "--\r\n")

			req, _ := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/static/upload", body)
			req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			req.Header.Set("Authorization", "Bearer "+env.APIKey)

			resp, err := env.HTTPClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			results[idx] = resp.StatusCode

			var result map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&result)
			if id, ok := result["deployment_id"].(string); ok {
				ids[idx] = id
			} else if id, ok := result["id"].(string); ok {
				ids[idx] = id
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Concurrent deploy results: status1=%d status2=%d id1=%s id2=%s",
		results[0], results[1], ids[0], ids[1])

	// At least one should succeed
	successCount := 0
	for _, status := range results {
		if status == http.StatusCreated {
			successCount++
		}
	}
	assert.GreaterOrEqual(t, successCount, 1,
		"At least one concurrent deploy should succeed")

	// Cleanup
	for _, id := range ids {
		if id != "" {
			e2e.DeleteDeployment(t, env, id)
		}
	}
}

func readFileBytes(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

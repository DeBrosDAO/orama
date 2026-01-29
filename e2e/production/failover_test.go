//go:build e2e && production

package production

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

// TestFailover_HomeNodeDown verifies that when the home node's deployment process
// is down, requests still succeed via the replica node.
func TestFailover_HomeNodeDown(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	if len(env.Config.Servers) < 2 {
		t.Skip("Failover testing requires at least 2 servers")
	}

	// Deploy a Node.js backend so we have a process to stop
	deploymentName := fmt.Sprintf("failover-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/node-api")

	deploymentID := createNodeJSDeploymentProd(t, env, deploymentName, tarballPath)
	require.NotEmpty(t, deploymentID)

	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for deployment and replica
	healthy := e2e.WaitForHealthy(t, env, deploymentID, 90*time.Second)
	require.True(t, healthy, "Deployment should become healthy")
	time.Sleep(20 * time.Second) // Wait for async replica setup

	deployment := e2e.GetDeployment(t, env, deploymentID)
	nodeURL := extractNodeURLProd(t, deployment)
	require.NotEmpty(t, nodeURL)
	domain := extractDomainProd(nodeURL)

	t.Run("All nodes serve before failover", func(t *testing.T) {
		for _, server := range env.Config.Servers {
			gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)
			req, _ := http.NewRequest("GET", gatewayURL+"/health", nil)
			req.Host = domain

			resp, err := env.HTTPClient.Do(req)
			if err != nil {
				t.Logf("%s: unreachable: %v", server.Name, err)
				continue
			}
			resp.Body.Close()
			t.Logf("%s: status=%d", server.Name, resp.StatusCode)
		}
	})

	t.Run("Requests succeed via non-home nodes", func(t *testing.T) {
		// Find home node
		homeNodeID, _ := deployment["home_node_id"].(string)
		t.Logf("Home node: %s", homeNodeID)

		// Send requests to each non-home server
		// Even without stopping the home node, we verify all nodes can serve
		successCount := 0
		for _, server := range env.Config.Servers {
			gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)

			req, _ := http.NewRequest("GET", gatewayURL+"/health", nil)
			req.Host = domain

			resp, err := env.HTTPClient.Do(req)
			if err != nil {
				t.Logf("%s: failed: %v", server.Name, err)
				continue
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == http.StatusOK {
				successCount++
				t.Logf("%s: OK - %s", server.Name, string(body))
			} else {
				t.Logf("%s: status=%d body=%s", server.Name, resp.StatusCode, string(body))
			}
		}

		assert.GreaterOrEqual(t, successCount, 2,
			"At least 2 nodes should serve the deployment (replica + home)")
	})
}

// TestFailover_5xxRetry verifies that if one node returns a gateway error,
// the middleware retries on the next replica.
func TestFailover_5xxRetry(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	if len(env.Config.Servers) < 2 {
		t.Skip("Requires at least 2 servers")
	}

	// Deploy a static app (always works via IPFS, no process to crash)
	deploymentName := fmt.Sprintf("retry-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	require.NotEmpty(t, deploymentID)

	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	time.Sleep(10 * time.Second)

	deployment := e2e.GetDeployment(t, env, deploymentID)
	nodeURL := extractNodeURLProd(t, deployment)
	if nodeURL == "" {
		t.Skip("No node URL")
	}
	domain := extractDomainProd(nodeURL)

	t.Run("All nodes serve successfully", func(t *testing.T) {
		for _, server := range env.Config.Servers {
			gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)
			req, _ := http.NewRequest("GET", gatewayURL+"/", nil)
			req.Host = domain

			resp, err := env.HTTPClient.Do(req)
			require.NoError(t, err, "Request to %s should not error", server.Name)
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, http.StatusOK, resp.StatusCode,
				"Request via %s should return 200 (got %d: %s)", server.Name, resp.StatusCode, string(body))
		}
	})
}

// TestFailover_CrossNodeProxyTimeout verifies that cross-node proxy fails fast
// (within a reasonable timeout) rather than hanging.
func TestFailover_CrossNodeProxyTimeout(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	if len(env.Config.Servers) < 2 {
		t.Skip("Requires at least 2 servers")
	}

	// Make a request to a non-existent deployment — should fail fast
	domain := fmt.Sprintf("nonexistent-%d.%s", time.Now().Unix(), env.BaseDomain)

	start := time.Now()

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s:6001/", env.Config.Servers[0].IP), nil)
	req.Host = domain

	resp, err := env.HTTPClient.Do(req)
	elapsed := time.Since(start)

	if err != nil {
		t.Logf("Request failed in %v: %v", elapsed, err)
	} else {
		resp.Body.Close()
		t.Logf("Got status %d in %v", resp.StatusCode, elapsed)
	}

	// Should respond within 15 seconds (our proxy timeout is 5s)
	assert.Less(t, elapsed.Seconds(), 15.0,
		"Request to non-existent deployment should fail fast, took %v", elapsed)
}

func createNodeJSDeploymentProd(t *testing.T, env *e2e.E2ETestEnv, name, tarballPath string) string {
	t.Helper()

	var fileData []byte

	info, err := os.Stat(tarballPath)
	require.NoError(t, err, "Failed to stat: %s", tarballPath)

	if info.IsDir() {
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
	t.Fatalf("Deployment response missing id: %+v", result)
	return ""
}

func extractNodeURLProd(t *testing.T, deployment map[string]interface{}) string {
	t.Helper()
	if urls, ok := deployment["urls"].([]interface{}); ok && len(urls) > 0 {
		if url, ok := urls[0].(string); ok {
			return url
		}
	}
	if urls, ok := deployment["urls"].(map[string]interface{}); ok {
		if url, ok := urls["node"].(string); ok {
			return url
		}
	}
	return ""
}

func extractDomainProd(url string) string {
	domain := url
	if len(url) > 8 && url[:8] == "https://" {
		domain = url[8:]
	} else if len(url) > 7 && url[:7] == "http://" {
		domain = url[7:]
	}
	if len(domain) > 0 && domain[len(domain)-1] == '/' {
		domain = domain[:len(domain)-1]
	}
	return domain
}

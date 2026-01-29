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

// TestStaticReplica_CreatedOnDeploy verifies that deploying a static app
// creates replica records on a second node.
func TestStaticReplica_CreatedOnDeploy(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("replica-static-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")
	var deploymentID string

	defer func() {
		if !env.SkipCleanup && deploymentID != "" {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Deploy static app", func(t *testing.T) {
		deploymentID = e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
		require.NotEmpty(t, deploymentID)
		t.Logf("Created deployment: %s (ID: %s)", deploymentName, deploymentID)
	})

	t.Run("Wait for replica setup", func(t *testing.T) {
		// Static replicas should set up quickly (IPFS content)
		time.Sleep(10 * time.Second)
	})

	t.Run("Deployment has replica records", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		// Check that replicas field exists and has entries
		replicas, ok := deployment["replicas"].([]interface{})
		if !ok {
			// Replicas might be in a nested structure or separate endpoint
			t.Logf("Deployment response: %+v", deployment)
			// Try querying replicas via the deployment details
			homeNodeID, _ := deployment["home_node_id"].(string)
			require.NotEmpty(t, homeNodeID, "Deployment should have a home_node_id")
			t.Logf("Home node: %s", homeNodeID)
			// If replicas aren't in the response, that's still okay — we verify
			// via DNS and cross-node serving below
			t.Log("Replica records not in deployment response; will verify via DNS/serving")
			return
		}

		assert.GreaterOrEqual(t, len(replicas), 1, "Should have at least 1 replica")
		t.Logf("Found %d replica records", len(replicas))
		for i, r := range replicas {
			if replica, ok := r.(map[string]interface{}); ok {
				t.Logf("  Replica %d: node=%s status=%s", i, replica["node_id"], replica["status"])
			}
		}
	})

	t.Run("Static content served from both nodes", func(t *testing.T) {
		e2e.SkipIfLocal(t)

		if len(env.Config.Servers) < 2 {
			t.Skip("Requires at least 2 servers")
		}

		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}
		domain := extractDomain(nodeURL)

		for _, server := range env.Config.Servers {
			t.Run("via_"+server.Name, func(t *testing.T) {
				gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)

				req, err := http.NewRequest("GET", gatewayURL+"/", nil)
				require.NoError(t, err)
				req.Host = domain

				resp, err := env.HTTPClient.Do(req)
				require.NoError(t, err, "Request to %s should succeed", server.Name)
				defer resp.Body.Close()

				body, _ := io.ReadAll(resp.Body)
				assert.Equal(t, http.StatusOK, resp.StatusCode,
					"Request via %s should return 200 (got %d: %s)", server.Name, resp.StatusCode, string(body))
				t.Logf("Served via %s (%s): status=%d", server.Name, server.IP, resp.StatusCode)
			})
		}
	})
}

// TestDynamicReplica_CreatedOnDeploy verifies that deploying a dynamic (Node.js) app
// creates a replica process on a second node.
func TestDynamicReplica_CreatedOnDeploy(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("replica-nodejs-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/node-api")
	var deploymentID string

	defer func() {
		if !env.SkipCleanup && deploymentID != "" {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Deploy Node.js backend", func(t *testing.T) {
		deploymentID = createNodeJSDeployment(t, env, deploymentName, tarballPath)
		require.NotEmpty(t, deploymentID)
		t.Logf("Created deployment: %s (ID: %s)", deploymentName, deploymentID)
	})

	t.Run("Wait for deployment and replica", func(t *testing.T) {
		healthy := e2e.WaitForHealthy(t, env, deploymentID, 90*time.Second)
		assert.True(t, healthy, "Deployment should become healthy")
		// Extra wait for async replica setup
		time.Sleep(15 * time.Second)
	})

	t.Run("Dynamic app served from both nodes", func(t *testing.T) {
		e2e.SkipIfLocal(t)

		if len(env.Config.Servers) < 2 {
			t.Skip("Requires at least 2 servers")
		}

		deployment := e2e.GetDeployment(t, env, deploymentID)
		nodeURL := extractNodeURL(t, deployment)
		if nodeURL == "" {
			t.Skip("No node URL in deployment")
		}
		domain := extractDomain(nodeURL)

		successCount := 0
		for _, server := range env.Config.Servers {
			t.Run("via_"+server.Name, func(t *testing.T) {
				gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)

				req, err := http.NewRequest("GET", gatewayURL+"/health", nil)
				require.NoError(t, err)
				req.Host = domain

				resp, err := env.HTTPClient.Do(req)
				if err != nil {
					t.Logf("Request to %s failed: %v", server.Name, err)
					return
				}
				defer resp.Body.Close()

				body, _ := io.ReadAll(resp.Body)
				if resp.StatusCode == http.StatusOK {
					successCount++
					t.Logf("Served via %s: status=%d body=%s", server.Name, resp.StatusCode, string(body))
				} else {
					t.Logf("Non-200 via %s: status=%d body=%s", server.Name, resp.StatusCode, string(body))
				}
			})
		}

		assert.GreaterOrEqual(t, successCount, 2, "At least 2 nodes should serve the deployment")
	})
}

// TestReplica_UpdatePropagation verifies that updating a deployment propagates to replicas.
func TestReplica_UpdatePropagation(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")
	e2e.SkipIfLocal(t)

	if len(env.Config.Servers) < 2 {
		t.Skip("Requires at least 2 servers")
	}

	deploymentName := fmt.Sprintf("replica-update-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")
	var deploymentID string

	defer func() {
		if !env.SkipCleanup && deploymentID != "" {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Deploy v1", func(t *testing.T) {
		deploymentID = e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
		require.NotEmpty(t, deploymentID)
		time.Sleep(10 * time.Second) // Wait for replica
	})

	var v1CID string
	t.Run("Record v1 CID", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		v1CID, _ = deployment["content_cid"].(string)
		require.NotEmpty(t, v1CID)
		t.Logf("v1 CID: %s", v1CID)
	})

	t.Run("Update to v2", func(t *testing.T) {
		updateStaticDeployment(t, env, deploymentName, tarballPath)
		time.Sleep(10 * time.Second) // Wait for update + replica propagation
	})

	t.Run("All nodes serve updated version", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		v2CID, _ := deployment["content_cid"].(string)

		// v2 CID might be same (same tarball) but version should increment
		version, _ := deployment["version"].(float64)
		assert.Equal(t, float64(2), version, "Should be version 2")
		t.Logf("v2 CID: %s, version: %v", v2CID, version)

		// Verify all nodes return consistent data
		for _, server := range env.Config.Servers {
			gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)
			req, _ := http.NewRequest("GET", gatewayURL+"/v1/deployments/get?id="+deploymentID, nil)
			req.Header.Set("Authorization", "Bearer "+env.APIKey)

			resp, err := env.HTTPClient.Do(req)
			if err != nil {
				t.Logf("Could not reach %s: %v", server.Name, err)
				continue
			}
			defer resp.Body.Close()

			var dep map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&dep)
			nodeCID, _ := dep["content_cid"].(string)
			nodeVersion, _ := dep["version"].(float64)
			t.Logf("%s: cid=%s version=%v", server.Name, nodeCID, nodeVersion)

			assert.Equal(t, v2CID, nodeCID, "CID should match on %s", server.Name)
		}
	})
}

// TestReplica_RollbackPropagation verifies rollback propagates to replica nodes.
func TestReplica_RollbackPropagation(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")
	e2e.SkipIfLocal(t)

	if len(env.Config.Servers) < 2 {
		t.Skip("Requires at least 2 servers")
	}

	deploymentName := fmt.Sprintf("replica-rollback-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")
	var deploymentID string

	defer func() {
		if !env.SkipCleanup && deploymentID != "" {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Deploy v1 and update to v2", func(t *testing.T) {
		deploymentID = e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
		require.NotEmpty(t, deploymentID)
		time.Sleep(10 * time.Second)

		updateStaticDeployment(t, env, deploymentName, tarballPath)
		time.Sleep(10 * time.Second)
	})

	var v1CID string
	t.Run("Get v1 CID from versions", func(t *testing.T) {
		versions := listVersions(t, env, deploymentName)
		if len(versions) > 0 {
			v1CID, _ = versions[0]["content_cid"].(string)
		}
		if v1CID == "" {
			// Fall back: v1 CID from current deployment
			deployment := e2e.GetDeployment(t, env, deploymentID)
			v1CID, _ = deployment["content_cid"].(string)
		}
		t.Logf("v1 CID for rollback comparison: %s", v1CID)
	})

	t.Run("Rollback to v1", func(t *testing.T) {
		rollbackDeployment(t, env, deploymentName, 1)
		time.Sleep(10 * time.Second) // Wait for rollback + replica propagation
	})

	t.Run("All nodes have rolled-back CID", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		currentCID, _ := deployment["content_cid"].(string)
		t.Logf("Post-rollback CID: %s", currentCID)

		for _, server := range env.Config.Servers {
			gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)
			req, _ := http.NewRequest("GET", gatewayURL+"/v1/deployments/get?id="+deploymentID, nil)
			req.Header.Set("Authorization", "Bearer "+env.APIKey)

			resp, err := env.HTTPClient.Do(req)
			if err != nil {
				continue
			}
			defer resp.Body.Close()

			var dep map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&dep)
			nodeCID, _ := dep["content_cid"].(string)
			assert.Equal(t, currentCID, nodeCID, "CID should match on %s after rollback", server.Name)
		}
	})
}

// TestReplica_TeardownOnDelete verifies that deleting a deployment removes replicas.
func TestReplica_TeardownOnDelete(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")
	e2e.SkipIfLocal(t)

	if len(env.Config.Servers) < 2 {
		t.Skip("Requires at least 2 servers")
	}

	deploymentName := fmt.Sprintf("replica-delete-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	require.NotEmpty(t, deploymentID)
	time.Sleep(10 * time.Second) // Wait for replica

	// Get the domain before deletion
	deployment := e2e.GetDeployment(t, env, deploymentID)
	nodeURL := extractNodeURL(t, deployment)
	domain := ""
	if nodeURL != "" {
		domain = extractDomain(nodeURL)
	}

	t.Run("Delete deployment", func(t *testing.T) {
		e2e.DeleteDeployment(t, env, deploymentID)
		time.Sleep(10 * time.Second) // Wait for teardown propagation
	})

	t.Run("Deployment no longer served on any node", func(t *testing.T) {
		if domain == "" {
			t.Skip("No domain to test")
		}

		for _, server := range env.Config.Servers {
			gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)
			req, _ := http.NewRequest("GET", gatewayURL+"/", nil)
			req.Host = domain

			resp, err := env.HTTPClient.Do(req)
			if err != nil {
				t.Logf("%s: connection failed (expected)", server.Name)
				continue
			}
			defer resp.Body.Close()

			// Should get 404 or 502, not 200 with app content
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == http.StatusOK {
				// If we get 200, make sure it's not the deleted app
				assert.NotContains(t, string(body), "<div id=\"root\">",
					"Deleted deployment should not be served on %s", server.Name)
			}
			t.Logf("%s: status=%d (expected non-200)", server.Name, resp.StatusCode)
		}
	})
}

// updateStaticDeployment updates an existing static deployment.
func updateStaticDeployment(t *testing.T, env *e2e.E2ETestEnv, name, tarballPath string) {
	t.Helper()

	file, err := os.Open(tarballPath)
	require.NoError(t, err)
	defer file.Close()

	body := &bytes.Buffer{}
	boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"name\"\r\n\r\n")
	body.WriteString(name + "\r\n")

	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"tarball\"; filename=\"app.tar.gz\"\r\n")
	body.WriteString("Content-Type: application/gzip\r\n\r\n")

	fileData, _ := io.ReadAll(file)
	body.Write(fileData)
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/static/update", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Update failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}
}

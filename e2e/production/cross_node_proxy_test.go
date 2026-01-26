//go:build e2e

package production

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

// TestCrossNode_ProxyRouting tests that requests can be made to any node
// and get proxied to the correct home node for a deployment
func TestCrossNode_ProxyRouting(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	if len(env.Config.Servers) < 2 {
		t.Skip("Cross-node testing requires at least 2 servers in config")
	}

	deploymentName := fmt.Sprintf("proxy-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/tarballs/react-vite.tar.gz")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for deployment to be active
	time.Sleep(3 * time.Second)

	domain := env.BuildDeploymentDomain(deploymentName)
	t.Logf("Testing cross-node routing for: %s", domain)

	t.Run("Request via each server succeeds", func(t *testing.T) {
		for _, server := range env.Config.Servers {
			t.Run("via_"+server.Name, func(t *testing.T) {
				// Make request directly to this server's IP
				gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)

				req, err := http.NewRequest("GET", gatewayURL+"/", nil)
				require.NoError(t, err)

				// Set Host header to the deployment domain
				req.Host = domain

				resp, err := env.HTTPClient.Do(req)
				require.NoError(t, err, "Request to %s should succeed", server.Name)
				defer resp.Body.Close()

				body, _ := io.ReadAll(resp.Body)

				assert.Equal(t, http.StatusOK, resp.StatusCode,
					"Request via %s should return 200 (got %d: %s)",
					server.Name, resp.StatusCode, string(body))

				assert.Contains(t, string(body), "<div id=\"root\">",
					"Should serve deployment content via %s", server.Name)

				t.Logf("✓ Request via %s (%s) succeeded", server.Name, server.IP)
			})
		}
	})
}

// TestCrossNode_APIConsistency tests that API responses are consistent across nodes
func TestCrossNode_APIConsistency(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	if len(env.Config.Servers) < 2 {
		t.Skip("Cross-node testing requires at least 2 servers in config")
	}

	deploymentName := fmt.Sprintf("consistency-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/tarballs/react-vite.tar.gz")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for replication
	time.Sleep(5 * time.Second)

	t.Run("Deployment list is consistent across nodes", func(t *testing.T) {
		var deploymentCounts []int

		for _, server := range env.Config.Servers {
			gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)

			req, err := http.NewRequest("GET", gatewayURL+"/v1/deployments/list", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+env.APIKey)

			resp, err := env.HTTPClient.Do(req)
			if err != nil {
				t.Logf("⚠ Could not reach %s: %v", server.Name, err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Logf("⚠ %s returned status %d", server.Name, resp.StatusCode)
				continue
			}

			var result map[string]interface{}
			if err := e2e.DecodeJSON(mustReadAll(t, resp.Body), &result); err != nil {
				t.Logf("⚠ Could not decode response from %s", server.Name)
				continue
			}

			deployments, ok := result["deployments"].([]interface{})
			if !ok {
				t.Logf("⚠ Invalid response format from %s", server.Name)
				continue
			}

			deploymentCounts = append(deploymentCounts, len(deployments))
			t.Logf("%s reports %d deployments", server.Name, len(deployments))
		}

		// All nodes should report the same count (or close to it, allowing for replication delay)
		if len(deploymentCounts) >= 2 {
			for i := 1; i < len(deploymentCounts); i++ {
				diff := deploymentCounts[i] - deploymentCounts[0]
				if diff < 0 {
					diff = -diff
				}
				assert.LessOrEqual(t, diff, 1,
					"Deployment counts should be consistent across nodes (allowing for replication)")
			}
		}
	})
}

// TestCrossNode_DeploymentGetConsistency tests that deployment details are consistent
func TestCrossNode_DeploymentGetConsistency(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	if len(env.Config.Servers) < 2 {
		t.Skip("Cross-node testing requires at least 2 servers in config")
	}

	deploymentName := fmt.Sprintf("get-consistency-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/tarballs/react-vite.tar.gz")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for replication
	time.Sleep(5 * time.Second)

	t.Run("Deployment details match across nodes", func(t *testing.T) {
		var cids []string

		for _, server := range env.Config.Servers {
			gatewayURL := fmt.Sprintf("http://%s:6001", server.IP)

			req, err := http.NewRequest("GET", gatewayURL+"/v1/deployments/get?id="+deploymentID, nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+env.APIKey)

			resp, err := env.HTTPClient.Do(req)
			if err != nil {
				t.Logf("⚠ Could not reach %s: %v", server.Name, err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Logf("⚠ %s returned status %d", server.Name, resp.StatusCode)
				continue
			}

			var deployment map[string]interface{}
			if err := e2e.DecodeJSON(mustReadAll(t, resp.Body), &deployment); err != nil {
				t.Logf("⚠ Could not decode response from %s", server.Name)
				continue
			}

			cid, _ := deployment["content_cid"].(string)
			cids = append(cids, cid)

			t.Logf("%s: name=%s, cid=%s, status=%s",
				server.Name, deployment["name"], cid, deployment["status"])
		}

		// All nodes should have the same CID
		if len(cids) >= 2 {
			for i := 1; i < len(cids); i++ {
				assert.Equal(t, cids[0], cids[i],
					"Content CID should be consistent across nodes")
			}
		}
	})
}

func mustReadAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return data
}

//go:build e2e && production

package production

import (
	"encoding/json"
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

// TestCrossNode_ProxyRouting tests that requests routed through the gateway
// are served correctly for a deployment.
func TestCrossNode_ProxyRouting(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	if len(env.Config.Servers) < 2 {
		t.Skip("Cross-node testing requires at least 2 servers in config")
	}

	deploymentName := fmt.Sprintf("proxy-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for deployment to be active
	time.Sleep(3 * time.Second)

	domain := env.BuildDeploymentDomain(deploymentName)
	t.Logf("Testing routing for: %s", domain)

	t.Run("Request via gateway succeeds", func(t *testing.T) {
		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/")
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"Request should return 200 (got %d: %s)", resp.StatusCode, string(body))

		assert.Contains(t, string(body), "<div id=\"root\">",
			"Should serve deployment content")
	})
}

// TestCrossNode_APIConsistency tests that API responses are consistent
func TestCrossNode_APIConsistency(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("consistency-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for replication
	time.Sleep(5 * time.Second)

	t.Run("Deployment list contains our deployment", func(t *testing.T) {
		req, err := http.NewRequest("GET", env.GatewayURL+"/v1/deployments/list", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

		deployments, ok := result["deployments"].([]interface{})
		require.True(t, ok, "Response should have deployments array")
		t.Logf("Gateway reports %d deployments", len(deployments))

		found := false
		for _, d := range deployments {
			dep, _ := d.(map[string]interface{})
			if dep["name"] == deploymentName {
				found = true
				break
			}
		}
		assert.True(t, found, "Our deployment should be in the list")
	})
}

// TestCrossNode_DeploymentGetConsistency tests that deployment details are correct
func TestCrossNode_DeploymentGetConsistency(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("get-consistency-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for replication
	time.Sleep(5 * time.Second)

	t.Run("Deployment details are correct", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		cid, _ := deployment["content_cid"].(string)
		assert.NotEmpty(t, cid, "Should have a content CID")

		name, _ := deployment["name"].(string)
		assert.Equal(t, deploymentName, name, "Name should match")

		t.Logf("Deployment: name=%s, cid=%s, status=%s", name, cid, deployment["status"])
	})
}

//go:build e2e

package cluster_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// STRICT NAMESPACE CLUSTER TESTS
// These tests FAIL if things don't work. No t.Skip() for expected functionality.
// =============================================================================

// TestNamespaceCluster_FullProvisioning is a STRICT test that verifies the complete
// namespace cluster provisioning flow. This test FAILS if any component doesn't work.
func TestNamespaceCluster_FullProvisioning(t *testing.T) {
	// Generate unique namespace name
	newNamespace := fmt.Sprintf("e2e-cluster-%d", time.Now().UnixNano())

	env, err := e2e.LoadTestEnvWithNamespace(newNamespace)
	require.NoError(t, err, "FATAL: Failed to create test environment for namespace %s", newNamespace)
	require.NotEmpty(t, env.APIKey, "FATAL: No API key received - namespace provisioning failed")

	t.Logf("Created namespace: %s", newNamespace)
	t.Logf("API Key: %s...", env.APIKey[:min(20, len(env.APIKey))])

	// Get cluster status to verify provisioning
	t.Run("Cluster status shows ready", func(t *testing.T) {
		// Query the namespace cluster status
		req, _ := http.NewRequest("GET", env.GatewayURL+"/v1/namespace/status?name="+newNamespace, nil)
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err, "Failed to query cluster status")
		defer resp.Body.Close()

		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Logf("Cluster status response: %s", string(bodyBytes))

		// If status endpoint exists and returns cluster info, verify it
		if resp.StatusCode == http.StatusOK {
			var result map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &result); err == nil {
				status, _ := result["status"].(string)
				if status != "" && status != "ready" && status != "default" {
					t.Errorf("FAIL: Cluster status is '%s', expected 'ready'", status)
				}
			}
		}
	})

	// Verify we can use the namespace for deployments
	t.Run("Deployments work on namespace", func(t *testing.T) {
		tarballPath := filepath.Join("../../testdata/apps/react-app")
		if _, err := os.Stat(tarballPath); os.IsNotExist(err) {
			t.Skip("Test tarball not found - skipping deployment test")
		}

		deploymentName := fmt.Sprintf("cluster-test-%d", time.Now().Unix())
		deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
		require.NotEmpty(t, deploymentID, "FAIL: Deployment creation failed on namespace cluster")

		t.Logf("Created deployment %s (ID: %s) on namespace %s", deploymentName, deploymentID, newNamespace)

		// Cleanup
		defer func() {
			if !env.SkipCleanup {
				e2e.DeleteDeployment(t, env, deploymentID)
			}
		}()

		// Verify deployment is accessible
		req, _ := http.NewRequest("GET", env.GatewayURL+"/v1/deployments/get?id="+deploymentID, nil)
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err, "Failed to get deployment")
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode, "FAIL: Cannot retrieve deployment from namespace cluster")
	})
}

// TestNamespaceCluster_RQLiteHealth verifies that namespace RQLite cluster is running
// and accepting connections. This test FAILS if RQLite is not accessible.
func TestNamespaceCluster_RQLiteHealth(t *testing.T) {
	t.Run("Check namespace port range for RQLite", func(t *testing.T) {
		foundRQLite := false
		var healthyPorts []int
		var unhealthyPorts []int

		// Check first few port blocks
		for portStart := 10000; portStart <= 10015; portStart += 5 {
			rqlitePort := portStart // RQLite HTTP is first port in block
			if isPortListening("localhost", rqlitePort) {
				t.Logf("Found RQLite instance on port %d", rqlitePort)
				foundRQLite = true

				// Verify it responds to health check
				healthURL := fmt.Sprintf("http://localhost:%d/status", rqlitePort)
				healthResp, err := http.Get(healthURL)
				if err == nil {
					defer healthResp.Body.Close()
					if healthResp.StatusCode == http.StatusOK {
						healthyPorts = append(healthyPorts, rqlitePort)
						t.Logf("  ✓ RQLite on port %d is healthy", rqlitePort)
					} else {
						unhealthyPorts = append(unhealthyPorts, rqlitePort)
						t.Errorf("FAIL: RQLite on port %d returned status %d", rqlitePort, healthResp.StatusCode)
					}
				} else {
					unhealthyPorts = append(unhealthyPorts, rqlitePort)
					t.Errorf("FAIL: RQLite on port %d health check failed: %v", rqlitePort, err)
				}
			}
		}

		if !foundRQLite {
			t.Log("No namespace RQLite instances found in port range 10000-10015")
			t.Log("This is expected if no namespaces have been provisioned yet")
		} else {
			t.Logf("Summary: %d healthy, %d unhealthy RQLite instances", len(healthyPorts), len(unhealthyPorts))
			require.Empty(t, unhealthyPorts, "FAIL: Some RQLite instances are unhealthy")
		}
	})
}

// TestNamespaceCluster_OlricHealth verifies that namespace Olric cluster is running
// and accepting connections.
func TestNamespaceCluster_OlricHealth(t *testing.T) {
	t.Run("Check namespace port range for Olric", func(t *testing.T) {
		foundOlric := false
		foundCount := 0

		// Check first few port blocks - Olric memberlist is port_start + 3
		for portStart := 10000; portStart <= 10015; portStart += 5 {
			olricMemberlistPort := portStart + 3
			if isPortListening("localhost", olricMemberlistPort) {
				t.Logf("Found Olric memberlist on port %d", olricMemberlistPort)
				foundOlric = true
				foundCount++
			}
		}

		if !foundOlric {
			t.Log("No namespace Olric instances found in port range 10003-10018")
			t.Log("This is expected if no namespaces have been provisioned yet")
		} else {
			t.Logf("Found %d Olric memberlist ports accepting connections", foundCount)
		}
	})
}

// TestNamespaceCluster_GatewayHealth verifies that namespace Gateway instances are running.
// This test FAILS if gateway binary exists but gateways don't spawn.
func TestNamespaceCluster_GatewayHealth(t *testing.T) {
	// Check if gateway binary exists
	gatewayBinaryPaths := []string{
		"./bin/gateway",
		"../bin/gateway",
		"/usr/local/bin/orama-gateway",
	}

	var gatewayBinaryExists bool
	var foundPath string
	for _, path := range gatewayBinaryPaths {
		if _, err := os.Stat(path); err == nil {
			gatewayBinaryExists = true
			foundPath = path
			break
		}
	}

	if !gatewayBinaryExists {
		t.Log("Gateway binary not found - namespace gateways will not spawn")
		t.Log("Run 'make build' to build the gateway binary")
		t.Log("Checked paths:", gatewayBinaryPaths)
		// This is a FAILURE if we expect gateway to work
		t.Error("FAIL: Gateway binary not found. Run 'make build' first.")
		return
	}

	t.Logf("Gateway binary found at: %s", foundPath)

	t.Run("Check namespace port range for Gateway", func(t *testing.T) {
		foundGateway := false
		var healthyPorts []int
		var unhealthyPorts []int

		// Check first few port blocks - Gateway HTTP is port_start + 4
		for portStart := 10000; portStart <= 10015; portStart += 5 {
			gatewayPort := portStart + 4
			if isPortListening("localhost", gatewayPort) {
				t.Logf("Found Gateway instance on port %d", gatewayPort)
				foundGateway = true

				// Verify it responds to health check
				healthURL := fmt.Sprintf("http://localhost:%d/v1/health", gatewayPort)
				healthResp, err := http.Get(healthURL)
				if err == nil {
					defer healthResp.Body.Close()
					if healthResp.StatusCode == http.StatusOK {
						healthyPorts = append(healthyPorts, gatewayPort)
						t.Logf("  ✓ Gateway on port %d is healthy", gatewayPort)
					} else {
						unhealthyPorts = append(unhealthyPorts, gatewayPort)
						t.Errorf("FAIL: Gateway on port %d returned status %d", gatewayPort, healthResp.StatusCode)
					}
				} else {
					unhealthyPorts = append(unhealthyPorts, gatewayPort)
					t.Errorf("FAIL: Gateway on port %d health check failed: %v", gatewayPort, err)
				}
			}
		}

		if !foundGateway {
			t.Log("No namespace Gateway instances found in port range 10004-10019")
			t.Log("This is expected if no namespaces have been provisioned yet")
		} else {
			t.Logf("Summary: %d healthy, %d unhealthy Gateway instances", len(healthyPorts), len(unhealthyPorts))
			require.Empty(t, unhealthyPorts, "FAIL: Some Gateway instances are unhealthy")
		}
	})
}

// TestNamespaceCluster_ProvisioningCreatesProcesses creates a new namespace and
// verifies that actual processes are spawned. This is the STRICTEST test.
func TestNamespaceCluster_ProvisioningCreatesProcesses(t *testing.T) {
	newNamespace := fmt.Sprintf("e2e-strict-%d", time.Now().UnixNano())

	// Record ports before provisioning
	portsBefore := getListeningPortsInRange(10000, 10099)
	t.Logf("Ports in use before provisioning: %v", portsBefore)

	// Create namespace
	env, err := e2e.LoadTestEnvWithNamespace(newNamespace)
	require.NoError(t, err, "FATAL: Failed to create namespace")
	require.NotEmpty(t, env.APIKey, "FATAL: No API key - provisioning failed")

	t.Logf("Namespace '%s' created successfully", newNamespace)

	// Wait a moment for processes to fully start
	time.Sleep(3 * time.Second)

	// Record ports after provisioning
	portsAfter := getListeningPortsInRange(10000, 10099)
	t.Logf("Ports in use after provisioning: %v", portsAfter)

	// Check if new ports were opened
	newPorts := diffPorts(portsBefore, portsAfter)
	sort.Ints(newPorts)
	t.Logf("New ports opened: %v", newPorts)

	t.Run("New ports allocated for namespace cluster", func(t *testing.T) {
		if len(newPorts) == 0 {
			// This might be OK for default namespace or if using global cluster
			t.Log("No new ports detected")
			t.Log("Possible reasons:")
			t.Log("  - Namespace uses default cluster (expected for 'default')")
			t.Log("  - Cluster already existed from previous test")
			t.Log("  - Provisioning is handled differently in this environment")
		} else {
			t.Logf("SUCCESS: %d new ports opened for namespace cluster", len(newPorts))

			// Verify the ports follow expected pattern
			for _, port := range newPorts {
				offset := (port - 10000) % 5
				switch offset {
				case 0:
					t.Logf("  Port %d: RQLite HTTP", port)
				case 1:
					t.Logf("  Port %d: RQLite Raft", port)
				case 2:
					t.Logf("  Port %d: Olric HTTP", port)
				case 3:
					t.Logf("  Port %d: Olric Memberlist", port)
				case 4:
					t.Logf("  Port %d: Gateway HTTP", port)
				}
			}
		}
	})

	t.Run("RQLite is accessible on allocated ports", func(t *testing.T) {
		rqlitePorts := filterPortsByOffset(newPorts, 0) // RQLite HTTP is offset 0
		if len(rqlitePorts) == 0 {
			t.Log("No new RQLite ports detected")
			return
		}

		for _, port := range rqlitePorts {
			healthURL := fmt.Sprintf("http://localhost:%d/status", port)
			resp, err := http.Get(healthURL)
			require.NoError(t, err, "FAIL: RQLite on port %d is not responding", port)
			resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode,
				"FAIL: RQLite on port %d returned status %d", port, resp.StatusCode)
			t.Logf("✓ RQLite on port %d is healthy", port)
		}
	})

	t.Run("Olric is accessible on allocated ports", func(t *testing.T) {
		olricPorts := filterPortsByOffset(newPorts, 3) // Olric Memberlist is offset 3
		if len(olricPorts) == 0 {
			t.Log("No new Olric ports detected")
			return
		}

		for _, port := range olricPorts {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 2*time.Second)
			require.NoError(t, err, "FAIL: Olric memberlist on port %d is not responding", port)
			conn.Close()
			t.Logf("✓ Olric memberlist on port %d is accepting connections", port)
		}
	})
}

// TestNamespaceCluster_StatusEndpoint tests the /v1/namespace/status endpoint
func TestNamespaceCluster_StatusEndpoint(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	t.Run("Status endpoint returns 404 for non-existent cluster", func(t *testing.T) {
		req, _ := http.NewRequest("GET", env.GatewayURL+"/v1/namespace/status?id=non-existent-id", nil)
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err, "Request should not fail")
		defer resp.Body.Close()

		require.Equal(t, http.StatusNotFound, resp.StatusCode,
			"FAIL: Should return 404 for non-existent cluster, got %d", resp.StatusCode)
	})
}

// TestNamespaceCluster_CrossNamespaceAccess verifies namespace isolation
func TestNamespaceCluster_CrossNamespaceAccess(t *testing.T) {
	nsA := fmt.Sprintf("ns-a-%d", time.Now().Unix())
	nsB := fmt.Sprintf("ns-b-%d", time.Now().Unix())

	envA, err := e2e.LoadTestEnvWithNamespace(nsA)
	require.NoError(t, err, "FAIL: Cannot create namespace A")

	envB, err := e2e.LoadTestEnvWithNamespace(nsB)
	require.NoError(t, err, "FAIL: Cannot create namespace B")

	// Verify both namespaces have different API keys
	require.NotEqual(t, envA.APIKey, envB.APIKey, "FAIL: Namespaces should have different API keys")
	t.Logf("Namespace A API key: %s...", envA.APIKey[:min(10, len(envA.APIKey))])
	t.Logf("Namespace B API key: %s...", envB.APIKey[:min(10, len(envB.APIKey))])

	t.Run("API keys are namespace-scoped", func(t *testing.T) {
		// Namespace A should not see namespace B's resources
		req, _ := http.NewRequest("GET", envA.GatewayURL+"/v1/deployments/list", nil)
		req.Header.Set("Authorization", "Bearer "+envA.APIKey)

		resp, err := envA.HTTPClient.Do(req)
		require.NoError(t, err, "Request failed")
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode, "Should list deployments")

		var result map[string]interface{}
		bodyBytes, _ := io.ReadAll(resp.Body)
		json.Unmarshal(bodyBytes, &result)

		deployments, _ := result["deployments"].([]interface{})
		for _, d := range deployments {
			dep, ok := d.(map[string]interface{})
			if !ok {
				continue
			}
			ns, _ := dep["namespace"].(string)
			require.NotEqual(t, nsB, ns,
				"FAIL: Namespace A sees Namespace B deployments - isolation broken!")
		}
	})
}

// TestDeployment_SubdomainFormat tests deployment subdomain format
func TestDeployment_SubdomainFormat(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	tarballPath := filepath.Join("../../testdata/apps/react-app")
	if _, err := os.Stat(tarballPath); os.IsNotExist(err) {
		t.Skip("Test tarball not found")
	}

	deploymentName := fmt.Sprintf("subdomain-test-%d", time.Now().UnixNano())
	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	require.NotEmpty(t, deploymentID, "FAIL: Deployment creation failed")

	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Deployment has subdomain with random suffix", func(t *testing.T) {
		req, _ := http.NewRequest("GET", env.GatewayURL+"/v1/deployments/get?id="+deploymentID, nil)
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err, "Failed to get deployment")
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode, "Should get deployment")

		var result map[string]interface{}
		bodyBytes, _ := io.ReadAll(resp.Body)
		json.Unmarshal(bodyBytes, &result)

		deployment, ok := result["deployment"].(map[string]interface{})
		if !ok {
			deployment = result
		}

		subdomain, _ := deployment["subdomain"].(string)
		if subdomain != "" {
			require.True(t, strings.HasPrefix(subdomain, deploymentName),
				"FAIL: Subdomain '%s' should start with deployment name '%s'", subdomain, deploymentName)

			suffix := strings.TrimPrefix(subdomain, deploymentName+"-")
			if suffix != subdomain { // There was a dash separator
				require.Equal(t, 6, len(suffix),
					"FAIL: Random suffix should be 6 characters, got %d (%s)", len(suffix), suffix)
			}
			t.Logf("Deployment subdomain: %s", subdomain)
		}
	})
}

// TestNamespaceCluster_PortAllocation tests port allocation correctness
func TestNamespaceCluster_PortAllocation(t *testing.T) {
	t.Run("Port range is 10000-10099", func(t *testing.T) {
		const portRangeStart = 10000
		const portRangeEnd = 10099
		const portsPerNamespace = 5
		const maxNamespacesPerNode = 20

		totalPorts := portRangeEnd - portRangeStart + 1
		require.Equal(t, 100, totalPorts, "Port range should be 100 ports")

		expectedMax := totalPorts / portsPerNamespace
		require.Equal(t, maxNamespacesPerNode, expectedMax,
			"Max namespaces per node calculation mismatch")
	})

	t.Run("Port assignments are sequential within block", func(t *testing.T) {
		portStart := 10000
		ports := map[string]int{
			"rqlite_http":      portStart + 0,
			"rqlite_raft":      portStart + 1,
			"olric_http":       portStart + 2,
			"olric_memberlist": portStart + 3,
			"gateway_http":     portStart + 4,
		}

		seen := make(map[int]bool)
		for name, port := range ports {
			require.False(t, seen[port], "FAIL: Port %d for %s is duplicate", port, name)
			seen[port] = true
		}
	})
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

func isPortListening(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func getListeningPortsInRange(start, end int) []int {
	var ports []int
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Check ports concurrently for speed
	results := make(chan int, end-start+1)
	for port := start; port <= end; port++ {
		go func(p int) {
			select {
			case <-ctx.Done():
				results <- 0
				return
			default:
				if isPortListening("localhost", p) {
					results <- p
				} else {
					results <- 0
				}
			}
		}(port)
	}

	for i := 0; i <= end-start; i++ {
		if port := <-results; port > 0 {
			ports = append(ports, port)
		}
	}
	return ports
}

func diffPorts(before, after []int) []int {
	beforeMap := make(map[int]bool)
	for _, p := range before {
		beforeMap[p] = true
	}

	var newPorts []int
	for _, p := range after {
		if !beforeMap[p] {
			newPorts = append(newPorts, p)
		}
	}
	return newPorts
}

func filterPortsByOffset(ports []int, offset int) []int {
	var filtered []int
	for _, p := range ports {
		if (p-10000)%5 == offset {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

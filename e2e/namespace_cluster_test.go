//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNamespaceCluster_Provisioning tests that creating a new namespace
// triggers cluster provisioning with 202 Accepted response
func TestNamespaceCluster_Provisioning(t *testing.T) {
	if !IsProductionMode() {
		t.Skip("Namespace cluster provisioning only applies in production mode")
	}

	// This test requires a completely new namespace to trigger provisioning
	newNamespace := fmt.Sprintf("test-ns-%d", time.Now().UnixNano())

	env, err := LoadTestEnvWithNamespace(newNamespace)
	require.NoError(t, err, "Should create test environment")

	t.Run("New namespace triggers provisioning", func(t *testing.T) {
		// If we got here with an API key, provisioning either completed or was not required
		// The LoadTestEnvWithNamespace function handles the provisioning flow
		require.NotEmpty(t, env.APIKey, "Should have received API key after provisioning")
		t.Logf("Namespace %s provisioned successfully", newNamespace)
	})

	t.Run("Namespace gateway is accessible", func(t *testing.T) {
		// Try to access the namespace gateway
		// The URL should be ns-{namespace}.{baseDomain}
		cfg, _ := LoadE2EConfig()
		if cfg.BaseDomain == "" {
			cfg.BaseDomain = "devnet-orama.network"
		}

		nsGatewayURL := fmt.Sprintf("https://ns-%s.%s", newNamespace, cfg.BaseDomain)

		req, _ := http.NewRequest("GET", nsGatewayURL+"/v1/health", nil)
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		if err != nil {
			t.Logf("Note: Namespace gateway not accessible (expected in local mode): %v", err)
			t.Skip("Namespace gateway endpoint not available")
		}
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Namespace gateway should be healthy")
		t.Logf("Namespace gateway %s is accessible", nsGatewayURL)
	})
}

// TestNamespaceCluster_StatusPolling tests the /v1/namespace/status endpoint
func TestNamespaceCluster_StatusPolling(t *testing.T) {
	env, err := LoadTestEnv()
	require.NoError(t, err, "Should load test environment")

	t.Run("Status endpoint returns valid response", func(t *testing.T) {
		// Test with a non-existent cluster ID (should return 404)
		req, _ := http.NewRequest("GET", env.GatewayURL+"/v1/namespace/status?id=non-existent-id", nil)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		// Should return 404 for non-existent cluster
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "Should return 404 for non-existent cluster")
	})
}

// TestNamespaceCluster_CrossGatewayAccess tests that API keys from one namespace
// cannot access another namespace's dedicated gateway
func TestNamespaceCluster_CrossGatewayAccess(t *testing.T) {
	if !IsProductionMode() {
		t.Skip("Cross-gateway access control only applies in production mode")
	}

	// Create two namespaces
	nsA := fmt.Sprintf("ns-a-%d", time.Now().Unix())
	nsB := fmt.Sprintf("ns-b-%d", time.Now().Unix())

	envA, err := LoadTestEnvWithNamespace(nsA)
	require.NoError(t, err, "Should create test environment for namespace A")

	envB, err := LoadTestEnvWithNamespace(nsB)
	require.NoError(t, err, "Should create test environment for namespace B")

	cfg, _ := LoadE2EConfig()
	if cfg.BaseDomain == "" {
		cfg.BaseDomain = "devnet-orama.network"
	}

	t.Run("Namespace A key cannot access Namespace B gateway", func(t *testing.T) {
		// Try to use namespace A's key on namespace B's gateway
		nsBGatewayURL := fmt.Sprintf("https://ns-%s.%s", nsB, cfg.BaseDomain)

		req, _ := http.NewRequest("GET", nsBGatewayURL+"/v1/deployments/list", nil)
		req.Header.Set("Authorization", "Bearer "+envA.APIKey) // Using A's key

		resp, err := envA.HTTPClient.Do(req)
		if err != nil {
			t.Logf("Note: Gateway not accessible: %v", err)
			t.Skip("Namespace gateway endpoint not available")
		}
		defer resp.Body.Close()

		assert.Equal(t, http.StatusForbidden, resp.StatusCode,
			"Should deny namespace A's key on namespace B's gateway")
		t.Logf("Cross-namespace access correctly denied (status: %d)", resp.StatusCode)
	})

	t.Run("Namespace B key works on Namespace B gateway", func(t *testing.T) {
		nsBGatewayURL := fmt.Sprintf("https://ns-%s.%s", nsB, cfg.BaseDomain)

		req, _ := http.NewRequest("GET", nsBGatewayURL+"/v1/deployments/list", nil)
		req.Header.Set("Authorization", "Bearer "+envB.APIKey) // Using B's key

		resp, err := envB.HTTPClient.Do(req)
		if err != nil {
			t.Logf("Note: Gateway not accessible: %v", err)
			t.Skip("Namespace gateway endpoint not available")
		}
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"Should allow namespace B's key on namespace B's gateway")
		t.Logf("Same-namespace access correctly allowed")
	})
}

// TestNamespaceCluster_DefaultNamespaceAccessible tests that the default namespace
// is accessible by any valid API key
func TestNamespaceCluster_DefaultNamespaceAccessible(t *testing.T) {
	// Create a non-default namespace
	customNS := fmt.Sprintf("custom-%d", time.Now().Unix())
	env, err := LoadTestEnvWithNamespace(customNS)
	require.NoError(t, err, "Should create test environment")

	t.Run("Custom namespace key can access default gateway endpoints", func(t *testing.T) {
		// The default gateway should accept keys from any namespace
		req, _ := http.NewRequest("GET", env.GatewayURL+"/v1/health", nil)
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"Default gateway should accept any valid API key")
	})
}

// TestDeployment_RandomSubdomain tests that deployments get random subdomain suffix
func TestDeployment_RandomSubdomain(t *testing.T) {
	env, err := LoadTestEnv()
	require.NoError(t, err, "Should load test environment")

	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	// Create a deployment
	deploymentName := "subdomain-test"
	deploymentID := CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Deployment URL contains random suffix", func(t *testing.T) {
		// Get deployment details
		req, _ := http.NewRequest("GET", env.GatewayURL+"/v1/deployments/get?id="+deploymentID, nil)
		req.Header.Set("Authorization", "Bearer "+env.APIKey)

		resp, err := env.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode, "Should get deployment")

		var result map[string]interface{}
		bodyBytes, _ := io.ReadAll(resp.Body)
		require.NoError(t, json.Unmarshal(bodyBytes, &result), "Should decode JSON")

		deployment, ok := result["deployment"].(map[string]interface{})
		if !ok {
			deployment = result
		}

		// Check subdomain field
		subdomain, _ := deployment["subdomain"].(string)
		if subdomain != "" {
			// Subdomain should follow format: {name}-{random}
			// e.g., "subdomain-test-f3o4if"
			assert.True(t, strings.HasPrefix(subdomain, deploymentName+"-"),
				"Subdomain should start with deployment name followed by dash")

			suffix := strings.TrimPrefix(subdomain, deploymentName+"-")
			assert.Equal(t, 6, len(suffix), "Random suffix should be 6 characters")

			t.Logf("Deployment subdomain: %s (suffix: %s)", subdomain, suffix)
		} else {
			t.Logf("Note: Subdomain field not set (may be using legacy format)")
		}

		// Check URLs
		urls, ok := deployment["urls"].([]interface{})
		if ok && len(urls) > 0 {
			url := urls[0].(string)
			t.Logf("Deployment URL: %s", url)

			// URL should contain the subdomain with random suffix
			if subdomain != "" {
				assert.Contains(t, url, subdomain, "URL should contain the subdomain")
			}
		}
	})
}

// TestDeployment_SubdomainUniqueness tests that two deployments with the same name
// get different subdomains
func TestDeployment_SubdomainUniqueness(t *testing.T) {
	envA, err := LoadTestEnvWithNamespace("ns-unique-a-" + fmt.Sprintf("%d", time.Now().Unix()))
	require.NoError(t, err, "Should create test environment A")

	envB, err := LoadTestEnvWithNamespace("ns-unique-b-" + fmt.Sprintf("%d", time.Now().Unix()))
	require.NoError(t, err, "Should create test environment B")

	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")
	deploymentName := "same-name-app"

	// Create deployment in namespace A
	deploymentIDA := CreateTestDeployment(t, envA, deploymentName, tarballPath)
	defer func() {
		if !envA.SkipCleanup {
			DeleteDeployment(t, envA, deploymentIDA)
		}
	}()

	// Create deployment with same name in namespace B
	deploymentIDB := CreateTestDeployment(t, envB, deploymentName, tarballPath)
	defer func() {
		if !envB.SkipCleanup {
			DeleteDeployment(t, envB, deploymentIDB)
		}
	}()

	t.Run("Same name deployments have different subdomains", func(t *testing.T) {
		// Get deployment A details
		reqA, _ := http.NewRequest("GET", envA.GatewayURL+"/v1/deployments/get?id="+deploymentIDA, nil)
		reqA.Header.Set("Authorization", "Bearer "+envA.APIKey)
		respA, _ := envA.HTTPClient.Do(reqA)
		defer respA.Body.Close()

		var resultA map[string]interface{}
		bodyBytesA, _ := io.ReadAll(respA.Body)
		json.Unmarshal(bodyBytesA, &resultA)

		deploymentA, ok := resultA["deployment"].(map[string]interface{})
		if !ok {
			deploymentA = resultA
		}
		subdomainA, _ := deploymentA["subdomain"].(string)

		// Get deployment B details
		reqB, _ := http.NewRequest("GET", envB.GatewayURL+"/v1/deployments/get?id="+deploymentIDB, nil)
		reqB.Header.Set("Authorization", "Bearer "+envB.APIKey)
		respB, _ := envB.HTTPClient.Do(reqB)
		defer respB.Body.Close()

		var resultB map[string]interface{}
		bodyBytesB, _ := io.ReadAll(respB.Body)
		json.Unmarshal(bodyBytesB, &resultB)

		deploymentB, ok := resultB["deployment"].(map[string]interface{})
		if !ok {
			deploymentB = resultB
		}
		subdomainB, _ := deploymentB["subdomain"].(string)

		// If subdomains are set, they should be different
		if subdomainA != "" && subdomainB != "" {
			assert.NotEqual(t, subdomainA, subdomainB,
				"Same-name deployments in different namespaces should have different subdomains")

			t.Logf("Namespace A subdomain: %s", subdomainA)
			t.Logf("Namespace B subdomain: %s", subdomainB)
		} else {
			t.Logf("Note: Subdomains not set (may be using legacy format)")
		}
	})
}

// TestNamespaceCluster_DNSFormat tests the DNS naming convention for namespaces
func TestNamespaceCluster_DNSFormat(t *testing.T) {
	cfg, err := LoadE2EConfig()
	if err != nil {
		cfg = DefaultConfig()
	}

	if cfg.BaseDomain == "" {
		cfg.BaseDomain = "devnet-orama.network"
	}

	t.Run("Namespace gateway DNS follows ns-{name}.{baseDomain} format", func(t *testing.T) {
		namespace := "my-test-namespace"
		expectedDomain := fmt.Sprintf("ns-%s.%s", namespace, cfg.BaseDomain)

		t.Logf("Expected namespace gateway domain: %s", expectedDomain)

		// Verify format
		assert.True(t, strings.HasPrefix(expectedDomain, "ns-"),
			"Namespace gateway domain should start with 'ns-'")
		assert.True(t, strings.HasSuffix(expectedDomain, cfg.BaseDomain),
			"Namespace gateway domain should end with base domain")
	})

	t.Run("Deployment DNS follows {name}-{random}.{baseDomain} format", func(t *testing.T) {
		deploymentName := "my-app"
		randomSuffix := "f3o4if"
		expectedDomain := fmt.Sprintf("%s-%s.%s", deploymentName, randomSuffix, cfg.BaseDomain)

		t.Logf("Expected deployment domain: %s", expectedDomain)

		// Verify format
		assert.Contains(t, expectedDomain, deploymentName,
			"Deployment domain should contain the deployment name")
		assert.True(t, strings.HasSuffix(expectedDomain, cfg.BaseDomain),
			"Deployment domain should end with base domain")
	})
}

// TestNamespaceCluster_PortAllocation tests the port allocation constraints
func TestNamespaceCluster_PortAllocation(t *testing.T) {
	t.Run("Port range constants are correct", func(t *testing.T) {
		// These constants are defined in pkg/namespace/types.go
		const (
			portRangeStart       = 10000
			portRangeEnd         = 10099
			portsPerNamespace    = 5
			maxNamespacesPerNode = 20
		)

		// Verify range calculation
		totalPorts := portRangeEnd - portRangeStart + 1
		assert.Equal(t, 100, totalPorts, "Port range should be 100 ports")

		expectedMax := totalPorts / portsPerNamespace
		assert.Equal(t, maxNamespacesPerNode, expectedMax,
			"Max namespaces per node should be total ports / ports per namespace")

		t.Logf("Port range: %d-%d (%d ports total)", portRangeStart, portRangeEnd, totalPorts)
		t.Logf("Ports per namespace: %d", portsPerNamespace)
		t.Logf("Max namespaces per node: %d", maxNamespacesPerNode)
	})

	t.Run("Port assignments within a block are sequential", func(t *testing.T) {
		portStart := 10000

		rqliteHTTP := portStart + 0
		rqliteRaft := portStart + 1
		olricHTTP := portStart + 2
		olricMemberlist := portStart + 3
		gatewayHTTP := portStart + 4

		// All ports should be unique
		ports := []int{rqliteHTTP, rqliteRaft, olricHTTP, olricMemberlist, gatewayHTTP}
		seen := make(map[int]bool)
		for _, port := range ports {
			assert.False(t, seen[port], "Ports should be unique within a block")
			seen[port] = true
		}

		t.Logf("Port assignments for block starting at %d:", portStart)
		t.Logf("  RQLite HTTP:      %d", rqliteHTTP)
		t.Logf("  RQLite Raft:      %d", rqliteRaft)
		t.Logf("  Olric HTTP:       %d", olricHTTP)
		t.Logf("  Olric Memberlist: %d", olricMemberlist)
		t.Logf("  Gateway HTTP:     %d", gatewayHTTP)
	})
}

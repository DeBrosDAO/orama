//go:build e2e && production

package production

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDNS_DeploymentResolution tests that deployed applications are resolvable via DNS
// This test requires production mode as it performs real DNS lookups
func TestDNS_DeploymentResolution(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("dns-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/tarballs/react-vite.tar.gz")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for DNS propagation
	domain := env.BuildDeploymentDomain(deploymentName)
	t.Logf("Testing DNS resolution for: %s", domain)

	t.Run("DNS resolves to valid server IP", func(t *testing.T) {
		// Allow some time for DNS propagation
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var ips []string
		var err error

		// Poll for DNS resolution
		for {
			select {
			case <-ctx.Done():
				t.Fatalf("DNS resolution timeout for %s", domain)
			default:
				ips, err = net.LookupHost(domain)
				if err == nil && len(ips) > 0 {
					goto resolved
				}
				time.Sleep(2 * time.Second)
			}
		}

	resolved:
		t.Logf("DNS resolved: %s -> %v", domain, ips)
		assert.NotEmpty(t, ips, "Should have IP addresses")

		// Verify resolved IP is one of our servers
		validIPs := e2e.GetServerIPs(env.Config)
		if len(validIPs) > 0 {
			found := false
			for _, ip := range ips {
				for _, validIP := range validIPs {
					if ip == validIP {
						found = true
						break
					}
				}
			}
			assert.True(t, found, "Resolved IP should be one of our servers: %v (valid: %v)", ips, validIPs)
		}
	})
}

// TestDNS_BaseDomainResolution tests that the base domain resolves correctly
func TestDNS_BaseDomainResolution(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	t.Run("Base domain resolves", func(t *testing.T) {
		ips, err := net.LookupHost(env.BaseDomain)
		require.NoError(t, err, "Base domain %s should resolve", env.BaseDomain)
		assert.NotEmpty(t, ips, "Should have IP addresses")

		t.Logf("✓ Base domain %s resolves to: %v", env.BaseDomain, ips)
	})
}

// TestDNS_WildcardResolution tests wildcard DNS for arbitrary subdomains
func TestDNS_WildcardResolution(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	t.Run("Wildcard subdomain resolves", func(t *testing.T) {
		// Test with a random subdomain that doesn't exist as a deployment
		randomSubdomain := fmt.Sprintf("random-test-%d.%s", time.Now().UnixNano(), env.BaseDomain)

		ips, err := net.LookupHost(randomSubdomain)
		if err != nil {
			// DNS may not support wildcard - that's OK for some setups
			t.Logf("⚠ Wildcard DNS not configured (this may be expected): %v", err)
			t.Skip("Wildcard DNS not configured")
			return
		}

		assert.NotEmpty(t, ips, "Wildcard subdomain should resolve")
		t.Logf("✓ Wildcard subdomain resolves: %s -> %v", randomSubdomain, ips)
	})
}

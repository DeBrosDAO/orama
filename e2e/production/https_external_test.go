//go:build e2e && production

package production

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPS_ExternalAccess tests that deployed apps are accessible via HTTPS
// from the public internet with valid SSL certificates.
//
// This test requires:
// - Orama deployed on a VPS with a real domain
// - DNS properly configured
// - Run with: go test -v -tags "e2e production" -run TestHTTPS ./e2e/production/...
func TestHTTPS_ExternalAccess(t *testing.T) {
	// Skip if not configured for external testing
	externalURL := os.Getenv("ORAMA_EXTERNAL_URL")
	if externalURL == "" {
		t.Skip("ORAMA_EXTERNAL_URL not set - skipping external HTTPS test")
	}

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("https-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/tarballs/react-vite.tar.gz")
	var deploymentID string

	// Cleanup after test
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

	var deploymentDomain string

	t.Run("Get deployment domain", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)

		nodeURL := extractNodeURL(t, deployment)
		require.NotEmpty(t, nodeURL, "Deployment should have node URL")

		deploymentDomain = extractDomain(nodeURL)
		t.Logf("Deployment domain: %s", deploymentDomain)
	})

	t.Run("Wait for DNS propagation", func(t *testing.T) {
		// Poll DNS until the domain resolves
		deadline := time.Now().Add(2 * time.Minute)

		for time.Now().Before(deadline) {
			ips, err := net.LookupHost(deploymentDomain)
			if err == nil && len(ips) > 0 {
				t.Logf("DNS resolved: %s -> %v", deploymentDomain, ips)
				return
			}
			t.Logf("DNS not yet resolved, waiting...")
			time.Sleep(5 * time.Second)
		}

		t.Fatalf("DNS did not resolve within timeout for %s", deploymentDomain)
	})

	t.Run("Test HTTPS access with valid certificate", func(t *testing.T) {
		// Create HTTP client that DOES verify certificates
		// (no InsecureSkipVerify - we want to test real SSL)
		client := &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					// Use default verification (validates certificate)
					InsecureSkipVerify: false,
				},
			},
		}

		url := fmt.Sprintf("https://%s/", deploymentDomain)
		t.Logf("Testing HTTPS: %s", url)

		resp, err := client.Get(url)
		require.NoError(t, err, "HTTPS request should succeed with valid certificate")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Should return 200 OK")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		// Verify it's our React app
		assert.Contains(t, string(body), "<div id=\"root\">", "Should serve React app")

		t.Logf("HTTPS test passed: %s returned %d", url, resp.StatusCode)
	})

	t.Run("Verify SSL certificate details", func(t *testing.T) {
		conn, err := tls.Dial("tcp", deploymentDomain+":443", nil)
		require.NoError(t, err, "TLS dial should succeed")
		defer conn.Close()

		state := conn.ConnectionState()
		require.NotEmpty(t, state.PeerCertificates, "Should have peer certificates")

		cert := state.PeerCertificates[0]
		t.Logf("Certificate subject: %s", cert.Subject)
		t.Logf("Certificate issuer: %s", cert.Issuer)
		t.Logf("Certificate valid from: %s to %s", cert.NotBefore, cert.NotAfter)

		// Verify certificate is not expired
		assert.True(t, time.Now().After(cert.NotBefore), "Certificate should be valid (not before)")
		assert.True(t, time.Now().Before(cert.NotAfter), "Certificate should be valid (not expired)")

		// Verify domain matches
		err = cert.VerifyHostname(deploymentDomain)
		assert.NoError(t, err, "Certificate should be valid for domain %s", deploymentDomain)
	})
}

// TestHTTPS_DomainFormat verifies deployment URL format
func TestHTTPS_DomainFormat(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("domain-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/tarballs/react-vite.tar.gz")
	var deploymentID string

	// Cleanup after test
	defer func() {
		if !env.SkipCleanup && deploymentID != "" {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	t.Run("Deploy app and verify domain format", func(t *testing.T) {
		deploymentID = e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
		require.NotEmpty(t, deploymentID)

		deployment := e2e.GetDeployment(t, env, deploymentID)

		t.Logf("Deployment URLs: %+v", deployment["urls"])

		// Get deployment URL (handles both array and map formats)
		deploymentURL := extractNodeURL(t, deployment)
		assert.NotEmpty(t, deploymentURL, "Should have deployment URL")

		// URL should be simple format: {name}.{baseDomain} (NOT {name}.node-{shortID}.{baseDomain})
		if deploymentURL != "" {
			assert.NotContains(t, deploymentURL, ".node-", "URL should NOT contain node identifier (simplified format)")
			assert.Contains(t, deploymentURL, deploymentName, "URL should contain deployment name")
			t.Logf("Deployment URL: %s", deploymentURL)
		}
	})
}

func extractNodeURL(t *testing.T, deployment map[string]interface{}) string {
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

func extractDomain(url string) string {
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

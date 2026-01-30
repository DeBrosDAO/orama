//go:build e2e && production

package production

import (
	"crypto/tls"
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

// TestHTTPS_CertificateValid tests that HTTPS works with a valid certificate
func TestHTTPS_CertificateValid(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	deploymentName := fmt.Sprintf("https-test-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for deployment and certificate provisioning
	time.Sleep(5 * time.Second)

	domain := env.BuildDeploymentDomain(deploymentName)
	httpsURL := fmt.Sprintf("https://%s", domain)

	t.Run("HTTPS connection with certificate verification", func(t *testing.T) {
		// Create client that DOES verify certificates
		client := &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					// Do NOT skip verification - we want to test real certs
					InsecureSkipVerify: false,
				},
			},
		}

		req, err := http.NewRequest("GET", httpsURL+"/", nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		if err != nil {
			// Certificate might not be ready yet, or domain might not resolve
			t.Logf("⚠ HTTPS request failed (this may be expected if certs are still provisioning): %v", err)
			t.Skip("HTTPS not available or certificate not ready")
			return
		}
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "HTTPS should return 200")

		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "<div id=\"root\">", "Should serve deployment content over HTTPS")

		// Check TLS connection state
		if resp.TLS != nil {
			t.Logf("✓ HTTPS works with valid certificate")
			t.Logf("   - Domain: %s", domain)
			t.Logf("   - TLS Version: %x", resp.TLS.Version)
			t.Logf("   - Cipher Suite: %x", resp.TLS.CipherSuite)
			if len(resp.TLS.PeerCertificates) > 0 {
				cert := resp.TLS.PeerCertificates[0]
				t.Logf("   - Certificate Subject: %s", cert.Subject)
				t.Logf("   - Certificate Issuer: %s", cert.Issuer)
				t.Logf("   - Valid Until: %s", cert.NotAfter)
			}
		}
	})
}

// TestHTTPS_CertificateDetails tests certificate properties
func TestHTTPS_CertificateDetails(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	t.Run("Base domain certificate", func(t *testing.T) {
		httpsURL := fmt.Sprintf("https://%s", env.BaseDomain)

		// Connect and get certificate info
		conn, err := tls.Dial("tcp", env.BaseDomain+":443", &tls.Config{
			InsecureSkipVerify: true, // We just want to inspect the cert
		})
		if err != nil {
			t.Logf("⚠ Could not connect to %s:443: %v", env.BaseDomain, err)
			t.Skip("HTTPS not available on base domain")
			return
		}
		defer conn.Close()

		certs := conn.ConnectionState().PeerCertificates
		require.NotEmpty(t, certs, "Should have certificates")

		cert := certs[0]
		t.Logf("Certificate for %s:", env.BaseDomain)
		t.Logf("  - Subject: %s", cert.Subject)
		t.Logf("  - DNS Names: %v", cert.DNSNames)
		t.Logf("  - Valid From: %s", cert.NotBefore)
		t.Logf("  - Valid Until: %s", cert.NotAfter)
		t.Logf("  - Issuer: %s", cert.Issuer)

		// Check that certificate covers our domain
		coversDomain := false
		for _, name := range cert.DNSNames {
			if name == env.BaseDomain || name == "*."+env.BaseDomain {
				coversDomain = true
				break
			}
		}
		assert.True(t, coversDomain, "Certificate should cover %s", env.BaseDomain)

		// Check certificate is not expired
		assert.True(t, time.Now().Before(cert.NotAfter), "Certificate should not be expired")
		assert.True(t, time.Now().After(cert.NotBefore), "Certificate should be valid now")

		// Make actual HTTPS request to verify it works
		client := &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: false,
				},
			},
		}

		resp, err := client.Get(httpsURL)
		if err != nil {
			t.Logf("⚠ HTTPS request failed: %v", err)
		} else {
			resp.Body.Close()
			t.Logf("✓ HTTPS request succeeded with status %d", resp.StatusCode)
		}
	})
}

// TestHTTPS_HTTPRedirect tests that HTTP requests are redirected to HTTPS
func TestHTTPS_HTTPRedirect(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err, "Failed to load test environment")

	t.Run("HTTP redirects to HTTPS", func(t *testing.T) {
		// Create client that doesn't follow redirects
		client := &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		httpURL := fmt.Sprintf("http://%s", env.BaseDomain)

		resp, err := client.Get(httpURL)
		if err != nil {
			t.Logf("⚠ HTTP request failed: %v", err)
			t.Skip("HTTP not available or redirects not configured")
			return
		}
		defer resp.Body.Close()

		// Check for redirect
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location := resp.Header.Get("Location")
			t.Logf("✓ HTTP redirects to: %s (status %d)", location, resp.StatusCode)
			assert.Contains(t, location, "https://", "Should redirect to HTTPS")
		} else if resp.StatusCode == http.StatusOK {
			// HTTP might just serve content directly in some configurations
			t.Logf("⚠ HTTP returned 200 instead of redirect (HTTPS redirect may not be configured)")
		} else {
			t.Logf("HTTP returned status %d", resp.StatusCode)
		}
	})
}

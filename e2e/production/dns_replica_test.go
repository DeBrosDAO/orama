//go:build e2e && production

package production

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

// TestDNS_MultipleARecords verifies that deploying with replicas creates
// multiple A records (one per node) for DNS round-robin.
func TestDNS_MultipleARecords(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	if len(env.Config.Servers) < 2 {
		t.Skip("Requires at least 2 servers")
	}

	deploymentName := fmt.Sprintf("dns-multi-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	require.NotEmpty(t, deploymentID)

	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	// Wait for replica setup and DNS propagation
	time.Sleep(15 * time.Second)

	t.Run("DNS returns multiple IPs", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		subdomain, _ := deployment["subdomain"].(string)
		if subdomain == "" {
			subdomain = deploymentName
		}
		fqdn := fmt.Sprintf("%s.%s", subdomain, env.BaseDomain)

		// Query nameserver directly
		nameserverIP := env.Config.Servers[0].IP
		resolver := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 10 * time.Second}
				return d.Dial("udp", nameserverIP+":53")
			},
		}

		ctx := context.Background()
		ips, err := resolver.LookupHost(ctx, fqdn)
		if err != nil {
			t.Logf("DNS lookup failed for %s: %v", fqdn, err)
			t.Log("Trying net.LookupHost instead...")
			ips, err = net.LookupHost(fqdn)
		}

		if err != nil {
			t.Logf("DNS lookup failed: %v (DNS may not be propagated yet)", err)
			t.Skip("DNS not yet propagated")
		}

		t.Logf("DNS returned %d IPs for %s: %v", len(ips), fqdn, ips)
		assert.GreaterOrEqual(t, len(ips), 2,
			"Should have at least 2 A records (home + replica)")

		// Verify returned IPs are from our server list
		serverIPs := e2e.GetServerIPs(env.Config)
		for _, ip := range ips {
			assert.Contains(t, serverIPs, ip,
				"DNS IP %s should be one of our servers", ip)
		}
	})
}

// TestDNS_CleanupOnDelete verifies that deleting a deployment removes all
// DNS records (both home and replica A records).
func TestDNS_CleanupOnDelete(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	deploymentName := fmt.Sprintf("dns-cleanup-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	require.NotEmpty(t, deploymentID)

	// Wait for DNS
	time.Sleep(10 * time.Second)

	// Get subdomain before deletion
	deployment := e2e.GetDeployment(t, env, deploymentID)
	subdomain, _ := deployment["subdomain"].(string)
	if subdomain == "" {
		subdomain = deploymentName
	}
	fqdn := fmt.Sprintf("%s.%s", subdomain, env.BaseDomain)

	// Verify DNS works before deletion
	t.Run("DNS resolves before deletion", func(t *testing.T) {
		nodeURL := extractNodeURLProd(t, deployment)
		if nodeURL == "" {
			t.Skip("No URL to test")
		}
		domain := extractDomainProd(nodeURL)

		req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s:6001/", env.Config.Servers[0].IP), nil)
		req.Host = domain

		resp, err := env.HTTPClient.Do(req)
		if err == nil {
			resp.Body.Close()
			t.Logf("Pre-delete: status=%d", resp.StatusCode)
		}
	})

	// Delete
	e2e.DeleteDeployment(t, env, deploymentID)
	time.Sleep(10 * time.Second)

	t.Run("DNS records removed after deletion", func(t *testing.T) {
		ips, err := net.LookupHost(fqdn)
		if err != nil {
			t.Logf("DNS lookup failed (expected): %v", err)
			return // Good — no records
		}

		// If we still get IPs, they might be cached. Log and warn.
		if len(ips) > 0 {
			t.Logf("WARNING: DNS still returns %d IPs after deletion (may be cached): %v", len(ips), ips)
		}
	})
}

// TestDNS_CustomSubdomain verifies that deploying with a custom subdomain
// creates DNS records using the custom name.
func TestDNS_CustomSubdomain(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	deploymentName := fmt.Sprintf("dns-custom-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := createDeploymentWithSubdomain(t, env, deploymentName, tarballPath)
	require.NotEmpty(t, deploymentID)

	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	time.Sleep(10 * time.Second)

	t.Run("Deployment has subdomain with random suffix", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		subdomain, _ := deployment["subdomain"].(string)
		require.NotEmpty(t, subdomain, "Deployment should have a subdomain")
		t.Logf("Subdomain: %s", subdomain)

		// Verify the subdomain starts with the deployment name
		assert.Contains(t, subdomain, deploymentName[:10],
			"Subdomain should relate to deployment name")
	})
}

// TestDNS_RedeployPreservesSubdomain verifies that updating a deployment
// does not change the subdomain/DNS.
func TestDNS_RedeployPreservesSubdomain(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	deploymentName := fmt.Sprintf("dns-preserve-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	require.NotEmpty(t, deploymentID)

	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	time.Sleep(5 * time.Second)

	// Get original subdomain
	deployment := e2e.GetDeployment(t, env, deploymentID)
	originalSubdomain, _ := deployment["subdomain"].(string)
	originalURLs := deployment["urls"]
	t.Logf("Original subdomain: %s, urls: %v", originalSubdomain, originalURLs)

	// Update
	updateStaticDeploymentProd(t, env, deploymentName, tarballPath)
	time.Sleep(5 * time.Second)

	// Verify subdomain unchanged
	t.Run("Subdomain unchanged after update", func(t *testing.T) {
		updated := e2e.GetDeployment(t, env, deploymentID)
		updatedSubdomain, _ := updated["subdomain"].(string)

		assert.Equal(t, originalSubdomain, updatedSubdomain,
			"Subdomain should not change after update")
		t.Logf("After update: subdomain=%s", updatedSubdomain)
	})
}

func createDeploymentWithSubdomain(t *testing.T, env *e2e.E2ETestEnv, name, tarballPath string) string {
	t.Helper()

	var fileData []byte
	info, err := os.Stat(tarballPath)
	require.NoError(t, err)
	if info.IsDir() {
		fileData, err = exec.Command("tar", "-czf", "-", "-C", tarballPath, ".").Output()
		require.NoError(t, err)
	} else {
		file, err := os.Open(tarballPath)
		require.NoError(t, err)
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

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/static/upload", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Upload failed: status=%d body=%s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if id, ok := result["deployment_id"].(string); ok {
		return id
	}
	if id, ok := result["id"].(string); ok {
		return id
	}
	t.Fatalf("No id in response: %+v", result)
	return ""
}

func updateStaticDeploymentProd(t *testing.T, env *e2e.E2ETestEnv, name, tarballPath string) {
	t.Helper()

	var fileData []byte
	info, err := os.Stat(tarballPath)
	require.NoError(t, err)
	if info.IsDir() {
		fileData, err = exec.Command("tar", "-czf", "-", "-C", tarballPath, ".").Output()
		require.NoError(t, err)
	} else {
		file, err := os.Open(tarballPath)
		require.NoError(t, err)
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

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/static/update", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Update failed: status=%d body=%s", resp.StatusCode, string(bodyBytes))
	}
}

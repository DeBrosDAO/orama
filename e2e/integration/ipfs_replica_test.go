//go:build e2e

package integration

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

// TestIPFS_ContentPinnedOnMultipleNodes verifies that deploying a static app
// makes the IPFS content available across multiple nodes.
func TestIPFS_ContentPinnedOnMultipleNodes(t *testing.T) {
	e2e.SkipIfLocal(t)

	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	if len(env.Config.Servers) < 2 {
		t.Skip("Requires at least 2 servers")
	}

	deploymentName := fmt.Sprintf("ipfs-pin-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	require.NotEmpty(t, deploymentID)

	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	time.Sleep(15 * time.Second) // Wait for IPFS content replication

	deployment := e2e.GetDeployment(t, env, deploymentID)
	contentCID, _ := deployment["content_cid"].(string)
	require.NotEmpty(t, contentCID, "Deployment should have a content CID")

	t.Run("Content served from each node via gateway", func(t *testing.T) {
		// Extract domain from deployment URLs
		urls, _ := deployment["urls"].([]interface{})
		require.NotEmpty(t, urls, "Deployment should have URLs")
		urlStr, _ := urls[0].(string)
		domain := urlStr
		if len(urlStr) > 8 && urlStr[:8] == "https://" {
			domain = urlStr[8:]
		} else if len(urlStr) > 7 && urlStr[:7] == "http://" {
			domain = urlStr[7:]
		}

		client := e2e.NewHTTPClient(30 * time.Second)

		for _, server := range env.Config.Servers {
			t.Run("node_"+server.Name, func(t *testing.T) {
				gatewayURL := fmt.Sprintf("http://%s:6001/", server.IP)
				req, _ := http.NewRequest("GET", gatewayURL, nil)
				req.Host = domain

				resp, err := client.Do(req)
				require.NoError(t, err, "Request to %s should not error", server.Name)
				defer resp.Body.Close()

				body, _ := io.ReadAll(resp.Body)
				t.Logf("%s: status=%d, body=%d bytes", server.Name, resp.StatusCode, len(body))
				assert.Equal(t, http.StatusOK, resp.StatusCode,
					"IPFS content should be served on %s (CID: %s)", server.Name, contentCID)
			})
		}
	})
}

// TestIPFS_LargeFileDeployment verifies that deploying an app with larger
// static assets works correctly.
func TestIPFS_LargeFileDeployment(t *testing.T) {
	env, err := e2e.LoadTestEnv()
	require.NoError(t, err)

	deploymentName := fmt.Sprintf("ipfs-large-%d", time.Now().Unix())
	tarballPath := filepath.Join("../../testdata/apps/react-app")

	// The react-vite tarball is our largest test asset
	deploymentID := e2e.CreateTestDeployment(t, env, deploymentName, tarballPath)
	require.NotEmpty(t, deploymentID)

	defer func() {
		if !env.SkipCleanup {
			e2e.DeleteDeployment(t, env, deploymentID)
		}
	}()

	time.Sleep(5 * time.Second)

	t.Run("Deployment has valid CID", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		contentCID, _ := deployment["content_cid"].(string)
		assert.NotEmpty(t, contentCID, "Should have a content CID")
		assert.True(t, len(contentCID) > 10, "CID should be a valid IPFS hash")
		t.Logf("Content CID: %s", contentCID)
	})

	t.Run("Static content serves correctly", func(t *testing.T) {
		deployment := e2e.GetDeployment(t, env, deploymentID)
		urls, ok := deployment["urls"].([]interface{})
		if !ok || len(urls) == 0 {
			t.Skip("No URLs in deployment")
		}

		nodeURL, _ := urls[0].(string)
		domain := nodeURL
		if len(nodeURL) > 8 && nodeURL[:8] == "https://" {
			domain = nodeURL[8:]
		} else if len(nodeURL) > 7 && nodeURL[:7] == "http://" {
			domain = nodeURL[7:]
		}
		if len(domain) > 0 && domain[len(domain)-1] == '/' {
			domain = domain[:len(domain)-1]
		}

		resp := e2e.TestDeploymentWithHostHeader(t, env, domain, "/")
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Greater(t, len(body), 100, "Response should have substantial content")
	})
}

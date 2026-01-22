//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNamespaceIsolation_Deployments(t *testing.T) {
	// Setup two test environments with different namespaces
	envA, err := LoadTestEnvWithNamespace("namespace-a-" + fmt.Sprintf("%d", time.Now().Unix()))
	require.NoError(t, err, "Failed to create namespace A environment")

	envB, err := LoadTestEnvWithNamespace("namespace-b-" + fmt.Sprintf("%d", time.Now().Unix()))
	require.NoError(t, err, "Failed to create namespace B environment")

	tarballPath := filepath.Join("../testdata/tarballs/react-vite.tar.gz")

	// Create deployment in namespace-a
	deploymentNameA := "test-app-ns-a"
	deploymentIDA := CreateTestDeployment(t, envA, deploymentNameA, tarballPath)
	defer func() {
		if !envA.SkipCleanup {
			DeleteDeployment(t, envA, deploymentIDA)
		}
	}()

	// Create deployment in namespace-b
	deploymentNameB := "test-app-ns-b"
	deploymentIDB := CreateTestDeployment(t, envB, deploymentNameB, tarballPath)
	defer func() {
		if !envB.SkipCleanup {
			DeleteDeployment(t, envB, deploymentIDB)
		}
	}()

	t.Run("Namespace-A cannot list Namespace-B deployments", func(t *testing.T) {
		req, _ := http.NewRequest("GET", envA.GatewayURL+"/v1/deployments/list", nil)
		req.Header.Set("Authorization", "Bearer "+envA.APIKey)

		resp, err := envA.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		var result map[string]interface{}
		bodyBytes, _ := io.ReadAll(resp.Body)
		require.NoError(t, json.Unmarshal(bodyBytes, &result), "Should decode JSON")

		deployments, ok := result["deployments"].([]interface{})
		require.True(t, ok, "Deployments should be an array")

		// Should only see namespace-a deployments
		for _, d := range deployments {
			dep, ok := d.(map[string]interface{})
			if !ok {
				continue
			}
			assert.NotEqual(t, deploymentNameB, dep["name"], "Should not see namespace-b deployment")
		}

		t.Logf("✓ Namespace A cannot see Namespace B deployments")
	})

	t.Run("Namespace-A cannot access Namespace-B deployment by ID", func(t *testing.T) {
		req, _ := http.NewRequest("GET", envA.GatewayURL+"/v1/deployments/get?id="+deploymentIDB, nil)
		req.Header.Set("Authorization", "Bearer "+envA.APIKey)

		resp, err := envA.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		// Should return 404 or 403
		assert.Contains(t, []int{http.StatusNotFound, http.StatusForbidden}, resp.StatusCode,
			"Should block cross-namespace access")

		t.Logf("✓ Namespace A cannot access Namespace B deployment (status: %d)", resp.StatusCode)
	})

	t.Run("Namespace-A cannot delete Namespace-B deployment", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", envA.GatewayURL+"/v1/deployments/delete?id="+deploymentIDB, nil)
		req.Header.Set("Authorization", "Bearer "+envA.APIKey)

		resp, err := envA.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		assert.Contains(t, []int{http.StatusNotFound, http.StatusForbidden}, resp.StatusCode,
			"Should block cross-namespace deletion")

		// Verify deployment still exists for namespace-b
		req2, _ := http.NewRequest("GET", envB.GatewayURL+"/v1/deployments/get?id="+deploymentIDB, nil)
		req2.Header.Set("Authorization", "Bearer "+envB.APIKey)

		resp2, err := envB.HTTPClient.Do(req2)
		require.NoError(t, err, "Should execute request")
		defer resp2.Body.Close()

		assert.Equal(t, http.StatusOK, resp2.StatusCode, "Deployment should still exist in namespace B")

		t.Logf("✓ Namespace A cannot delete Namespace B deployment")
	})
}

func TestNamespaceIsolation_SQLiteDatabases(t *testing.T) {
	envA, _ := LoadTestEnvWithNamespace("namespace-a-" + fmt.Sprintf("%d", time.Now().Unix()))
	envB, _ := LoadTestEnvWithNamespace("namespace-b-" + fmt.Sprintf("%d", time.Now().Unix()))

	// Create database in namespace-a
	dbNameA := "users-db-a"
	CreateSQLiteDB(t, envA, dbNameA)
	defer func() {
		if !envA.SkipCleanup {
			DeleteSQLiteDB(t, envA, dbNameA)
		}
	}()

	// Create database in namespace-b
	dbNameB := "users-db-b"
	CreateSQLiteDB(t, envB, dbNameB)
	defer func() {
		if !envB.SkipCleanup {
			DeleteSQLiteDB(t, envB, dbNameB)
		}
	}()

	t.Run("Namespace-A cannot list Namespace-B databases", func(t *testing.T) {
		req, _ := http.NewRequest("GET", envA.GatewayURL+"/v1/db/sqlite/list", nil)
		req.Header.Set("Authorization", "Bearer "+envA.APIKey)

		resp, err := envA.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		var result map[string]interface{}
		bodyBytes, _ := io.ReadAll(resp.Body)
		require.NoError(t, json.Unmarshal(bodyBytes, &result), "Should decode JSON")

		databases, ok := result["databases"].([]interface{})
		require.True(t, ok, "Databases should be an array")

		for _, db := range databases {
			database, ok := db.(map[string]interface{})
			if !ok {
				continue
			}
			assert.NotEqual(t, dbNameB, database["database_name"], "Should not see namespace-b database")
		}

		t.Logf("✓ Namespace A cannot see Namespace B databases")
	})

	t.Run("Namespace-A cannot query Namespace-B database", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"database_name": dbNameB,
			"query":         "SELECT * FROM users",
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", envA.GatewayURL+"/v1/db/sqlite/query", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+envA.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := envA.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "Should block cross-namespace query")

		t.Logf("✓ Namespace A cannot query Namespace B database")
	})

	t.Run("Namespace-A cannot backup Namespace-B database", func(t *testing.T) {
		reqBody := map[string]string{"database_name": dbNameB}
		bodyBytes, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", envA.GatewayURL+"/v1/db/sqlite/backup", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+envA.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := envA.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "Should block cross-namespace backup")

		t.Logf("✓ Namespace A cannot backup Namespace B database")
	})
}

func TestNamespaceIsolation_IPFSContent(t *testing.T) {
	envA, _ := LoadTestEnvWithNamespace("namespace-a-" + fmt.Sprintf("%d", time.Now().Unix()))
	envB, _ := LoadTestEnvWithNamespace("namespace-b-" + fmt.Sprintf("%d", time.Now().Unix()))

	// Upload file in namespace-a
	cidA := UploadTestFile(t, envA, "test-file-a.txt", "Content from namespace A")
	defer func() {
		if !envA.SkipCleanup {
			UnpinFile(t, envA, cidA)
		}
	}()

	t.Run("Namespace-B cannot GET Namespace-A IPFS content", func(t *testing.T) {
		// This tests application-level access control
		// IPFS content is globally accessible by CID, but our handlers should enforce namespace
		req, _ := http.NewRequest("GET", envB.GatewayURL+"/v1/storage/get?cid="+cidA, nil)
		req.Header.Set("Authorization", "Bearer "+envB.APIKey)

		resp, err := envB.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		// Should return 403 or 404 (namespace doesn't own this CID)
		assert.Contains(t, []int{http.StatusNotFound, http.StatusForbidden}, resp.StatusCode,
			"Should block cross-namespace IPFS GET")

		t.Logf("✓ Namespace B cannot GET Namespace A IPFS content (status: %d)", resp.StatusCode)
	})

	t.Run("Namespace-B cannot PIN Namespace-A IPFS content", func(t *testing.T) {
		reqBody := map[string]string{
			"cid":  cidA,
			"name": "stolen-content",
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", envB.GatewayURL+"/v1/storage/pin", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+envB.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := envB.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		assert.Contains(t, []int{http.StatusNotFound, http.StatusForbidden}, resp.StatusCode,
			"Should block cross-namespace PIN")

		t.Logf("✓ Namespace B cannot PIN Namespace A IPFS content (status: %d)", resp.StatusCode)
	})

	t.Run("Namespace-B cannot UNPIN Namespace-A IPFS content", func(t *testing.T) {
		reqBody := map[string]string{"cid": cidA}
		bodyBytes, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", envB.GatewayURL+"/v1/storage/unpin", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+envB.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := envB.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		assert.Contains(t, []int{http.StatusNotFound, http.StatusForbidden}, resp.StatusCode,
			"Should block cross-namespace UNPIN")

		t.Logf("✓ Namespace B cannot UNPIN Namespace A IPFS content (status: %d)", resp.StatusCode)
	})

	t.Run("Namespace-A can list only their own IPFS pins", func(t *testing.T) {
		req, _ := http.NewRequest("GET", envA.GatewayURL+"/v1/storage/pins", nil)
		req.Header.Set("Authorization", "Bearer "+envA.APIKey)

		resp, err := envA.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Should list pins successfully")

		var pins []map[string]interface{}
		bodyBytes, _ := io.ReadAll(resp.Body)
		require.NoError(t, json.Unmarshal(bodyBytes, &pins), "Should decode pins")

		// Should see their own pin
		foundOwn := false
		for _, pin := range pins {
			if cid, ok := pin["cid"].(string); ok && cid == cidA {
				foundOwn = true
				break
			}
		}
		assert.True(t, foundOwn, "Should see own pins")

		t.Logf("✓ Namespace A can list only their own pins")
	})
}

func TestNamespaceIsolation_OlricCache(t *testing.T) {
	envA, _ := LoadTestEnvWithNamespace("namespace-a-" + fmt.Sprintf("%d", time.Now().Unix()))
	envB, _ := LoadTestEnvWithNamespace("namespace-b-" + fmt.Sprintf("%d", time.Now().Unix()))

	keyA := "user-session-123"
	valueA := `{"user_id": "alice", "token": "secret-token-a"}`

	t.Run("Namespace-A sets cache key", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"key":   keyA,
			"value": valueA,
			"ttl":   300,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", envA.GatewayURL+"/v1/cache/set", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+envA.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := envA.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Should set cache key successfully")

		t.Logf("✓ Namespace A set cache key")
	})

	t.Run("Namespace-B cannot GET Namespace-A cache key", func(t *testing.T) {
		req, _ := http.NewRequest("GET", envB.GatewayURL+"/v1/cache/get?key="+keyA, nil)
		req.Header.Set("Authorization", "Bearer "+envB.APIKey)

		resp, err := envB.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		// Should return 404 (key doesn't exist in namespace-b)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "Should not find key in different namespace")

		t.Logf("✓ Namespace B cannot GET Namespace A cache key")
	})

	t.Run("Namespace-B cannot DELETE Namespace-A cache key", func(t *testing.T) {
		reqBody := map[string]string{"key": keyA}
		bodyBytes, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", envB.GatewayURL+"/v1/cache/delete", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+envB.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := envB.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		// Should return 404 or success (key doesn't exist in their namespace)
		assert.Contains(t, []int{http.StatusOK, http.StatusNotFound}, resp.StatusCode)

		// Verify key still exists for namespace-a
		req2, _ := http.NewRequest("GET", envA.GatewayURL+"/v1/cache/get?key="+keyA, nil)
		req2.Header.Set("Authorization", "Bearer "+envA.APIKey)

		resp2, err := envA.HTTPClient.Do(req2)
		require.NoError(t, err, "Should execute request")
		defer resp2.Body.Close()

		assert.Equal(t, http.StatusOK, resp2.StatusCode, "Key should still exist in namespace A")

		var result map[string]interface{}
		bodyBytes2, _ := io.ReadAll(resp2.Body)
		require.NoError(t, json.Unmarshal(bodyBytes2, &result), "Should decode result")

		assert.Equal(t, valueA, result["value"], "Value should match")

		t.Logf("✓ Namespace B cannot DELETE Namespace A cache key")
	})

	t.Run("Namespace-B can set same key name in their namespace", func(t *testing.T) {
		// Same key name, different namespace should be allowed
		valueB := `{"user_id": "bob", "token": "secret-token-b"}`

		reqBody := map[string]interface{}{
			"key":   keyA, // Same key name as namespace-a
			"value": valueB,
			"ttl":   300,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", envB.GatewayURL+"/v1/cache/set", bytes.NewReader(bodyBytes))
		req.Header.Set("Authorization", "Bearer "+envB.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := envB.HTTPClient.Do(req)
		require.NoError(t, err, "Should execute request")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Should set key in namespace B")

		// Verify namespace-a still has their value
		req2, _ := http.NewRequest("GET", envA.GatewayURL+"/v1/cache/get?key="+keyA, nil)
		req2.Header.Set("Authorization", "Bearer "+envA.APIKey)

		resp2, _ := envA.HTTPClient.Do(req2)
		defer resp2.Body.Close()

		var resultA map[string]interface{}
		bodyBytesA, _ := io.ReadAll(resp2.Body)
		require.NoError(t, json.Unmarshal(bodyBytesA, &resultA), "Should decode result A")

		assert.Equal(t, valueA, resultA["value"], "Namespace A value should be unchanged")

		// Verify namespace-b has their different value
		req3, _ := http.NewRequest("GET", envB.GatewayURL+"/v1/cache/get?key="+keyA, nil)
		req3.Header.Set("Authorization", "Bearer "+envB.APIKey)

		resp3, _ := envB.HTTPClient.Do(req3)
		defer resp3.Body.Close()

		var resultB map[string]interface{}
		bodyBytesB, _ := io.ReadAll(resp3.Body)
		require.NoError(t, json.Unmarshal(bodyBytesB, &resultB), "Should decode result B")

		assert.Equal(t, valueB, resultB["value"], "Namespace B value should be different")

		t.Logf("✓ Namespace B can set same key name independently")
		t.Logf("   - Namespace A value: %s", valueA)
		t.Logf("   - Namespace B value: %s", valueB)
	})
}

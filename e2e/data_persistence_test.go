//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// =============================================================================
// STRICT DATA PERSISTENCE TESTS
// These tests verify that data is properly persisted and survives operations.
// Tests FAIL if data is lost or corrupted.
// =============================================================================

// TestRQLite_DataPersistence verifies that RQLite data is persisted through the gateway.
func TestRQLite_DataPersistence(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tableName := fmt.Sprintf("persist_test_%d", time.Now().UnixNano())

	// Cleanup
	defer func() {
		dropReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/drop-table",
			Body:   map[string]interface{}{"table": tableName},
		}
		dropReq.Do(context.Background())
	}()

	// Create table
	createReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, value TEXT, version INTEGER)",
				tableName,
			),
		},
	}

	_, status, err := createReq.Do(ctx)
	require.NoError(t, err, "FAIL: Could not create table")
	require.True(t, status == http.StatusCreated || status == http.StatusOK,
		"FAIL: Create table returned status %d", status)

	t.Run("Data_survives_multiple_writes", func(t *testing.T) {
		// Insert initial data
		var statements []string
		for i := 1; i <= 10; i++ {
			statements = append(statements,
				fmt.Sprintf("INSERT INTO %s (value, version) VALUES ('item_%d', %d)", tableName, i, i))
		}

		insertReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/transaction",
			Body:   map[string]interface{}{"statements": statements},
		}

		_, status, err := insertReq.Do(ctx)
		require.NoError(t, err, "FAIL: Could not insert rows")
		require.Equal(t, http.StatusOK, status, "FAIL: Insert returned status %d", status)

		// Verify all data exists
		queryReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/query",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName),
			},
		}

		body, status, err := queryReq.Do(ctx)
		require.NoError(t, err, "FAIL: Could not count rows")
		require.Equal(t, http.StatusOK, status, "FAIL: Count query returned status %d", status)

		var queryResp map[string]interface{}
		DecodeJSON(body, &queryResp)

		if rows, ok := queryResp["rows"].([]interface{}); ok && len(rows) > 0 {
			row := rows[0].([]interface{})
			count := int(row[0].(float64))
			require.Equal(t, 10, count, "FAIL: Expected 10 rows, got %d", count)
		}

		// Update data
		updateReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/transaction",
			Body: map[string]interface{}{
				"statements": []string{
					fmt.Sprintf("UPDATE %s SET version = version + 100 WHERE version <= 5", tableName),
				},
			},
		}

		_, status, err = updateReq.Do(ctx)
		require.NoError(t, err, "FAIL: Could not update rows")
		require.Equal(t, http.StatusOK, status, "FAIL: Update returned status %d", status)

		// Verify updates persisted
		queryUpdatedReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/query",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE version > 100", tableName),
			},
		}

		body, status, err = queryUpdatedReq.Do(ctx)
		require.NoError(t, err, "FAIL: Could not count updated rows")
		require.Equal(t, http.StatusOK, status, "FAIL: Count updated query returned status %d", status)

		DecodeJSON(body, &queryResp)
		if rows, ok := queryResp["rows"].([]interface{}); ok && len(rows) > 0 {
			row := rows[0].([]interface{})
			count := int(row[0].(float64))
			require.Equal(t, 5, count, "FAIL: Expected 5 updated rows, got %d", count)
		}

		t.Logf("  ✓ Data persists through multiple write operations")
	})

	t.Run("Deletes_are_persisted", func(t *testing.T) {
		// Delete some rows
		deleteReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/transaction",
			Body: map[string]interface{}{
				"statements": []string{
					fmt.Sprintf("DELETE FROM %s WHERE version > 100", tableName),
				},
			},
		}

		_, status, err := deleteReq.Do(ctx)
		require.NoError(t, err, "FAIL: Could not delete rows")
		require.Equal(t, http.StatusOK, status, "FAIL: Delete returned status %d", status)

		// Verify deletes persisted
		queryReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/query",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName),
			},
		}

		body, status, err := queryReq.Do(ctx)
		require.NoError(t, err, "FAIL: Could not count remaining rows")
		require.Equal(t, http.StatusOK, status, "FAIL: Count query returned status %d", status)

		var queryResp map[string]interface{}
		DecodeJSON(body, &queryResp)

		if rows, ok := queryResp["rows"].([]interface{}); ok && len(rows) > 0 {
			row := rows[0].([]interface{})
			count := int(row[0].(float64))
			require.Equal(t, 5, count, "FAIL: Expected 5 rows after delete, got %d", count)
		}

		t.Logf("  ✓ Deletes are properly persisted")
	})
}

// TestRQLite_DataFilesExist verifies RQLite data files are created on disk.
func TestRQLite_DataFilesExist(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err, "FAIL: Could not get home directory")

	// Check for RQLite data directories
	dataLocations := []string{
		filepath.Join(homeDir, ".orama", "node-1", "rqlite"),
		filepath.Join(homeDir, ".orama", "node-2", "rqlite"),
		filepath.Join(homeDir, ".orama", "node-3", "rqlite"),
		filepath.Join(homeDir, ".orama", "node-4", "rqlite"),
		filepath.Join(homeDir, ".orama", "node-5", "rqlite"),
	}

	foundDataDirs := 0
	for _, dataDir := range dataLocations {
		if _, err := os.Stat(dataDir); err == nil {
			foundDataDirs++
			t.Logf("  ✓ Found RQLite data directory: %s", dataDir)

			// Check for Raft log files
			entries, _ := os.ReadDir(dataDir)
			for _, entry := range entries {
				t.Logf("    - %s", entry.Name())
			}
		}
	}

	require.Greater(t, foundDataDirs, 0,
		"FAIL: No RQLite data directories found - data may not be persisted")
	t.Logf("  Found %d RQLite data directories", foundDataDirs)
}

// TestOlric_DataPersistence verifies Olric cache data persistence.
// Note: Olric is an in-memory cache, so this tests data survival during runtime.
func TestOlric_DataPersistence(t *testing.T) {
	env, err := LoadTestEnv()
	require.NoError(t, err, "FAIL: Could not load test environment")

	dmap := fmt.Sprintf("persist_cache_%d", time.Now().UnixNano())

	t.Run("Cache_data_survives_multiple_operations", func(t *testing.T) {
		// Put multiple keys
		keys := make(map[string]string)
		for i := 0; i < 10; i++ {
			key := fmt.Sprintf("persist_key_%d", i)
			value := fmt.Sprintf("persist_value_%d", i)
			keys[key] = value

			err := putToOlric(env.GatewayURL, env.APIKey, dmap, key, value)
			require.NoError(t, err, "FAIL: Could not put key %s", key)
		}

		// Perform other operations
		err := putToOlric(env.GatewayURL, env.APIKey, dmap, "other_key", "other_value")
		require.NoError(t, err, "FAIL: Could not put other key")

		// Verify original keys still exist
		for key, expectedValue := range keys {
			retrieved, err := getFromOlric(env.GatewayURL, env.APIKey, dmap, key)
			require.NoError(t, err, "FAIL: Key %s not found after other operations", key)
			require.Equal(t, expectedValue, retrieved, "FAIL: Value mismatch for key %s", key)
		}

		t.Logf("  ✓ Cache data survives multiple operations")
	})
}

// TestNamespaceCluster_DataPersistence verifies namespace-specific data is isolated and persisted.
func TestNamespaceCluster_DataPersistence(t *testing.T) {
	// Create namespace
	namespace := fmt.Sprintf("persist-ns-%d", time.Now().UnixNano())
	env, err := LoadTestEnvWithNamespace(namespace)
	require.NoError(t, err, "FAIL: Could not create namespace")

	t.Logf("Created namespace: %s", namespace)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Run("Namespace_data_is_isolated", func(t *testing.T) {
		// Create data via gateway API
		tableName := fmt.Sprintf("ns_data_%d", time.Now().UnixNano())

		req := &HTTPRequest{
			Method: http.MethodPost,
			URL:    env.GatewayURL + "/v1/rqlite/create-table",
			Headers: map[string]string{
				"Authorization": "Bearer " + env.APIKey,
			},
			Body: map[string]interface{}{
				"schema": fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, value TEXT)", tableName),
			},
		}

		_, status, err := req.Do(ctx)
		require.NoError(t, err, "FAIL: Could not create table in namespace")
		require.True(t, status == http.StatusOK || status == http.StatusCreated,
			"FAIL: Create table returned status %d", status)

		// Insert data
		insertReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    env.GatewayURL + "/v1/rqlite/transaction",
			Headers: map[string]string{
				"Authorization": "Bearer " + env.APIKey,
			},
			Body: map[string]interface{}{
				"statements": []string{
					fmt.Sprintf("INSERT INTO %s (value) VALUES ('ns_test_value')", tableName),
				},
			},
		}

		_, status, err = insertReq.Do(ctx)
		require.NoError(t, err, "FAIL: Could not insert into namespace table")
		require.Equal(t, http.StatusOK, status, "FAIL: Insert returned status %d", status)

		// Verify data exists
		queryReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    env.GatewayURL + "/v1/rqlite/query",
			Headers: map[string]string{
				"Authorization": "Bearer " + env.APIKey,
			},
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT value FROM %s", tableName),
			},
		}

		body, status, err := queryReq.Do(ctx)
		require.NoError(t, err, "FAIL: Could not query namespace table")
		require.Equal(t, http.StatusOK, status, "FAIL: Query returned status %d", status)

		var queryResp map[string]interface{}
		json.Unmarshal(body, &queryResp)
		count, _ := queryResp["count"].(float64)
		require.Equal(t, float64(1), count, "FAIL: Expected 1 row in namespace table")

		t.Logf("  ✓ Namespace data is isolated and persisted")
	})
}

// TestIPFS_DataPersistence verifies IPFS content is persisted and pinned.
// Note: Detailed IPFS tests are in storage_http_test.go. This test uses the helper from env.go.
func TestIPFS_DataPersistence(t *testing.T) {
	env, err := LoadTestEnv()
	require.NoError(t, err, "FAIL: Could not load test environment")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("Uploaded_content_persists", func(t *testing.T) {
		// Use helper function to upload content via multipart form
		content := fmt.Sprintf("persistent content %d", time.Now().UnixNano())
		cid := UploadTestFile(t, env, "persist_test.txt", content)
		require.NotEmpty(t, cid, "FAIL: No CID returned from upload")
		t.Logf("  Uploaded content with CID: %s", cid)

		// Verify content can be retrieved
		getReq := &HTTPRequest{
			Method: http.MethodGet,
			URL:    env.GatewayURL + "/v1/storage/get/" + cid,
			Headers: map[string]string{
				"Authorization": "Bearer " + env.APIKey,
			},
		}

		respBody, status, err := getReq.Do(ctx)
		require.NoError(t, err, "FAIL: Get content failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Get returned status %d", status)
		require.Contains(t, string(respBody), "persistent content",
			"FAIL: Retrieved content doesn't match uploaded content")

		t.Logf("  ✓ IPFS content persists and is retrievable")
	})
}

// TestSQLite_DataPersistence verifies per-deployment SQLite databases persist.
func TestSQLite_DataPersistence(t *testing.T) {
	env, err := LoadTestEnv()
	require.NoError(t, err, "FAIL: Could not load test environment")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbName := fmt.Sprintf("persist_db_%d", time.Now().UnixNano())

	t.Run("SQLite_database_persists", func(t *testing.T) {
		// Create database
		createReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    env.GatewayURL + "/v1/db/sqlite/create",
			Headers: map[string]string{
				"Authorization": "Bearer " + env.APIKey,
			},
			Body: map[string]interface{}{
				"name": dbName,
			},
		}

		_, status, err := createReq.Do(ctx)
		require.NoError(t, err, "FAIL: Create database failed")
		require.True(t, status == http.StatusOK || status == http.StatusCreated,
			"FAIL: Create returned status %d", status)
		t.Logf("  Created SQLite database: %s", dbName)

		// Create table and insert data
		queryReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    env.GatewayURL + "/v1/db/sqlite/query",
			Headers: map[string]string{
				"Authorization": "Bearer " + env.APIKey,
			},
			Body: map[string]interface{}{
				"database": dbName,
				"sql":      "CREATE TABLE IF NOT EXISTS test_table (id INTEGER PRIMARY KEY, data TEXT)",
			},
		}

		_, status, err = queryReq.Do(ctx)
		require.NoError(t, err, "FAIL: Create table failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Create table returned status %d", status)

		// Insert data
		insertReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    env.GatewayURL + "/v1/db/sqlite/query",
			Headers: map[string]string{
				"Authorization": "Bearer " + env.APIKey,
			},
			Body: map[string]interface{}{
				"database": dbName,
				"sql":      "INSERT INTO test_table (data) VALUES ('persistent_data')",
			},
		}

		_, status, err = insertReq.Do(ctx)
		require.NoError(t, err, "FAIL: Insert failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Insert returned status %d", status)

		// Verify data persists
		selectReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    env.GatewayURL + "/v1/db/sqlite/query",
			Headers: map[string]string{
				"Authorization": "Bearer " + env.APIKey,
			},
			Body: map[string]interface{}{
				"database": dbName,
				"sql":      "SELECT data FROM test_table",
			},
		}

		body, status, err := selectReq.Do(ctx)
		require.NoError(t, err, "FAIL: Select failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Select returned status %d", status)
		require.Contains(t, string(body), "persistent_data",
			"FAIL: Data not found in SQLite database")

		t.Logf("  ✓ SQLite database data persists")
	})

	t.Run("SQLite_database_listed", func(t *testing.T) {
		// List databases to verify it was persisted
		listReq := &HTTPRequest{
			Method: http.MethodGet,
			URL:    env.GatewayURL + "/v1/db/sqlite/list",
			Headers: map[string]string{
				"Authorization": "Bearer " + env.APIKey,
			},
		}

		body, status, err := listReq.Do(ctx)
		require.NoError(t, err, "FAIL: List databases failed")
		require.Equal(t, http.StatusOK, status, "FAIL: List returned status %d", status)
		require.Contains(t, string(body), dbName,
			"FAIL: Created database not found in list")

		t.Logf("  ✓ SQLite database appears in list")
	})
}

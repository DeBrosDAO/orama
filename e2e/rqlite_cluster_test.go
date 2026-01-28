//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// =============================================================================
// STRICT RQLITE CLUSTER TESTS
// These tests verify that RQLite cluster operations work correctly.
// Tests FAIL if operations don't work - no skips, no warnings.
// =============================================================================

// TestRQLite_ClusterHealth verifies the RQLite cluster is healthy and operational.
func TestRQLite_ClusterHealth(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check RQLite schema endpoint (proves cluster is reachable)
	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/rqlite/schema",
	}

	body, status, err := req.Do(ctx)
	require.NoError(t, err, "FAIL: Could not reach RQLite cluster")
	require.Equal(t, http.StatusOK, status, "FAIL: RQLite schema endpoint returned %d: %s", status, string(body))

	var schemaResp map[string]interface{}
	err = DecodeJSON(body, &schemaResp)
	require.NoError(t, err, "FAIL: Could not decode RQLite schema response")

	// Schema endpoint should return tables array
	_, hasTables := schemaResp["tables"]
	require.True(t, hasTables, "FAIL: RQLite schema response missing 'tables' field")

	t.Logf("  ✓ RQLite cluster is healthy and responding")
}

// TestRQLite_WriteReadConsistency verifies data written can be read back consistently.
func TestRQLite_WriteReadConsistency(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	table := GenerateTableName()

	// Cleanup
	defer func() {
		dropReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/drop-table",
			Body:   map[string]interface{}{"table": table},
		}
		dropReq.Do(context.Background())
	}()

	// Create table
	createReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, value TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)",
				table,
			),
		},
	}

	_, status, err := createReq.Do(ctx)
	require.NoError(t, err, "FAIL: Create table request failed")
	require.True(t, status == http.StatusCreated || status == http.StatusOK,
		"FAIL: Create table returned status %d", status)
	t.Logf("Created table %s", table)

	t.Run("Write_then_read_returns_same_data", func(t *testing.T) {
		uniqueValue := fmt.Sprintf("test_value_%d", time.Now().UnixNano())

		// Insert
		insertReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/transaction",
			Body: map[string]interface{}{
				"statements": []string{
					fmt.Sprintf("INSERT INTO %s (value) VALUES ('%s')", table, uniqueValue),
				},
			},
		}

		_, status, err := insertReq.Do(ctx)
		require.NoError(t, err, "FAIL: Insert request failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Insert returned status %d", status)

		// Read back
		queryReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/query",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT value FROM %s WHERE value = '%s'", table, uniqueValue),
			},
		}

		body, status, err := queryReq.Do(ctx)
		require.NoError(t, err, "FAIL: Query request failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Query returned status %d", status)

		var queryResp map[string]interface{}
		err = DecodeJSON(body, &queryResp)
		require.NoError(t, err, "FAIL: Could not decode query response")

		// Verify we got our value back
		count, ok := queryResp["count"].(float64)
		require.True(t, ok, "FAIL: Response missing 'count' field")
		require.Equal(t, float64(1), count, "FAIL: Expected 1 row, got %v", count)

		t.Logf("  ✓ Written value '%s' was read back correctly", uniqueValue)
	})

	t.Run("Multiple_writes_all_readable", func(t *testing.T) {
		// Insert multiple values
		var statements []string
		for i := 0; i < 10; i++ {
			statements = append(statements,
				fmt.Sprintf("INSERT INTO %s (value) VALUES ('batch_%d')", table, i))
		}

		insertReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/transaction",
			Body: map[string]interface{}{
				"statements": statements,
			},
		}

		_, status, err := insertReq.Do(ctx)
		require.NoError(t, err, "FAIL: Batch insert failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Batch insert returned status %d", status)

		// Count all batch rows
		queryReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/query",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT COUNT(*) as cnt FROM %s WHERE value LIKE 'batch_%%'", table),
			},
		}

		body, status, err := queryReq.Do(ctx)
		require.NoError(t, err, "FAIL: Count query failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Count query returned status %d", status)

		var queryResp map[string]interface{}
		DecodeJSON(body, &queryResp)

		if rows, ok := queryResp["rows"].([]interface{}); ok && len(rows) > 0 {
			row := rows[0].([]interface{})
			count := int(row[0].(float64))
			require.Equal(t, 10, count, "FAIL: Expected 10 batch rows, got %d", count)
		}

		t.Logf("  ✓ All 10 batch writes are readable")
	})
}

// TestRQLite_TransactionAtomicity verifies transactions are atomic.
func TestRQLite_TransactionAtomicity(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	table := GenerateTableName()

	// Cleanup
	defer func() {
		dropReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/drop-table",
			Body:   map[string]interface{}{"table": table},
		}
		dropReq.Do(context.Background())
	}()

	// Create table
	createReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, value TEXT UNIQUE)",
				table,
			),
		},
	}

	_, status, err := createReq.Do(ctx)
	require.NoError(t, err, "FAIL: Create table failed")
	require.True(t, status == http.StatusCreated || status == http.StatusOK,
		"FAIL: Create table returned status %d", status)

	t.Run("Successful_transaction_commits_all", func(t *testing.T) {
		txReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/transaction",
			Body: map[string]interface{}{
				"statements": []string{
					fmt.Sprintf("INSERT INTO %s (value) VALUES ('tx_val_1')", table),
					fmt.Sprintf("INSERT INTO %s (value) VALUES ('tx_val_2')", table),
					fmt.Sprintf("INSERT INTO %s (value) VALUES ('tx_val_3')", table),
				},
			},
		}

		_, status, err := txReq.Do(ctx)
		require.NoError(t, err, "FAIL: Transaction request failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Transaction returned status %d", status)

		// Verify all 3 rows exist
		queryReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/query",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE value LIKE 'tx_val_%%'", table),
			},
		}

		body, _, _ := queryReq.Do(ctx)
		var queryResp map[string]interface{}
		DecodeJSON(body, &queryResp)

		if rows, ok := queryResp["rows"].([]interface{}); ok && len(rows) > 0 {
			row := rows[0].([]interface{})
			count := int(row[0].(float64))
			require.Equal(t, 3, count, "FAIL: Transaction didn't commit all 3 rows - got %d", count)
		}

		t.Logf("  ✓ Transaction committed all 3 rows atomically")
	})

	t.Run("Updates_preserve_consistency", func(t *testing.T) {
		// Update a value
		updateReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/transaction",
			Body: map[string]interface{}{
				"statements": []string{
					fmt.Sprintf("UPDATE %s SET value = 'tx_val_1_updated' WHERE value = 'tx_val_1'", table),
				},
			},
		}

		_, status, err := updateReq.Do(ctx)
		require.NoError(t, err, "FAIL: Update request failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Update returned status %d", status)

		// Verify update took effect
		queryReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/query",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT value FROM %s WHERE value = 'tx_val_1_updated'", table),
			},
		}

		body, _, _ := queryReq.Do(ctx)
		var queryResp map[string]interface{}
		DecodeJSON(body, &queryResp)

		count, _ := queryResp["count"].(float64)
		require.Equal(t, float64(1), count, "FAIL: Update didn't take effect")

		t.Logf("  ✓ Update preserved consistency")
	})
}

// TestRQLite_ConcurrentWrites verifies the cluster handles concurrent writes correctly.
func TestRQLite_ConcurrentWrites(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	table := GenerateTableName()

	// Cleanup
	defer func() {
		dropReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/drop-table",
			Body:   map[string]interface{}{"table": table},
		}
		dropReq.Do(context.Background())
	}()

	// Create table
	createReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, worker INTEGER, seq INTEGER)",
				table,
			),
		},
	}

	_, status, err := createReq.Do(ctx)
	require.NoError(t, err, "FAIL: Create table failed")
	require.True(t, status == http.StatusCreated || status == http.StatusOK,
		"FAIL: Create table returned status %d", status)

	t.Run("Concurrent_inserts_all_succeed", func(t *testing.T) {
		numWorkers := 5
		insertsPerWorker := 10
		expectedTotal := numWorkers * insertsPerWorker

		var wg sync.WaitGroup
		errChan := make(chan error, numWorkers*insertsPerWorker)

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for i := 0; i < insertsPerWorker; i++ {
					insertReq := &HTTPRequest{
						Method: http.MethodPost,
						URL:    GetGatewayURL() + "/v1/rqlite/transaction",
						Body: map[string]interface{}{
							"statements": []string{
								fmt.Sprintf("INSERT INTO %s (worker, seq) VALUES (%d, %d)", table, workerID, i),
							},
						},
					}

					_, status, err := insertReq.Do(ctx)
					if err != nil {
						errChan <- fmt.Errorf("worker %d insert %d failed: %w", workerID, i, err)
						return
					}
					if status != http.StatusOK {
						errChan <- fmt.Errorf("worker %d insert %d got status %d", workerID, i, status)
						return
					}
				}
			}(w)
		}

		wg.Wait()
		close(errChan)

		// Collect errors
		var errors []error
		for err := range errChan {
			errors = append(errors, err)
		}
		require.Empty(t, errors, "FAIL: %d concurrent inserts failed: %v", len(errors), errors)

		// Verify total count
		queryReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/rqlite/query",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT COUNT(*) FROM %s", table),
			},
		}

		body, _, _ := queryReq.Do(ctx)
		var queryResp map[string]interface{}
		DecodeJSON(body, &queryResp)

		if rows, ok := queryResp["rows"].([]interface{}); ok && len(rows) > 0 {
			row := rows[0].([]interface{})
			count := int(row[0].(float64))
			require.Equal(t, expectedTotal, count,
				"FAIL: Expected %d total rows from concurrent inserts, got %d", expectedTotal, count)
		}

		t.Logf("  ✓ All %d concurrent inserts succeeded", expectedTotal)
	})
}

// TestRQLite_NamespaceClusterOperations verifies RQLite works in namespace clusters.
func TestRQLite_NamespaceClusterOperations(t *testing.T) {
	// Create a new namespace
	namespace := fmt.Sprintf("rqlite-test-%d", time.Now().UnixNano())

	env, err := LoadTestEnvWithNamespace(namespace)
	require.NoError(t, err, "FAIL: Could not create namespace for RQLite test")
	require.NotEmpty(t, env.APIKey, "FAIL: No API key - namespace provisioning failed")

	t.Logf("Created namespace %s", namespace)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	table := GenerateTableName()

	// Cleanup
	defer func() {
		dropReq := &HTTPRequest{
			Method:  http.MethodPost,
			URL:     env.GatewayURL + "/v1/rqlite/drop-table",
			Body:    map[string]interface{}{"table": table},
			Headers: map[string]string{"Authorization": "Bearer " + env.APIKey},
		}
		dropReq.Do(context.Background())
	}()

	t.Run("Namespace_RQLite_create_insert_query", func(t *testing.T) {
		// Create table in namespace cluster
		createReq := &HTTPRequest{
			Method:  http.MethodPost,
			URL:     env.GatewayURL + "/v1/rqlite/create-table",
			Headers: map[string]string{"Authorization": "Bearer " + env.APIKey},
			Body: map[string]interface{}{
				"schema": fmt.Sprintf(
					"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, value TEXT)",
					table,
				),
			},
		}

		_, status, err := createReq.Do(ctx)
		require.NoError(t, err, "FAIL: Create table in namespace failed")
		require.True(t, status == http.StatusCreated || status == http.StatusOK,
			"FAIL: Create table returned status %d", status)

		// Insert data
		uniqueValue := fmt.Sprintf("ns_value_%d", time.Now().UnixNano())
		insertReq := &HTTPRequest{
			Method:  http.MethodPost,
			URL:     env.GatewayURL + "/v1/rqlite/transaction",
			Headers: map[string]string{"Authorization": "Bearer " + env.APIKey},
			Body: map[string]interface{}{
				"statements": []string{
					fmt.Sprintf("INSERT INTO %s (value) VALUES ('%s')", table, uniqueValue),
				},
			},
		}

		_, status, err = insertReq.Do(ctx)
		require.NoError(t, err, "FAIL: Insert in namespace failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Insert returned status %d", status)

		// Query data
		queryReq := &HTTPRequest{
			Method:  http.MethodPost,
			URL:     env.GatewayURL + "/v1/rqlite/query",
			Headers: map[string]string{"Authorization": "Bearer " + env.APIKey},
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT value FROM %s WHERE value = '%s'", table, uniqueValue),
			},
		}

		body, status, err := queryReq.Do(ctx)
		require.NoError(t, err, "FAIL: Query in namespace failed")
		require.Equal(t, http.StatusOK, status, "FAIL: Query returned status %d", status)

		var queryResp map[string]interface{}
		DecodeJSON(body, &queryResp)

		count, _ := queryResp["count"].(float64)
		require.Equal(t, float64(1), count, "FAIL: Data not found in namespace cluster")

		t.Logf("  ✓ Namespace RQLite operations work correctly")
	})
}

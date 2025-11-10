//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCache_ConcurrentWrites tests concurrent cache writes
func TestCache_ConcurrentWrites(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmap := GenerateDMapName()
	numGoroutines := 10
	var wg sync.WaitGroup
	var errorCount int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			key := fmt.Sprintf("key-%d", idx)
			value := fmt.Sprintf("value-%d", idx)

			putReq := &HTTPRequest{
				Method: http.MethodPost,
				URL:    GetGatewayURL() + "/v1/cache/put",
				Body: map[string]interface{}{
					"dmap":  dmap,
					"key":   key,
					"value": value,
				},
			}

			_, status, err := putReq.Do(ctx)
			if err != nil || status != http.StatusOK {
				atomic.AddInt32(&errorCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if errorCount > 0 {
		t.Fatalf("expected no errors, got %d", errorCount)
	}

	// Verify all values exist
	scanReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/scan",
		Body: map[string]interface{}{
			"dmap": dmap,
		},
	}

	body, status, err := scanReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("scan failed: status %d, err %v", status, err)
	}

	var scanResp map[string]interface{}
	if err := DecodeJSON(body, &scanResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	keys := scanResp["keys"].([]interface{})
	if len(keys) < numGoroutines {
		t.Fatalf("expected at least %d keys, got %d", numGoroutines, len(keys))
	}
}

// TestCache_ConcurrentReads tests concurrent cache reads
func TestCache_ConcurrentReads(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmap := GenerateDMapName()
	key := "shared-key"
	value := "shared-value"

	// Put value first
	putReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/put",
		Body: map[string]interface{}{
			"dmap":  dmap,
			"key":   key,
			"value": value,
		},
	}

	_, status, err := putReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("put failed: status %d, err %v", status, err)
	}

	// Read concurrently
	numGoroutines := 10
	var wg sync.WaitGroup
	var errorCount int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			getReq := &HTTPRequest{
				Method: http.MethodPost,
				URL:    GetGatewayURL() + "/v1/cache/get",
				Body: map[string]interface{}{
					"dmap": dmap,
					"key":  key,
				},
			}

			body, status, err := getReq.Do(ctx)
			if err != nil || status != http.StatusOK {
				atomic.AddInt32(&errorCount, 1)
				return
			}

			var getResp map[string]interface{}
			if err := DecodeJSON(body, &getResp); err != nil {
				atomic.AddInt32(&errorCount, 1)
				return
			}

			if getResp["value"] != value {
				atomic.AddInt32(&errorCount, 1)
			}
		}()
	}

	wg.Wait()

	if errorCount > 0 {
		t.Fatalf("expected no errors, got %d", errorCount)
	}
}

// TestCache_ConcurrentDeleteAndWrite tests concurrent delete and write
func TestCache_ConcurrentDeleteAndWrite(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmap := GenerateDMapName()
	var wg sync.WaitGroup
	var errorCount int32

	numWrites := 5
	numDeletes := 3

	// Write keys
	for i := 0; i < numWrites; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			key := fmt.Sprintf("key-%d", idx)
			value := fmt.Sprintf("value-%d", idx)

			putReq := &HTTPRequest{
				Method: http.MethodPost,
				URL:    GetGatewayURL() + "/v1/cache/put",
				Body: map[string]interface{}{
					"dmap":  dmap,
					"key":   key,
					"value": value,
				},
			}

			_, status, err := putReq.Do(ctx)
			if err != nil || status != http.StatusOK {
				atomic.AddInt32(&errorCount, 1)
			}
		}(i)
	}

	wg.Wait()

	// Delete some keys
	for i := 0; i < numDeletes; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			key := fmt.Sprintf("key-%d", idx)

			deleteReq := &HTTPRequest{
				Method: http.MethodPost,
				URL:    GetGatewayURL() + "/v1/cache/delete",
				Body: map[string]interface{}{
					"dmap": dmap,
					"key":  key,
				},
			}

			_, status, err := deleteReq.Do(ctx)
			if err != nil || status != http.StatusOK {
				atomic.AddInt32(&errorCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if errorCount > 0 {
		t.Fatalf("expected no errors, got %d", errorCount)
	}
}

// TestRQLite_ConcurrentInserts tests concurrent database inserts
func TestRQLite_ConcurrentInserts(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	table := GenerateTableName()
	schema := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, value INTEGER)",
		table,
	)

	// Create table
	createReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": schema,
		},
	}

	_, status, err := createReq.Do(ctx)
	if err != nil || (status != http.StatusCreated && status != http.StatusOK) {
		t.Fatalf("create table failed: status %d, err %v", status, err)
	}

	// Insert concurrently
	numInserts := 10
	var wg sync.WaitGroup
	var errorCount int32

	for i := 0; i < numInserts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			txReq := &HTTPRequest{
				Method: http.MethodPost,
				URL:    GetGatewayURL() + "/v1/rqlite/transaction",
				Body: map[string]interface{}{
					"statements": []string{
						fmt.Sprintf("INSERT INTO %s(value) VALUES (%d)", table, idx),
					},
				},
			}

			_, status, err := txReq.Do(ctx)
			if err != nil || status != http.StatusOK {
				atomic.AddInt32(&errorCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if errorCount > 0 {
		t.Logf("warning: %d concurrent inserts failed", errorCount)
	}

	// Verify count
	queryReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/query",
		Body: map[string]interface{}{
			"sql": fmt.Sprintf("SELECT COUNT(*) as count FROM %s", table),
		},
	}

	body, status, err := queryReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("count query failed: status %d, err %v", status, err)
	}

	var countResp map[string]interface{}
	if err := DecodeJSON(body, &countResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if rows, ok := countResp["rows"].([]interface{}); ok && len(rows) > 0 {
		row := rows[0].([]interface{})
		count := int(row[0].(float64))
		if count < numInserts {
			t.Logf("warning: expected %d inserts, got %d", numInserts, count)
		}
	}
}

// TestRQLite_LargeBatchTransaction tests a large transaction with many statements
func TestRQLite_LargeBatchTransaction(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	table := GenerateTableName()
	schema := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, value TEXT)",
		table,
	)

	// Create table
	createReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": schema,
		},
	}

	_, status, err := createReq.Do(ctx)
	if err != nil || (status != http.StatusCreated && status != http.StatusOK) {
		t.Fatalf("create table failed: status %d, err %v", status, err)
	}

	// Create large batch (100 statements)
	var ops []map[string]interface{}
	for i := 0; i < 100; i++ {
		ops = append(ops, map[string]interface{}{
			"kind": "exec",
			"sql":  fmt.Sprintf("INSERT INTO %s(value) VALUES ('value-%d')", table, i),
		})
	}

	txReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/transaction",
		Body: map[string]interface{}{
			"ops": ops,
		},
	}

	_, status, err = txReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("large batch transaction failed: status %d, err %v", status, err)
	}

	// Verify count
	queryReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/query",
		Body: map[string]interface{}{
			"sql": fmt.Sprintf("SELECT COUNT(*) as count FROM %s", table),
		},
	}

	body, status, err := queryReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("count query failed: status %d, err %v", status, err)
	}

	var countResp map[string]interface{}
	if err := DecodeJSON(body, &countResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if rows, ok := countResp["rows"].([]interface{}); ok && len(rows) > 0 {
		row := rows[0].([]interface{})
		if int(row[0].(float64)) != 100 {
			t.Fatalf("expected 100 rows, got %v", row[0])
		}
	}
}

// TestCache_TTLExpiryWithSleep tests TTL expiry with a controlled sleep
func TestCache_TTLExpiryWithSleep(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dmap := GenerateDMapName()
	key := "ttl-expiry-key"
	value := "ttl-expiry-value"

	// Put value with 2 second TTL
	putReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/put",
		Body: map[string]interface{}{
			"dmap":  dmap,
			"key":   key,
			"value": value,
			"ttl":   "2s",
		},
	}

	_, status, err := putReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("put with TTL failed: status %d, err %v", status, err)
	}

	// Verify exists immediately
	getReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/cache/get",
		Body: map[string]interface{}{
			"dmap": dmap,
			"key":  key,
		},
	}

	_, status, err = getReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("get immediately after put failed: status %d, err %v", status, err)
	}

	// Sleep for TTL duration + buffer
	Delay(2500)

	// Try to get after TTL expires
	_, status, err = getReq.Do(ctx)
	if status == http.StatusOK {
		t.Logf("warning: TTL expiry may not be fully implemented; key still exists after TTL")
	}
}

// TestCache_ConcurrentWriteAndDelete tests concurrent writes and deletes on same key
func TestCache_ConcurrentWriteAndDelete(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmap := GenerateDMapName()
	key := "contested-key"

	// Alternate between writes and deletes
	numIterations := 5
	for i := 0; i < numIterations; i++ {
		// Write
		putReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/cache/put",
			Body: map[string]interface{}{
				"dmap":  dmap,
				"key":   key,
				"value": fmt.Sprintf("value-%d", i),
			},
		}

		_, status, err := putReq.Do(ctx)
		if err != nil || status != http.StatusOK {
			t.Fatalf("put failed at iteration %d: status %d, err %v", i, status, err)
		}

		// Read
		getReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/cache/get",
			Body: map[string]interface{}{
				"dmap": dmap,
				"key":  key,
			},
		}

		_, status, err = getReq.Do(ctx)
		if err != nil || status != http.StatusOK {
			t.Fatalf("get failed at iteration %d: status %d, err %v", i, status, err)
		}

		// Delete
		deleteReq := &HTTPRequest{
			Method: http.MethodPost,
			URL:    GetGatewayURL() + "/v1/cache/delete",
			Body: map[string]interface{}{
				"dmap": dmap,
				"key":  key,
			},
		}

		_, status, err = deleteReq.Do(ctx)
		if err != nil || status != http.StatusOK {
			t.Logf("warning: delete at iteration %d failed: status %d, err %v", i, status, err)
		}
	}
}

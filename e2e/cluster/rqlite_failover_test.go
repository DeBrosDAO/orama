//go:build e2e

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRQLite_ReadConsistencyLevels tests that different consistency levels work.
func TestRQLite_ReadConsistencyLevels(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	gatewayURL := e2e.GetGatewayURL()
	table := e2e.GenerateTableName()

	defer func() {
		dropReq := &e2e.HTTPRequest{
			Method: http.MethodPost,
			URL:    gatewayURL + "/v1/rqlite/drop-table",
			Body:   map[string]interface{}{"table": table},
		}
		dropReq.Do(context.Background())
	}()

	// Create table
	createReq := &e2e.HTTPRequest{
		Method: http.MethodPost,
		URL:    gatewayURL + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, val TEXT)", table),
		},
	}
	_, status, err := createReq.Do(ctx)
	require.NoError(t, err)
	require.True(t, status == http.StatusOK || status == http.StatusCreated, "create table got %d", status)

	// Insert data
	insertReq := &e2e.HTTPRequest{
		Method: http.MethodPost,
		URL:    gatewayURL + "/v1/rqlite/transaction",
		Body: map[string]interface{}{
			"statements": []string{
				fmt.Sprintf("INSERT INTO %s(val) VALUES ('consistency-test')", table),
			},
		},
	}
	_, status, err = insertReq.Do(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)

	t.Run("Default consistency read", func(t *testing.T) {
		queryReq := &e2e.HTTPRequest{
			Method: http.MethodPost,
			URL:    gatewayURL + "/v1/rqlite/query",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT * FROM %s", table),
			},
		}
		body, status, err := queryReq.Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
		t.Logf("Default read: %s", string(body))
	})

	t.Run("Strong consistency read", func(t *testing.T) {
		queryReq := &e2e.HTTPRequest{
			Method: http.MethodPost,
			URL:    gatewayURL + "/v1/rqlite/query?level=strong",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT * FROM %s", table),
			},
		}
		body, status, err := queryReq.Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
		t.Logf("Strong read: %s", string(body))
	})

	t.Run("Weak consistency read", func(t *testing.T) {
		queryReq := &e2e.HTTPRequest{
			Method: http.MethodPost,
			URL:    gatewayURL + "/v1/rqlite/query?level=weak",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT * FROM %s", table),
			},
		}
		body, status, err := queryReq.Do(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
		t.Logf("Weak read: %s", string(body))
	})
}

// TestRQLite_WriteAfterMultipleReads verifies write-read cycles stay consistent.
func TestRQLite_WriteAfterMultipleReads(t *testing.T) {
	e2e.SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	gatewayURL := e2e.GetGatewayURL()
	table := e2e.GenerateTableName()

	defer func() {
		dropReq := &e2e.HTTPRequest{
			Method: http.MethodPost,
			URL:    gatewayURL + "/v1/rqlite/drop-table",
			Body:   map[string]interface{}{"table": table},
		}
		dropReq.Do(context.Background())
	}()

	createReq := &e2e.HTTPRequest{
		Method: http.MethodPost,
		URL:    gatewayURL + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, counter INTEGER DEFAULT 0)", table),
		},
	}
	_, status, err := createReq.Do(ctx)
	require.NoError(t, err)
	require.True(t, status == http.StatusOK || status == http.StatusCreated)

	// Write-read cycle 10 times
	for i := 1; i <= 10; i++ {
		insertReq := &e2e.HTTPRequest{
			Method: http.MethodPost,
			URL:    gatewayURL + "/v1/rqlite/transaction",
			Body: map[string]interface{}{
				"statements": []string{
					fmt.Sprintf("INSERT INTO %s(counter) VALUES (%d)", table, i),
				},
			},
		}
		_, status, err := insertReq.Do(ctx)
		require.NoError(t, err, "insert %d failed", i)
		require.Equal(t, http.StatusOK, status, "insert %d got status %d", i, status)

		queryReq := &e2e.HTTPRequest{
			Method: http.MethodPost,
			URL:    gatewayURL + "/v1/rqlite/query",
			Body: map[string]interface{}{
				"sql": fmt.Sprintf("SELECT COUNT(*) as cnt FROM %s", table),
			},
		}
		body, _, _ := queryReq.Do(ctx)
		t.Logf("Iteration %d: %s", i, string(body))
	}

	// Final verification
	queryReq := &e2e.HTTPRequest{
		Method: http.MethodPost,
		URL:    gatewayURL + "/v1/rqlite/query",
		Body: map[string]interface{}{
			"sql": fmt.Sprintf("SELECT COUNT(*) as cnt FROM %s", table),
		},
	}
	body, status, err := queryReq.Do(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)

	var result map[string]interface{}
	json.Unmarshal(body, &result)
	t.Logf("Final count result: %s", string(body))
}

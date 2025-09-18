//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		t.Skipf("%s not set; skipping", key)
	}
	return v
}

func TestClient_Database_CreateQueryMigrate(t *testing.T) {
	apiKey := requireEnv(t, "GATEWAY_API_KEY")
	namespace := getenv("E2E_CLIENT_NAMESPACE", "default")

	cfg := client.DefaultClientConfig(namespace)
	cfg.APIKey = apiKey
	cfg.QuietMode = true

	if v := strings.TrimSpace(os.Getenv("E2E_BOOTSTRAP_PEERS")); v != "" {
		parts := strings.Split(v, ",")
		var peers []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				peers = append(peers, p)
			}
		}
		cfg.BootstrapPeers = peers
	}
	if v := strings.TrimSpace(os.Getenv("E2E_RQLITE_NODES")); v != "" {
		nodes := strings.Fields(strings.ReplaceAll(v, ",", " "))
		cfg.DatabaseEndpoints = nodes
	}

	c, err := client.NewClient(cfg)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Disconnect() })

	// Unique table per run
	table := fmt.Sprintf("e2e_items_client_%d", time.Now().UnixNano())
	schema := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)", table)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.Database().CreateTable(ctx, schema); err != nil {
		t.Fatalf("create table: %v", err)
	}
	// Insert via transaction
	stmts := []string{
		fmt.Sprintf("INSERT INTO %s(name) VALUES ('alpha')", table),
		fmt.Sprintf("INSERT INTO %s(name) VALUES ('beta')", table),
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()
	if err := c.Database().Transaction(ctx2, stmts); err != nil {
		t.Fatalf("transaction: %v", err)
	}
	// Query rows
	ctx3, cancel3 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel3()
	res, err := c.Database().Query(ctx3, fmt.Sprintf("SELECT name FROM %s ORDER BY id", table))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if res.Count < 2 {
		t.Fatalf("expected at least 2 rows, got %d", res.Count)
	}
}

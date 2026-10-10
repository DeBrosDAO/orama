//go:build e2e_fleet

package sdkgo

import (
	"context"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/pkg/client"
)

// TestGoClientDatabase_documentedQueriesWork runs website/src/docs/developer/go-sdk.mdx
// "Database Client" with the configuration its Quick Start gives (a gateway
// URL and a credential): create a table, write with parameters, read back,
// a transaction, the schema, drop. The SDK sends these straight to RQLite
// endpoints (DatabaseClientImpl.getRQLiteConnection), which a program outside
// the WireGuard mesh cannot reach, so this is where that shows.
func TestGoClientDatabase_documentedQueriesWork(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	db := newClient(t, n, owner(n)).Database()
	ctx, cancel := context.WithTimeout(t.Context(), callBudget)
	defer cancel()
	const table = "e2e_sdk_users"
	schema := "CREATE TABLE IF NOT EXISTS " + table + " (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)"
	if err := db.CreateTable(ctx, schema); err != nil {
		t.Fatalf("CreateTable from outside the mesh: %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropTable(context.Background(), table); err != nil {
			t.Errorf("cleanup: DropTable: %v", err)
		}
	})
	if _, err := db.Query(ctx, "INSERT INTO "+table+" (name) VALUES (?)", "Alice'); DROP TABLE "+table+"; --"); err != nil {
		t.Fatalf("parameterised INSERT: %v", err)
	}
	if err := db.Transaction(ctx, []string{"INSERT INTO " + table + " (name) VALUES ('Bob')", "INSERT INTO " + table + " (name) VALUES ('Carol')"}); err != nil {
		t.Fatalf("Transaction: %v", err)
	}
	res, err := db.Query(ctx, "SELECT name FROM "+table+" ORDER BY id")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if res.Count != 3 || fmt.Sprint(res.Rows[0][0]) != "Alice'); DROP TABLE "+table+"; --" {
		t.Errorf("SELECT returned %d rows: %v", res.Count, res.Rows)
	}
	info, err := db.GetSchema(ctx)
	if err != nil || info == nil {
		t.Fatalf("GetSchema: %v", err)
	}
}

// TestGoClientDatabase_needsConnect: the database client refuses before
// Connect rather than guessing (DatabaseClientImpl.checkConnection).
func TestGoClientDatabase_needsConnect(t *testing.T) {
	t.Parallel()
	trustRunCA(t)
	cfg := client.DefaultClientConfig(appName)
	cfg.QuietMode, cfg.JWT = true, "x.y.z"
	c, err := client.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Database().Query(t.Context(), "SELECT 1"); err == nil {
		t.Fatal("Database().Query before Connect succeeded")
	}
}

// TestGoClientPubSub_outsideANodeFailsFast: the SDK publishes through the
// node's pubsub socket, which "admits only the gateways' user"
// (website/src/docs/developer/go-sdk.mdx ClientConfig.PubSubSocket), so a program outside a
// node has no pubsub transport: Publish, ListTopics and Subscribe must fail
// with an error, promptly, never report a delivery that did not happen.
func TestGoClientPubSub_outsideANodeFailsFast(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	ps := newClient(t, n, owner(n)).PubSub()
	ctx, cancel := context.WithTimeout(t.Context(), failFastBudget)
	defer cancel()
	if err := ps.Publish(ctx, "e2e-topic", []byte("hello")); err == nil {
		t.Error("Publish without a reachable pubsub socket reported success")
	}
	if _, err := ps.ListTopics(ctx); err == nil {
		t.Error("ListTopics without a reachable pubsub socket reported success")
	}
	err := ps.Subscribe(ctx, "e2e-topic", func(string, []byte) error { return nil })
	if err == nil {
		t.Error("Subscribe without a reachable pubsub socket reported success")
		if uerr := ps.Unsubscribe(context.Background(), "e2e-topic"); uerr != nil {
			t.Errorf("Unsubscribe: %v", uerr)
		}
	}
	if ctx.Err() != nil {
		t.Errorf("pubsub calls without a transport took the whole %s instead of failing", failFastBudget)
	}
}

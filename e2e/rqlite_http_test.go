//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestRQLite_CreateTable(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	table := GenerateTableName()
	schema := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)",
		table,
	)

	req := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": schema,
		},
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("create table request failed: %v", err)
	}

	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("expected status 201 or 200, got %d: %s", status, string(body))
	}
}

func TestRQLite_InsertQuery(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	table := GenerateTableName()
	schema := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)",
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

	// Insert rows
	insertReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/transaction",
		Body: map[string]interface{}{
			"statements": []string{
				fmt.Sprintf("INSERT INTO %s(name) VALUES ('alice')", table),
				fmt.Sprintf("INSERT INTO %s(name) VALUES ('bob')", table),
			},
		},
	}

	_, status, err = insertReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("insert failed: status %d, err %v", status, err)
	}

	// Query rows
	queryReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/query",
		Body: map[string]interface{}{
			"sql": fmt.Sprintf("SELECT name FROM %s ORDER BY id", table),
		},
	}

	body, status, err := queryReq.Do(ctx)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var queryResp map[string]interface{}
	if err := DecodeJSON(body, &queryResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if queryResp["count"].(float64) < 2 {
		t.Fatalf("expected at least 2 rows, got %v", queryResp["count"])
	}
}

func TestRQLite_DropTable(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	table := GenerateTableName()
	schema := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, note TEXT)",
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

	// Drop table
	dropReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/drop-table",
		Body: map[string]interface{}{
			"table": table,
		},
	}

	_, status, err = dropReq.Do(ctx)
	if err != nil {
		t.Fatalf("drop table request failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	// Verify table doesn't exist via schema
	schemaReq := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/rqlite/schema",
	}

	body, status, err := schemaReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Logf("warning: failed to verify schema after drop: status %d, err %v", status, err)
		return
	}

	var schemaResp map[string]interface{}
	if err := DecodeJSON(body, &schemaResp); err != nil {
		t.Logf("warning: failed to decode schema response: %v", err)
		return
	}

	if tables, ok := schemaResp["tables"].([]interface{}); ok {
		for _, tbl := range tables {
			tblMap := tbl.(map[string]interface{})
			if tblMap["name"] == table {
				t.Fatalf("table %s still present after drop", table)
			}
		}
	}
}

func TestRQLite_Schema(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method: http.MethodGet,
		URL:    GetGatewayURL() + "/v1/rqlite/schema",
	}

	body, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("schema request failed: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	var resp map[string]interface{}
	if err := DecodeJSON(body, &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if _, ok := resp["tables"]; !ok {
		t.Fatalf("expected 'tables' field in response")
	}
}

func TestRQLite_MalformedSQL(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/query",
		Body: map[string]interface{}{
			"sql": "SELECT * FROM nonexistent_table WHERE invalid syntax",
		},
	}

	_, status, err := req.Do(ctx)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// Should get an error response
	if status == http.StatusOK {
		t.Fatalf("expected error for malformed SQL, got status 200")
	}
}

func TestRQLite_LargeTransaction(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	// Generate large transaction (50 inserts)
	var statements []string
	for i := 0; i < 50; i++ {
		statements = append(statements, fmt.Sprintf("INSERT INTO %s(value) VALUES (%d)", table, i))
	}

	txReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/transaction",
		Body: map[string]interface{}{
			"statements": statements,
		},
	}

	_, status, err = txReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("large transaction failed: status %d, err %v", status, err)
	}

	// Verify all rows were inserted
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

	// Extract count from result
	if rows, ok := countResp["rows"].([]interface{}); ok && len(rows) > 0 {
		row := rows[0].([]interface{})
		if row[0].(float64) != 50 {
			t.Fatalf("expected 50 rows, got %v", row[0])
		}
	}
}

func TestRQLite_ForeignKeyMigration(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	orgsTable := GenerateTableName()
	usersTable := GenerateTableName()

	// Create base tables
	createOrgsReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, name TEXT)",
				orgsTable,
			),
		},
	}

	_, status, err := createOrgsReq.Do(ctx)
	if err != nil || (status != http.StatusCreated && status != http.StatusOK) {
		t.Fatalf("create orgs table failed: status %d, err %v", status, err)
	}

	createUsersReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/create-table",
		Body: map[string]interface{}{
			"schema": fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, name TEXT, org_id INTEGER, age TEXT)",
				usersTable,
			),
		},
	}

	_, status, err = createUsersReq.Do(ctx)
	if err != nil || (status != http.StatusCreated && status != http.StatusOK) {
		t.Fatalf("create users table failed: status %d, err %v", status, err)
	}

	// Seed data
	seedReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/transaction",
		Body: map[string]interface{}{
			"statements": []string{
				fmt.Sprintf("INSERT INTO %s(id,name) VALUES (1,'org')", orgsTable),
				fmt.Sprintf("INSERT INTO %s(id,name,org_id,age) VALUES (1,'alice',1,'30')", usersTable),
			},
		},
	}

	_, status, err = seedReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("seed transaction failed: status %d, err %v", status, err)
	}

	// Migrate: change age type and add FK
	migrationReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/transaction",
		Body: map[string]interface{}{
			"statements": []string{
				fmt.Sprintf(
					"CREATE TABLE %s_new (id INTEGER PRIMARY KEY, name TEXT, org_id INTEGER, age INTEGER, FOREIGN KEY(org_id) REFERENCES %s(id) ON DELETE CASCADE)",
					usersTable, orgsTable,
				),
				fmt.Sprintf(
					"INSERT INTO %s_new (id,name,org_id,age) SELECT id,name,org_id, CAST(age AS INTEGER) FROM %s",
					usersTable, usersTable,
				),
				fmt.Sprintf("DROP TABLE %s", usersTable),
				fmt.Sprintf("ALTER TABLE %s_new RENAME TO %s", usersTable, usersTable),
			},
		},
	}

	_, status, err = migrationReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("migration transaction failed: status %d, err %v", status, err)
	}

	// Verify data is intact
	queryReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/query",
		Body: map[string]interface{}{
			"sql": fmt.Sprintf("SELECT name, org_id, age FROM %s", usersTable),
		},
	}

	body, status, err := queryReq.Do(ctx)
	if err != nil || status != http.StatusOK {
		t.Fatalf("query after migration failed: status %d, err %v", status, err)
	}

	var queryResp map[string]interface{}
	if err := DecodeJSON(body, &queryResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if queryResp["count"].(float64) != 1 {
		t.Fatalf("expected 1 row after migration, got %v", queryResp["count"])
	}
}

func TestRQLite_DropNonexistentTable(t *testing.T) {
	SkipIfMissingGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dropReq := &HTTPRequest{
		Method: http.MethodPost,
		URL:    GetGatewayURL() + "/v1/rqlite/drop-table",
		Body: map[string]interface{}{
			"table": "nonexistent_table_xyz_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		},
	}

	_, status, err := dropReq.Do(ctx)
	if err != nil {
		t.Logf("warning: drop nonexistent table request failed: %v", err)
		return
	}

	// Should get an error (400 or 404)
	if status == http.StatusOK {
		t.Logf("warning: expected error for dropping nonexistent table, got status 200")
	}
}

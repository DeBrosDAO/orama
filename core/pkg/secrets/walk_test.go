package secrets

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
)

func TestWalk_rewritesLegacyAndIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE function_secrets (
		id TEXT PRIMARY KEY, encrypted_value TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	root := Root{CurrentID: "1", CurrentIKM: "ikm-walk", WriteVersioned: true}
	ks, err := root.Keyset("orama-secrets-encryption-v1")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := Encrypt("token", ks.Current)
	if err != nil {
		t.Fatal(err)
	}
	store := rqlite.NewClient(db)
	if _, err := store.Exec(context.Background(),
		`INSERT INTO function_secrets (id, encrypted_value) VALUES ('a', ?)`, legacy); err != nil {
		t.Fatal(err)
	}

	first, err := Walk(context.Background(), store, root, []Column{{
		Table: "function_secrets", Column: "encrypted_value", IDCols: []string{"id"},
		Purpose: "orama-secrets-encryption-v1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Rewrote != 1 {
		t.Fatalf("first pass rewrote %d", first.Rewrote)
	}

	var rows []struct {
		Value string `db:"encrypted_value"`
	}
	if err := store.Query(context.Background(), &rows, `SELECT encrypted_value FROM function_secrets`); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !IsEncrypted(rows[0].Value) {
		t.Fatalf("rows %+v", rows)
	}
	env, err := ParseEnvelope(rows[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if env.Version != 1 || env.KeyID != "1" {
		t.Fatalf("envelope %+v", env)
	}
	got, err := ks.Decrypt(rows[0].Value)
	if err != nil || got != "token" {
		t.Fatalf("decrypt %q %v", got, err)
	}

	second, err := Walk(context.Background(), store, root, []Column{{
		Table: "function_secrets", Column: "encrypted_value", IDCols: []string{"id"},
		Purpose: "orama-secrets-encryption-v1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Rewrote != 0 {
		t.Fatalf("second pass rewrote %d, want 0", second.Rewrote)
	}
}

func TestWalk_sealsPlaintextLeftovers(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE deployments (id TEXT PRIMARY KEY, environment TEXT)`); err != nil {
		t.Fatal(err)
	}
	store := rqlite.NewClient(db)
	if _, err := store.Exec(context.Background(),
		`INSERT INTO deployments (id, environment) VALUES ('d1', '{"A":"b"}')`); err != nil {
		t.Fatal(err)
	}
	root := Root{CurrentID: "1", CurrentIKM: "ikm-env", WriteVersioned: true}
	res, err := Walk(context.Background(), store, root, []Column{{
		Table: "deployments", Column: "environment", IDCols: []string{"id"},
		Purpose: "orama-deployment-environment-v1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rewrote != 1 {
		t.Fatalf("rewrote %d", res.Rewrote)
	}
	var rows []struct {
		Env string `db:"environment"`
	}
	if err := store.Query(context.Background(), &rows, `SELECT environment FROM deployments`); err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(rows[0].Env) {
		t.Fatalf("still plaintext %q", rows[0].Env)
	}
}

func TestWalk_missingTableIsNotAnError(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := rqlite.NewClient(db)
	root := Root{CurrentID: "1", CurrentIKM: "ikm"}
	res, err := Walk(context.Background(), store, root, []Column{{
		Table: "function_secrets", Column: "encrypted_value", IDCols: []string{"id"},
		Purpose: "orama-secrets-encryption-v1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Missing != 1 {
		t.Fatalf("missing %d", res.Missing)
	}
}

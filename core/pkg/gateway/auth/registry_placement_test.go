package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// Where platform state lives.
//
// A namespace gateway holds two databases: the tenant's own RQLite, which the
// tenant reads and writes and can export whole, and the cluster registry it
// validates credentials against. It is told about the second one *after* the
// auth service is built.
//
// Everything that says who somebody is, or what they may do, belongs in the
// second. It used to be split: keys and grants went to the registry (bug-162),
// and sessions, nonces, revocations, the audit trail and pending device logins
// stayed in the tenant's database — where a revocation written on the index
// never appeared, and where a namespace admin could export or import them.

// twoDatabaseService builds a service the way a namespace gateway is built.
func twoDatabaseService(t *testing.T) (svc *Service, tenant, registry *sql.DB) {
	t.Helper()

	open := func() *sql.DB {
		db, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
			t.Fatalf("apply migrations: %v", err)
		}
		return db
	}
	tenant, registry = open(), open()

	// The namespace exists in both, with deliberately different ids: an id
	// resolved against the wrong database points at the wrong namespace, and a
	// test where both are 1 could not tell.
	if _, err := tenant.Exec(`INSERT INTO namespaces(id, name) VALUES (40, 'acme')`); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Exec(`INSERT INTO namespaces(id, name) VALUES (70, 'acme')`); err != nil {
		t.Fatal(err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	logger, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatal(err)
	}
	svc, err = NewService(logger, &sqliteNet{db: &sqliteDatabase{db: tenant}}, string(keyPEM), "acme")
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	svc.SetAPIKeyRegistry(&sqliteNet{db: &sqliteDatabase{db: registry}})
	svc.SetRqliteClient(&sqliteRqlite{db: registry})
	return svc, tenant, registry
}

// namespaceIDOf reads the namespace id a table's single row is keyed on.
func namespaceIDOf(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT namespace_id FROM ` + table + ` LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("read namespace_id from %s: %v", table, err)
	}
	return id
}

// rowsIn counts a table, tolerating a table that is not there.
func rowsIn(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestPlatformState_landsInTheRegistryAndNotTheTenantsDatabase(t *testing.T) {
	svc, tenant, registry := twoDatabaseService(t)
	ctx := context.Background()

	t.Run("a session", func(t *testing.T) {
		if _, _, _, err := svc.IssueTokens(ctx, "0xowner", "acme"); err != nil {
			t.Fatalf("IssueTokens: %v", err)
		}
		if rowsIn(t, registry, "refresh_tokens") != 1 {
			t.Error("the session was not recorded in the cluster registry, so no other gateway can refresh or revoke it")
		}
		// The id it is keyed on has to come from the same database it is
		// written to, or the row points at a different namespace or at none.
		if got := namespaceIDOf(t, registry, "refresh_tokens"); got != 70 {
			t.Errorf("the session is keyed on namespace id %d, want the registry's 70", got)
		}
		if rowsIn(t, tenant, "refresh_tokens") != 0 {
			t.Error("the session was recorded in the tenant's own database, where the tenant can read and rewrite it")
		}
	})

	t.Run("a challenge", func(t *testing.T) {
		if err := svc.insertNonce(ctx, "0xowner", "nonce-1", "login", "acme"); err != nil {
			t.Fatalf("insertNonce: %v", err)
		}
		if rowsIn(t, registry, "nonces") != 1 {
			t.Error("the challenge was not recorded in the registry, so the gateway that verifies it cannot consume it")
		}
		if got := namespaceIDOf(t, registry, "nonces"); got != 70 {
			t.Errorf("the challenge is keyed on namespace id %d, want the registry's 70 — the id was looked up "+
				"in one database and the row written to another", got)
		}
		if rowsIn(t, tenant, "nonces") != 0 {
			t.Error("the challenge was recorded in the tenant's own database, where the tenant can mint one")
		}
	})

	t.Run("a revocation", func(t *testing.T) {
		if err := svc.Revocations().RevokeToken(ctx, "jti-1", 1<<40, "test"); err != nil {
			t.Fatalf("RevokeToken: %v", err)
		}
		if rowsIn(t, registry, "revoked_tokens") != 1 {
			t.Error("the revocation was not recorded in the registry, so it reaches no other gateway")
		}
		if rowsIn(t, tenant, "revoked_tokens") != 0 {
			t.Error("the revocation was recorded in the tenant's own database, where the tenant can delete it")
		}
	})

	t.Run("an audit event", func(t *testing.T) {
		svc.Audit().Record(ctx, AuditEvent{Namespace: "acme", Actor: "0xowner", Action: AuditKeyIssued, Result: AuditSuccess})
		if rowsIn(t, registry, "audit_events") != 1 {
			t.Error("the event was not recorded in the registry, so `orama audit` will not show it")
		}
		if rowsIn(t, tenant, "audit_events") != 0 {
			t.Error("the event was recorded in the tenant's own database, where the subject of it can delete it")
		}
	})

	t.Run("a pending device login", func(t *testing.T) {
		if _, err := svc.StartDeviceAuthorization(ctx, "acme"); err != nil {
			t.Fatalf("StartDeviceAuthorization: %v", err)
		}
		if rowsIn(t, registry, "device_authorizations") != 1 {
			t.Error("the pending login was not recorded in the registry, so the gateway that approves it cannot find it")
		}
		if rowsIn(t, tenant, "device_authorizations") != 0 {
			t.Error("the pending login was recorded in the tenant's own database, where the tenant can approve their own")
		}
	})
}

// Every row above is keyed on a namespace id. Resolving that id against the
// wrong database points it at a different namespace, or at nothing.
func TestResolveNamespaceID_resolvesAgainstTheRegistry(t *testing.T) {
	svc, _, _ := twoDatabaseService(t)

	id, err := svc.ResolveNamespaceID(context.Background(), "acme")
	if err != nil {
		t.Fatalf("ResolveNamespaceID: %v", err)
	}
	if cellInt64(id) != 70 {
		t.Errorf("id = %v, want the registry's 70 rather than the tenant's 40", id)
	}
}

// It used to `INSERT OR IGNORE INTO namespaces` before selecting, so resolving
// a name created it. That is the create-by-lookup that made /v1/auth/challenge
// a namespace-creation endpoint; creating one is POST /v1/namespaces and
// nothing else.
func TestResolveNamespaceID_doesNotCreate(t *testing.T) {
	svc, _, registry := twoDatabaseService(t)
	before := rowsIn(t, registry, "namespaces")

	_, err := svc.ResolveNamespaceID(context.Background(), "not-created-yet")
	var missing *ErrNoSuchNamespace
	if !errors.As(err, &missing) {
		t.Fatalf("resolving an unknown namespace returned %v, want ErrNoSuchNamespace", err)
	}
	if missing.Namespace != "not-created-yet" {
		t.Errorf("the error names %q", missing.Namespace)
	}
	if after := rowsIn(t, registry, "namespaces"); after != before {
		t.Errorf("resolving a name created it: %d namespaces, was %d", after, before)
	}
}

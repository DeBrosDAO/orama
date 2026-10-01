package serverless

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// newVersionedRegistry is a Registry over the real migrated schema.
func newVersionedRegistry(t *testing.T) (*Registry, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewRegistry(rqlite.NewClient(db), NewMockIPFSClient(), RegistryConfig{}, zap.NewNop()), db
}

func deployN(t *testing.T, r *Registry, name string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := r.Register(context.Background(), &FunctionDefinition{Name: name, Namespace: "ns"}, []byte("wasm")); err != nil {
			t.Fatalf("deploy %d: %v", i+1, err)
		}
	}
}

func TestRegister_keepsEveryVersion(t *testing.T) {
	r, _ := newVersionedRegistry(t)
	ctx := context.Background()
	deployN(t, r, "fn", 2)

	versions, err := r.ListVersions(ctx, "ns", "fn")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("want versions [2 1] kept, got %d rows", len(versions))
	}
	if versions[0].ID == versions[1].ID {
		t.Error("two versions share an id; the second deploy overwrote the first")
	}
	if got, err := r.Get(ctx, "ns", "fn", 1); err != nil || got.Version != 1 {
		t.Errorf("Get fn@1 after deploying v2 = %v, %v; want version 1", got, err)
	}
	if got, err := r.Get(ctx, "ns", "fn", 2); err != nil || got.Version != 2 {
		t.Errorf("Get fn@2 = %v, %v; want version 2", got, err)
	}
}

func TestRegister_getWithoutVersionIsTheLatest(t *testing.T) {
	r, _ := newVersionedRegistry(t)
	deployN(t, r, "fn", 3)

	got, err := r.Get(context.Background(), "ns", "fn", 0)
	if err != nil || got.Version != 3 {
		t.Fatalf("Get latest = %v, %v; want version 3", got, err)
	}
	listed, err := r.List(context.Background(), "ns")
	if err != nil || len(listed) != 1 || listed[0].Version != 3 {
		t.Fatalf("List must show each function once at its latest version, got %d rows, err %v", len(listed), err)
	}
}

func TestGet_unknownVersionIsVersionNotFound(t *testing.T) {
	r, _ := newVersionedRegistry(t)
	deployN(t, r, "fn", 1)
	if _, err := r.Get(context.Background(), "ns", "fn", 9); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("Get fn@9 = %v; want ErrVersionNotFound", err)
	}
}

func TestDelete_removesEveryVersion(t *testing.T) {
	r, _ := newVersionedRegistry(t)
	ctx := context.Background()
	deployN(t, r, "fn", 2)

	if err := r.Delete(ctx, "ns", "fn", 0); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.Get(ctx, "ns", "fn", 0); !errors.Is(err, ErrFunctionNotFound) {
		t.Errorf("Get latest after delete = %v; want ErrFunctionNotFound", err)
	}
	for v := 1; v <= 2; v++ {
		if _, err := r.Get(ctx, "ns", "fn", v); !errors.Is(err, ErrVersionNotFound) {
			t.Errorf("Get fn@%d after delete = %v; want ErrVersionNotFound", v, err)
		}
	}
}

func TestRegister_afterDeleteContinuesTheNumbering(t *testing.T) {
	r, _ := newVersionedRegistry(t)
	deployN(t, r, "fn", 1)
	if err := r.Delete(context.Background(), "ns", "fn", 0); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	deployN(t, r, "fn", 1)
	got, err := r.Get(context.Background(), "ns", "fn", 0)
	if err != nil || got.Version != 2 {
		t.Fatalf("redeploy after delete = %v, %v; want version 2 active", got, err)
	}
}

func TestRegister_triggersFollowTheLatestVersion(t *testing.T) {
	r, db := newVersionedRegistry(t)
	ctx := context.Background()
	deployN(t, r, "fn", 1)
	v1, err := r.Get(ctx, "ns", "fn", 1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO function_pubsub_triggers (id, function_id, topic, topic_pattern, enabled) VALUES ('t1', ?, 'a', 'a', 1)`, v1.ID); err != nil {
		t.Fatalf("seed trigger: %v", err)
	}
	deployN(t, r, "fn", 1)
	v2, err := r.Get(ctx, "ns", "fn", 2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	var owner string
	if err := db.QueryRow(`SELECT function_id FROM function_pubsub_triggers WHERE id = 't1'`).Scan(&owner); err != nil {
		t.Fatalf("read trigger: %v", err)
	}
	if owner != v2.ID {
		t.Errorf("trigger owned by %s after v2 deploy; want the latest version %s", owner, v2.ID)
	}
}

func TestRegister_prunesPastTheRetentionBound(t *testing.T) {
	r, db := newVersionedRegistry(t)
	ctx := context.Background()
	deployN(t, r, "fn", 1)
	v1, err := r.Get(ctx, "ns", "fn", 1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO function_invocations (id, function_id, request_id, trigger_type, status, started_at) VALUES ('i1', ?, 'r', 'http', 'success', CURRENT_TIMESTAMP)`, v1.ID); err != nil {
		t.Fatalf("seed invocation: %v", err)
	}
	deployN(t, r, "fn", MaxRetainedFunctionVersions)

	versions, err := r.ListVersions(ctx, "ns", "fn")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != MaxRetainedFunctionVersions {
		t.Fatalf("kept %d versions; want %d", len(versions), MaxRetainedFunctionVersions)
	}
	if oldest := versions[len(versions)-1].Version; oldest != 2 {
		t.Errorf("oldest kept version = %d; want 2 (version 1 pruned)", oldest)
	}
	if _, err := r.Get(ctx, "ns", "fn", 1); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("pruned fn@1 = %v; want ErrVersionNotFound", err)
	}
	inv, err := r.GetInvocations(ctx, "ns", "fn", 10)
	if err != nil || len(inv) != 1 {
		t.Errorf("the pruned version's invocation history must stay readable by name: %d rows, err %v", len(inv), err)
	}
}

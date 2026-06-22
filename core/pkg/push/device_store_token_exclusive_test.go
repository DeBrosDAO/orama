package push

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

const testClusterSecret = "test-cluster-secret-for-push-device-fingerprints-0123456789"

// newDeviceTestStore builds an RqliteDeviceStore over an in-memory sqlite3 DB
// (via rqlite.NewClient, which passes straight through to *sql.DB) with a schema
// mirroring migrations 023 + 033. This exercises the real Upsert/eviction SQL —
// UNIQUE constraints, ON CONFLICT, and the rowid-ordered DELETE — not a stub.
func newDeviceTestStore(t *testing.T) (*RqliteDeviceStore, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE push_devices (
			id              TEXT PRIMARY KEY,
			namespace       TEXT NOT NULL,
			user_id         TEXT NOT NULL,
			device_id       TEXT NOT NULL,
			provider        TEXT NOT NULL,
			token_encrypted TEXT NOT NULL,
			platform        TEXT,
			app_version     TEXT,
			created_at      INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL,
			last_seen       INTEGER,
			token_fp        TEXT,
			UNIQUE(namespace, user_id, device_id)
		)`,
		`CREATE INDEX idx_push_devices_token_fp ON push_devices(namespace, token_fp)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}

	store, err := NewRqliteDeviceStore(rqlite.NewClient(db), testClusterSecret, zap.NewNop())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store, db
}

func mustUpsert(t *testing.T, s *RqliteDeviceStore, dev PushDevice) string {
	t.Helper()
	id, err := s.Upsert(context.Background(), dev)
	if err != nil {
		t.Fatalf("upsert(%s/%s): %v", dev.UserID, dev.DeviceID, err)
	}
	if id == "" {
		t.Fatalf("upsert(%s/%s) returned empty id", dev.UserID, dev.DeviceID)
	}
	return id
}

func mustList(t *testing.T, s *RqliteDeviceStore, ns, user string) []PushDevice {
	t.Helper()
	devs, err := s.ListForUser(context.Background(), ns, user)
	if err != nil {
		t.Fatalf("list(%s/%s): %v", ns, user, err)
	}
	return devs
}

func countRows(t *testing.T, db *sql.DB, where string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM push_devices WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestUpsert_returnsPersistedID_conflictPreservesID covers bugboard #981 ask 2:
// register returns the row id, and re-registering the same (ns,user,device)
// preserves that id (so a CONFLICT update returns the existing id, not a fresh
// uuid the row never got).
func TestUpsert_returnsPersistedID_conflictPreservesID(t *testing.T) {
	s, _ := newDeviceTestStore(t)
	id1 := mustUpsert(t, s, PushDevice{Namespace: "ns", UserID: "u", DeviceID: "d", Provider: "ntfy", Token: "tok-1"})
	id2 := mustUpsert(t, s, PushDevice{Namespace: "ns", UserID: "u", DeviceID: "d", Provider: "ntfy", Token: "tok-2"})
	if id1 != id2 {
		t.Fatalf("conflict upsert changed the id: %q -> %q", id1, id2)
	}
}

// TestUpsert_tokenExclusive_evictsOlderOwner is the core #981 fix: when a second
// account on the same physical device registers the SAME token, the prior
// owner's row is evicted so one token maps to one active owner. The two
// registrations happen within the same second, which is exactly why eviction
// must order by rowid (insertion order) and not updated_at (unix seconds).
func TestUpsert_tokenExclusive_evictsOlderOwner(t *testing.T) {
	s, db := newDeviceTestStore(t)
	const token = "apns-physical-token-XYZ"

	idA := mustUpsert(t, s, PushDevice{Namespace: "ns", UserID: "accountA", DeviceID: "devA", Provider: "apns", Token: token})
	idB := mustUpsert(t, s, PushDevice{Namespace: "ns", UserID: "accountB", DeviceID: "devB", Provider: "apns", Token: token})
	if idA == idB {
		t.Fatal("expected distinct rows for distinct (user,device) pairs")
	}

	if got := len(mustList(t, s, "ns", "accountA")); got != 0 {
		t.Fatalf("accountA still has %d device(s); expected the older owner to be evicted", got)
	}
	if got := len(mustList(t, s, "ns", "accountB")); got != 1 {
		t.Fatalf("accountB has %d device(s); expected exactly 1", got)
	}
	fp := s.tokenFingerprint(token)
	if n := countRows(t, db, "namespace=? AND token_fp=?", "ns", fp); n != 1 {
		t.Fatalf("expected exactly 1 row carrying the token fingerprint, got %d", n)
	}
}

// TestUpsert_tokenExclusive_switchBackReclaims verifies the active account
// always owns the device token: A → B → A leaves exactly A owning it (the
// re-insert gets a fresh, higher rowid and wins).
func TestUpsert_tokenExclusive_switchBackReclaims(t *testing.T) {
	s, db := newDeviceTestStore(t)
	const token = "voip-physical-token-ABC"

	mustUpsert(t, s, PushDevice{Namespace: "ns", UserID: "A", DeviceID: "dA", Provider: "apns_voip", Token: token})
	mustUpsert(t, s, PushDevice{Namespace: "ns", UserID: "B", DeviceID: "dB", Provider: "apns_voip", Token: token})
	mustUpsert(t, s, PushDevice{Namespace: "ns", UserID: "A", DeviceID: "dA", Provider: "apns_voip", Token: token})

	if got := len(mustList(t, s, "ns", "A")); got != 1 {
		t.Fatalf("A should own the token after switching back, got %d row(s)", got)
	}
	if got := len(mustList(t, s, "ns", "B")); got != 0 {
		t.Fatalf("B should have been evicted, got %d row(s)", got)
	}
	fp := s.tokenFingerprint(token)
	if n := countRows(t, db, "namespace=? AND token_fp=?", "ns", fp); n != 1 {
		t.Fatalf("expected exactly 1 row for the token, got %d", n)
	}
}

// TestUpsert_tokenExclusive_namespaceScoped ensures the eviction never crosses
// tenants: the same physical device used with two namespaces (two apps) keeps an
// independent registration per namespace.
func TestUpsert_tokenExclusive_namespaceScoped(t *testing.T) {
	s, _ := newDeviceTestStore(t)
	const token = "shared-physical-token"

	mustUpsert(t, s, PushDevice{Namespace: "ns1", UserID: "u", DeviceID: "d", Provider: "apns", Token: token})
	mustUpsert(t, s, PushDevice{Namespace: "ns2", UserID: "u", DeviceID: "d", Provider: "apns", Token: token})

	if got := len(mustList(t, s, "ns1", "u")); got != 1 {
		t.Fatalf("ns1 lost its row to a cross-namespace eviction (got %d)", got)
	}
	if got := len(mustList(t, s, "ns2", "u")); got != 1 {
		t.Fatalf("ns2 row missing (got %d)", got)
	}
}

// TestUpsert_alertAndVoipCoexist confirms the iOS alert + VoIP tokens (distinct
// physical tokens registered under the same user) do NOT evict each other.
func TestUpsert_alertAndVoipCoexist(t *testing.T) {
	s, _ := newDeviceTestStore(t)
	mustUpsert(t, s, PushDevice{Namespace: "ns", UserID: "u", DeviceID: "iphone", Provider: "apns", Token: "alert-token"})
	mustUpsert(t, s, PushDevice{Namespace: "ns", UserID: "u", DeviceID: "iphone:voip", Provider: "apns_voip", Token: "voip-token"})

	if got := len(mustList(t, s, "ns", "u")); got != 2 {
		t.Fatalf("expected 2 coexisting rows (alert + voip), got %d", got)
	}
}

func TestUpsert_validation(t *testing.T) {
	s, _ := newDeviceTestStore(t)
	cases := []struct {
		name string
		dev  PushDevice
	}{
		{"empty token", PushDevice{Namespace: "ns", UserID: "u", DeviceID: "d", Provider: "ntfy", Token: ""}},
		{"empty namespace", PushDevice{Namespace: "", UserID: "u", DeviceID: "d", Provider: "ntfy", Token: "t"}},
		{"empty user", PushDevice{Namespace: "ns", UserID: "", DeviceID: "d", Provider: "ntfy", Token: "t"}},
		{"empty device", PushDevice{Namespace: "ns", UserID: "u", DeviceID: "", Provider: "ntfy", Token: "t"}},
		{"empty provider", PushDevice{Namespace: "ns", UserID: "u", DeviceID: "d", Provider: "", Token: "t"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.Upsert(context.Background(), c.dev); err == nil {
				t.Fatalf("expected error for %s", c.name)
			}
		})
	}
}

// TestBackfillTokenFP covers pre-#981 rows (NULL token_fp): the backfill computes
// the fingerprint from the decrypted token, and a later registration of the same
// physical token then evicts the backfilled legacy row.
func TestBackfillTokenFP(t *testing.T) {
	s, db := newDeviceTestStore(t)

	// Simulate a row written before the migration: encrypted with the store's
	// key (so backfill can decrypt) but with a NULL token_fp.
	enc, err := secrets.Encrypt("legacy-token", s.encKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO push_devices (id, namespace, user_id, device_id, provider, token_encrypted, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		"legacy-id", "ns", "old", "d", "apns", enc, 1, 1,
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	n, err := s.BackfillTokenFP(context.Background())
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 backfilled row, got %d", n)
	}
	var fp string
	if err := db.QueryRow(`SELECT token_fp FROM push_devices WHERE id='legacy-id'`).Scan(&fp); err != nil {
		t.Fatalf("read token_fp: %v", err)
	}
	if want := s.tokenFingerprint("legacy-token"); fp != want {
		t.Fatalf("backfilled token_fp = %q, want %q", fp, want)
	}

	// A re-running backfill is a no-op (idempotent).
	if n2, err := s.BackfillTokenFP(context.Background()); err != nil || n2 != 0 {
		t.Fatalf("second backfill: n=%d err=%v (want 0, nil)", n2, err)
	}

	// A new owner registering the same physical token now evicts the legacy row.
	mustUpsert(t, s, PushDevice{Namespace: "ns", UserID: "new", DeviceID: "d2", Provider: "apns", Token: "legacy-token"})
	if countRows(t, db, "id=?", "legacy-id") != 0 {
		t.Fatal("legacy row should have been evicted after backfill + re-register")
	}
}

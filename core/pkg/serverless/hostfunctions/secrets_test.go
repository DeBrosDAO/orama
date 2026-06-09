package hostfunctions

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// fakeSecretsDB is an in-memory rqlite.Client stub that implements only the
// Exec/Query paths used by DBSecretsManager (INSERT...ON CONFLICT upsert and
// SELECT by namespace+name). Storing the encrypted blob in a map lets us
// round-trip a Set through a Get — the core of the bugboard #837 regression.
type fakeSecretsDB struct {
	rqlite.Client
	store map[string][]byte // key: namespace\x00name -> encrypted_value
}

func newFakeSecretsDB() *fakeSecretsDB {
	return &fakeSecretsDB{store: map[string][]byte{}}
}

func storeKey(namespace, name string) string {
	return namespace + "\x00" + name
}

// Exec handles the upsert. args order matches secrets.go Set():
// (id, namespace, name, encrypted_value, created_at, updated_at).
func (f *fakeSecretsDB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, "INSERT INTO function_secrets") {
		namespace, _ := args[1].(string)
		name, _ := args[2].(string)
		enc, _ := args[3].([]byte)
		cp := make([]byte, len(enc))
		copy(cp, enc)
		f.store[storeKey(namespace, name)] = cp
		return fakeResult{rows: 1}, nil
	}
	return fakeResult{}, nil
}

// Query handles the SELECT encrypted_value ... WHERE namespace=? AND name=?.
func (f *fakeSecretsDB) Query(ctx context.Context, dest any, query string, args ...any) error {
	if !strings.Contains(query, "SELECT encrypted_value") {
		return errors.New("unexpected query")
	}
	namespace, _ := args[0].(string)
	name, _ := args[1].(string)
	rows, ok := dest.(*[]struct {
		EncryptedValue []byte `db:"encrypted_value"`
	})
	if !ok {
		return errors.New("unexpected dest type")
	}
	if enc, found := f.store[storeKey(namespace, name)]; found {
		*rows = append(*rows, struct {
			EncryptedValue []byte `db:"encrypted_value"`
		}{EncryptedValue: enc})
	}
	return nil
}

type fakeResult struct{ rows int64 }

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.rows, nil }

// validKey is a 32-byte AES-256 key, hex-encoded (64 chars).
const validKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// otherKey is a different valid 32-byte key.
const otherKey = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

// TestDBSecretsManager_SetGetRoundTrip_sameKey proves the fix: a secret
// encrypted with a fixed key is decryptable by a SEPARATE manager constructed
// with the SAME key (simulating another process / a restart).
func TestDBSecretsManager_SetGetRoundTrip_sameKey(t *testing.T) {
	db := newFakeSecretsDB()
	logger := zap.NewNop()
	ctx := context.Background()

	writer, err := NewDBSecretsManager(db, validKey, false, logger)
	if err != nil {
		t.Fatalf("NewDBSecretsManager (writer) failed: %v", err)
	}
	if err := writer.Set(ctx, "ns1", "API_TOKEN", "s3cr3t-value"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// A fresh manager with the SAME key (different process / post-restart).
	reader, err := NewDBSecretsManager(db, validKey, false, logger)
	if err != nil {
		t.Fatalf("NewDBSecretsManager (reader) failed: %v", err)
	}
	got, err := reader.Get(ctx, "ns1", "API_TOKEN")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != "s3cr3t-value" {
		t.Errorf("Get returned %q, want %q", got, "s3cr3t-value")
	}
}

// TestDBSecretsManager_GetWithDifferentKey_fails proves the bug it guards
// against: a manager with a DIFFERENT key cannot decrypt — exactly what
// happened when each process generated its own ephemeral key (bugboard #837).
func TestDBSecretsManager_GetWithDifferentKey_fails(t *testing.T) {
	db := newFakeSecretsDB()
	logger := zap.NewNop()
	ctx := context.Background()

	writer, err := NewDBSecretsManager(db, validKey, false, logger)
	if err != nil {
		t.Fatalf("NewDBSecretsManager (writer) failed: %v", err)
	}
	if err := writer.Set(ctx, "ns1", "API_TOKEN", "s3cr3t-value"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	reader, err := NewDBSecretsManager(db, otherKey, false, logger)
	if err != nil {
		t.Fatalf("NewDBSecretsManager (reader) failed: %v", err)
	}
	if _, err := reader.Get(ctx, "ns1", "API_TOKEN"); err == nil {
		t.Fatal("expected decryption to fail with a different key, got nil error")
	}
}

// TestDBSecretsManager_emptyKey_isLoud verifies the production constructor
// refuses to start with an empty key (allowEphemeral=false) instead of
// silently generating an undecryptable ephemeral key.
func TestDBSecretsManager_emptyKey_isLoud(t *testing.T) {
	db := newFakeSecretsDB()
	_, err := NewDBSecretsManager(db, "", false, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for empty key with allowEphemeral=false, got nil")
	}
	if !strings.Contains(err.Error(), "secrets encryption key is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestDBSecretsManager_emptyKey_ephemeralAllowed verifies tests/dev can still
// opt into a per-process ephemeral key.
func TestDBSecretsManager_emptyKey_ephemeralAllowed(t *testing.T) {
	db := newFakeSecretsDB()
	mgr, err := NewDBSecretsManager(db, "", true, zap.NewNop())
	if err != nil {
		t.Fatalf("expected ephemeral key to be allowed, got error: %v", err)
	}
	// Ephemeral key still round-trips within the same process.
	ctx := context.Background()
	if err := mgr.Set(ctx, "ns1", "K", "v"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	got, err := mgr.Get(ctx, "ns1", "K")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != "v" {
		t.Errorf("Get returned %q, want %q", got, "v")
	}
}

// TestDBSecretsManager_invalidKey_rejected covers malformed keys (wrong
// length, non-hex) at the boundary.
func TestDBSecretsManager_invalidKey_rejected(t *testing.T) {
	db := newFakeSecretsDB()
	cases := map[string]string{
		"too short":   "abcd",
		"odd hex":     "abc",
		"not hex":     strings.Repeat("zz", 32),
		"wrong bytes": "0123456789abcdef", // 8 bytes, not 32
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewDBSecretsManager(db, key, false, zap.NewNop()); err == nil {
				t.Fatalf("expected error for invalid key %q, got nil", key)
			}
		})
	}
}

// TestDBSecretsManager_Get_notFound verifies the not-found sentinel survives.
func TestDBSecretsManager_Get_notFound(t *testing.T) {
	db := newFakeSecretsDB()
	mgr, err := NewDBSecretsManager(db, validKey, false, zap.NewNop())
	if err != nil {
		t.Fatalf("NewDBSecretsManager failed: %v", err)
	}
	if _, err := mgr.Get(context.Background(), "ns1", "missing"); !errors.Is(err, serverless.ErrSecretNotFound) {
		t.Errorf("expected ErrSecretNotFound, got %v", err)
	}
}

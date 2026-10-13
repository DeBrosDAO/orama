package secrets

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
)

func TestLoadOrMaterialize_copiesClusterSecretAndDoesNotRegenerate(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	const cs = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	r, err := LoadOrMaterialize(context.Background(), nil, secretsDir, "", cs)
	if err != nil {
		t.Fatal(err)
	}
	if r.CurrentIKM != cs || r.CurrentID != FirstID {
		t.Fatalf("got %+v", r)
	}

	// A second load reads the file, not a new value.
	r2, err := LoadOrMaterialize(context.Background(), nil, secretsDir, "", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	if r2.CurrentIKM != cs {
		t.Fatalf("regenerated or ignored the file: %q", r2.CurrentIKM)
	}
}

func TestLoadOrMaterialize_emptyFileIsFatal(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretsDir, FileName), []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrMaterialize(context.Background(), nil, secretsDir, "", "cluster"); err == nil {
		t.Fatal("empty file was replaced")
	}
}

func TestRotate_newIKMCannotDeriveFromOld(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	const cs = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	cur, err := LoadOrMaterialize(context.Background(), nil, secretsDir, "", cs)
	if err != nil {
		t.Fatal(err)
	}
	next, err := Rotate(context.Background(), nil, secretsDir, cur)
	if err != nil {
		t.Fatal(err)
	}
	if next.CurrentIKM == cur.CurrentIKM {
		t.Fatal("rotate reused the IKM")
	}
	if next.PreviousIKM != cur.CurrentIKM {
		t.Fatal("previous was not the old current")
	}
	if next.CurrentID != "2" || next.PreviousID != "1" {
		t.Fatalf("ids %+v", next)
	}

	oldKS, _ := cur.Keyset("p")
	newKS, _ := next.Keyset("p")
	ct, err := newKS.Encrypt("after")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldKS.Decrypt(ct); err == nil {
		t.Fatal("pre-rotate key opened post-rotate ciphertext")
	}
}

func TestLoadOrMaterialize_registryWins(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE encryption_roots (
		slot TEXT PRIMARY KEY, key_id TEXT NOT NULL, ikm TEXT NOT NULL,
		write_versioned INTEGER NOT NULL DEFAULT 0, updated_at TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	store := rqlite.NewClient(db)
	if _, err := store.Exec(context.Background(),
		`INSERT INTO encryption_roots (slot, key_id, ikm, write_versioned) VALUES ('current', '7', 'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd', 1)`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r, err := LoadOrMaterialize(context.Background(), store, filepath.Join(dir, "secrets"), "", "cluster-secret-should-not-win")
	if err != nil {
		t.Fatal(err)
	}
	if r.CurrentID != "7" || r.CurrentIKM[0] != 'd' || !r.WriteVersioned {
		t.Fatalf("got %+v", r)
	}
}

// writeRoot puts a root file in dir, the way install seeds <oramaDir>/secrets.
func writeRoot(t *testing.T, dir, id, ikm string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(ikm), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileIDName), []byte(id), 0600); err != nil {
		t.Fatal(err)
	}
}

// A node that joined a rotated cluster was handed the current root by install.
// A gateway with an empty cache and no registry must use it, not mistake the
// cluster for a fresh one and copy the cluster secret as generation 1.
func TestLoadOrMaterialize_readsTheNodeSeedAndCachesIt(t *testing.T) {
	root := t.TempDir()
	cache, seed := filepath.Join(root, "gateway"), filepath.Join(root, "secrets")
	const rotated = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	writeRoot(t, seed, "3", rotated)

	r, err := LoadOrMaterialize(context.Background(), nil, cache, seed, "cluster-secret-must-not-win")
	if err != nil {
		t.Fatal(err)
	}
	if r.CurrentIKM != rotated || r.CurrentID != "3" {
		t.Fatalf("got %+v, want the seeded generation 3", r)
	}
	cached, err := loadFromFiles(cache)
	if err != nil || cached.CurrentIKM != rotated {
		t.Fatalf("the seed was not cached in the gateway's own directory: %+v, %v", cached, err)
	}
}

// The gateway's own cache is newer than the seed install wrote at join time:
// a rotate since then updated the cache, never the (read-only) seed.
func TestLoadOrMaterialize_cacheWinsOverTheSeed(t *testing.T) {
	root := t.TempDir()
	cache, seed := filepath.Join(root, "gateway"), filepath.Join(root, "secrets")
	writeRoot(t, seed, "1", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	writeRoot(t, cache, "2", "1111111111111111111111111111111111111111111111111111111111111111")

	r, err := LoadOrMaterialize(context.Background(), nil, cache, seed, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.CurrentID != "2" {
		t.Fatalf("got generation %s, want the cached 2", r.CurrentID)
	}
}

// The cache write used to be discarded. A gateway that could not write it
// booted fine and had nothing to decrypt with the next time the registry was
// unreachable.
func TestLoadOrMaterialize_unwritableCacheIsAnError(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(blocker, "gateway")

	if _, err := LoadOrMaterialize(context.Background(), nil, cache, "", "cluster"); err == nil {
		t.Fatal("materialised a root it could not persist")
	}

	seed := filepath.Join(root, "secrets")
	writeRoot(t, seed, "1", "2222222222222222222222222222222222222222222222222222222222222222")
	if _, err := LoadOrMaterialize(context.Background(), nil, cache, seed, ""); err == nil {
		t.Fatal("returned the seed without being able to cache it")
	}
}

func TestLoadOrMaterialize_nothingAnywhereIsAnError(t *testing.T) {
	root := t.TempDir()
	_, err := LoadOrMaterialize(context.Background(), nil, filepath.Join(root, "gateway"), filepath.Join(root, "secrets"), "")
	if err == nil {
		t.Fatal("invented an encryption root out of nothing")
	}
}

// Persisting nowhere is not persisting. An empty directory used to return nil.
func TestPersist_emptyDirIsAnError(t *testing.T) {
	if err := Persist("", Root{CurrentID: FirstID, CurrentIKM: "x"}); err == nil {
		t.Fatal("Persist reported success without writing anything")
	}
}

// The registry's root used to be cached with its error discarded, so a gateway
// that could not write its cache found out only when the registry was
// unreachable at a later boot and it had nothing to decrypt with.
func TestLoadOrMaterialize_registryRootThatCannotBeCachedIsAnError(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE encryption_roots (
		slot TEXT PRIMARY KEY, key_id TEXT NOT NULL, ikm TEXT NOT NULL,
		write_versioned INTEGER NOT NULL DEFAULT 0, updated_at TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO encryption_roots (slot, key_id, ikm) VALUES ('current', '4', '3333333333333333333333333333333333333333333333333333333333333333')`); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOrMaterialize(context.Background(), rqlite.NewClient(db), filepath.Join(blocker, "gateway"), "", ""); err == nil {
		t.Fatal("returned the registry's root without being able to cache it")
	}
}

// failingStore is a registry that cannot be reached.
type failingStore struct{ err error }

func (f failingStore) Query(context.Context, any, string, ...any) error { return f.err }
func (f failingStore) Exec(context.Context, string, ...any) (sql.Result, error) {
	return nil, f.err
}

// A gateway booting while the registry is unreachable used to take the root
// from its cache or the node seed — which may predate a rotation — and encrypt
// with it. Only "nothing recorded yet" lets the files decide.
func TestLoadOrMaterialize_unreachableRegistryIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeRoot(t, filepath.Join(dir, "seed"), "1", strings.Repeat("a", 64))
	store := failingStore{err: errors.New("dial tcp 10.0.0.1:10100: connection refused")}

	_, err := LoadOrMaterialize(context.Background(), store, filepath.Join(dir, "cache"), filepath.Join(dir, "seed"), "cs")
	if err == nil {
		t.Fatal("a root was loaded from disk while the registry could not be asked")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("the error does not name the registry: %v", err)
	}
}

func TestLoadOrMaterialize_registryWithoutTheTableUsesTheFiles(t *testing.T) {
	dir := t.TempDir()
	writeRoot(t, filepath.Join(dir, "seed"), "3", strings.Repeat("b", 64))
	store := failingStore{err: errors.New("no such table: encryption_roots")}

	r, err := LoadOrMaterialize(context.Background(), store, filepath.Join(dir, "cache"), filepath.Join(dir, "seed"), "cs")
	if err != nil {
		t.Fatalf("a cluster with no roots table yet could not boot: %v", err)
	}
	if r.CurrentID != "3" {
		t.Errorf("CurrentID = %q, want the seed's", r.CurrentID)
	}
}

package secrets

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
)

const boundPurpose = "orama-deployment-environment-v1"

func boundKeyset(t *testing.T, root Root) Keyset {
	t.Helper()
	ks, err := root.Keyset(boundPurpose)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func TestBoundEnvelope_opensOnlyForTheDataItWasSealedTo(t *testing.T) {
	ks := boundKeyset(t, Root{CurrentID: "1", CurrentIKM: "ikm", WriteVersioned: true, WriteBound: true})
	aad := BoundAAD(boundPurpose, "acme", "dep-1")

	sealed, err := ks.EncryptBound("secret", aad)
	if err != nil {
		t.Fatal(err)
	}
	env, err := ParseEnvelope(sealed)
	if err != nil || env.Version != 2 || env.KeyID != "1" {
		t.Fatalf("envelope %+v, %v; want version 2 key 1", env, err)
	}
	if got, err := ks.DecryptBound(sealed, aad); err != nil || got != "secret" {
		t.Fatalf("DecryptBound = %q, %v", got, err)
	}

	for name, other := range map[string][]byte{
		"another deployment": BoundAAD(boundPurpose, "acme", "dep-2"),
		"another namespace":  BoundAAD(boundPurpose, "mallory", "dep-1"),
		"another purpose":    BoundAAD("other-purpose", "acme", "dep-1"),
	} {
		if _, err := ks.DecryptBound(sealed, other); err == nil {
			t.Errorf("a ciphertext moved to %s still opened", name)
		}
	}
	if _, err := ks.DecryptBound(sealed, nil); err == nil {
		t.Error("a bound ciphertext opened with no additional data")
	}
	if _, err := ks.Decrypt(sealed); err == nil {
		t.Error("the unbound Decrypt opened a bound ciphertext")
	}
	if _, err := Decrypt(sealed, ks.Current); err == nil {
		t.Error("the package-level Decrypt opened a bound ciphertext")
	}
}

// A rolling upgrade: until the operator enables bound writes, a new node must
// write what an old node reads.
func TestKeyset_encryptBoundWritesTheOldFormatsUntilBoundWritesAreEnabled(t *testing.T) {
	aad := BoundAAD(boundPurpose, "acme", "dep-1")
	for name, tc := range map[string]struct {
		root    Root
		version int
	}{
		"legacy":    {Root{CurrentID: "1", CurrentIKM: "ikm"}, 0},
		"versioned": {Root{CurrentID: "1", CurrentIKM: "ikm", WriteVersioned: true}, 1},
		"bound":     {Root{CurrentID: "1", CurrentIKM: "ikm", WriteVersioned: true, WriteBound: true}, 2},
	} {
		sealed, err := boundKeyset(t, tc.root).EncryptBound("secret", aad)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		env, err := ParseEnvelope(sealed)
		if err != nil || env.Version != tc.version {
			t.Errorf("%s: envelope %+v, %v; want version %d", name, env, err, tc.version)
		}
		// The old formats are what a node without this change can open.
		if tc.version < 2 {
			if got, err := Decrypt(sealed, boundKeyset(t, tc.root).Current); err != nil || got != "secret" {
				t.Errorf("%s: an unbound reader got %q, %v", name, got, err)
			}
		}
	}
}

// Rows sealed before the binding existed keep opening, whatever the data.
func TestKeyset_decryptBoundReadsRowsSealedWithoutABinding(t *testing.T) {
	ks := boundKeyset(t, Root{CurrentID: "1", CurrentIKM: "ikm", WriteVersioned: true, WriteBound: true})
	aad := BoundAAD(boundPurpose, "acme", "dep-1")

	legacy, err := Encrypt("old", ks.Current)
	if err != nil {
		t.Fatal(err)
	}
	versioned, err := EncryptVersioned("mid", "1", ks.Current)
	if err != nil {
		t.Fatal(err)
	}
	for stored, want := range map[string]string{legacy: "old", versioned: "mid"} {
		if got, err := ks.DecryptBound(stored, aad); err != nil || got != want {
			t.Errorf("DecryptBound(%.12s...) = %q, %v; want %q", stored, got, err, want)
		}
	}
}

func TestEncryptBound_refusesWhatItCannotBind(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	if _, err := EncryptBound("x", "1", key, nil); err == nil {
		t.Error("sealed with no additional data")
	}
	if _, err := EncryptBound("x", "", key, []byte("a")); err == nil {
		t.Error("sealed with an empty key id")
	}
	if _, err := EncryptBound("x", "1:2", key, []byte("a")); err == nil {
		t.Error("sealed with a key id containing a separator")
	}
}

func TestBoundAAD_isUnambiguousAndRefusesMissingParts(t *testing.T) {
	if string(BoundAAD("p", "ab", "c")) == string(BoundAAD("p", "a", "bc")) {
		t.Error("two different rows produce the same additional data")
	}
	if BoundAAD("p") != nil || BoundAAD("p", "a", "") != nil || BoundAAD("p", "", "b") != nil {
		t.Error("additional data was built from a missing part")
	}
}

func TestWriteLevel_survivesTheRegistryAndOldReadersSeeVersioned(t *testing.T) {
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
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "secrets")

	root := Root{CurrentID: "1", CurrentIKM: strings.Repeat("a", 64)}
	enabled, err := EnableVersionedWrites(ctx, store, dir, root)
	if err != nil || !enabled.WriteBound {
		t.Fatalf("EnableVersionedWrites = %+v, %v; want bound writes on", enabled, err)
	}

	var raw []struct {
		Level int `db:"write_versioned"`
	}
	if err := store.Query(ctx, &raw, `SELECT write_versioned FROM encryption_roots WHERE slot = 'current'`); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || raw[0].Level != 2 {
		t.Fatalf("stored level %+v, want 2: an old gateway reads any non-zero value as versioned", raw)
	}

	loaded, err := LoadOrMaterialize(ctx, store, dir, "", "")
	if err != nil || !loaded.WriteVersioned || !loaded.WriteBound {
		t.Fatalf("loaded %+v, %v; want versioned and bound", loaded, err)
	}

	// A registry written by a gateway from before bound writes holds 1.
	if _, err := store.Exec(ctx, `UPDATE encryption_roots SET write_versioned = 1`); err != nil {
		t.Fatal(err)
	}
	old, err := LoadOrMaterialize(ctx, store, dir, "", "")
	if err != nil || !old.WriteVersioned || old.WriteBound {
		t.Fatalf("loaded %+v, %v; a versioned-only root must not enable bound writes", old, err)
	}
}

func TestRotate_keepsBoundWrites(t *testing.T) {
	root, err := Rotate(context.Background(), nil, filepath.Join(t.TempDir(), "secrets"),
		Root{CurrentID: "1", CurrentIKM: strings.Repeat("a", 64)})
	if err != nil || !root.WriteBound {
		t.Fatalf("Rotate = %+v, %v; want bound writes on", root, err)
	}
}

func deploymentColumn() Column {
	return Column{Table: "deployments", Column: "environment", IDCols: []string{"id"},
		Purpose: boundPurpose, BoundCols: []string{"namespace", "id"}}
}

func walkStore(t *testing.T) *rqlite.Client {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE deployments (id TEXT PRIMARY KEY, namespace TEXT NOT NULL, environment TEXT)`); err != nil {
		t.Fatal(err)
	}
	c := rqlite.NewClient(db)
	return &c
}

func storedEnvironment(t *testing.T, store rqlite.Client, id string) string {
	t.Helper()
	var rows []struct {
		Environment string `db:"environment"`
	}
	if err := store.Query(context.Background(), &rows, `SELECT environment FROM deployments WHERE id = ?`, id); err != nil || len(rows) != 1 {
		t.Fatalf("read %s: %v %v", id, rows, err)
	}
	return rows[0].Environment
}

func TestWalk_bindsDeploymentEnvironmentsToTheirRows(t *testing.T) {
	store := *walkStore(t)
	ctx := context.Background()
	root := Root{CurrentID: "1", CurrentIKM: "ikm-walk", WriteVersioned: true, WriteBound: true}
	ks := boundKeyset(t, root)

	legacy, _ := Encrypt(`{"A":"1"}`, ks.Current)
	for id, env := range map[string]string{"d1": legacy, "d2": `{"B":"2"}`, "d3": ""} {
		if _, err := store.Exec(ctx, `INSERT INTO deployments (id, namespace, environment) VALUES (?, 'acme', ?)`, id, env); err != nil {
			t.Fatal(err)
		}
	}

	res, err := Walk(ctx, store, root, []Column{deploymentColumn()})
	if err != nil || len(res.Failures) != 0 {
		t.Fatalf("Walk = %+v, %v", res, err)
	}
	if res.Rewrote != 2 {
		t.Fatalf("rewrote %d rows, want the sealed and the plaintext one", res.Rewrote)
	}
	for id, want := range map[string]string{"d1": `{"A":"1"}`, "d2": `{"B":"2"}`} {
		stored := storedEnvironment(t, store, id)
		if env, err := ParseEnvelope(stored); err != nil || env.Version != 2 {
			t.Fatalf("%s: stored %.14s... %v, want an enc:v2: envelope", id, stored, err)
		}
		got, err := ks.DecryptBound(stored, BoundAAD(boundPurpose, "acme", id))
		if err != nil || got != want {
			t.Fatalf("%s: opened as %q, %v; want %q", id, got, err, want)
		}
	}

	again, err := Walk(ctx, store, root, []Column{deploymentColumn()})
	if err != nil || again.Rewrote != 0 {
		t.Fatalf("a second pass = %+v, %v; want no rewrites", again, err)
	}
}

// A ciphertext copied onto another row does not follow its new owner through a
// rotation: the walk reports it instead of re-sealing it to the wrong row.
func TestWalk_reportsACiphertextThatWasMovedToAnotherRow(t *testing.T) {
	store := *walkStore(t)
	ctx := context.Background()
	root := Root{CurrentID: "1", CurrentIKM: "ikm-walk", WriteVersioned: true, WriteBound: true}
	ks := boundKeyset(t, root)

	sealed, err := ks.EncryptBound(`{"SECRET":"x"}`, BoundAAD(boundPurpose, "acme", "d1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Exec(ctx, `INSERT INTO deployments (id, namespace, environment) VALUES ('d2', 'mallory', ?)`, sealed); err != nil {
		t.Fatal(err)
	}

	res, err := Walk(ctx, store, Root{CurrentID: "1", CurrentIKM: "ikm-walk", WriteVersioned: true}, []Column{deploymentColumn()})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failures) != 1 || res.Rewrote != 0 {
		t.Fatalf("Walk = %+v, want one failure and no rewrite", res)
	}
}

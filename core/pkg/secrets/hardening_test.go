package secrets

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// registryStore is a registry with the tables the root and the index columns
// live in, all empty.
func registryStore(t *testing.T) (rqlite.Client, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE encryption_roots (slot TEXT PRIMARY KEY, key_id TEXT NOT NULL, ikm TEXT NOT NULL,
			write_versioned INTEGER NOT NULL DEFAULT 0, updated_at TIMESTAMP)`,
		`CREATE TABLE deployments (id TEXT PRIMARY KEY, namespace TEXT NOT NULL, environment TEXT)`,
		`CREATE TABLE wireguard_peers (node_id TEXT PRIMARY KEY, agent_token TEXT)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	return rqlite.NewClient(db), db
}

func TestCheckSuccessor_aSameGenerationPushCannotLowerTheWriteLevel(t *testing.T) {
	bound := Root{CurrentID: "3", CurrentIKM: "gen-3", WriteVersioned: true, WriteBound: true}
	versioned := Root{CurrentID: "3", CurrentIKM: "gen-3", WriteVersioned: true}
	legacy := Root{CurrentID: "3", CurrentIKM: "gen-3"}
	for _, tc := range []struct {
		name          string
		current, next Root
		wantErr       bool
	}{
		{"bound to versioned", bound, versioned, true},
		{"bound to legacy", bound, legacy, true},
		{"versioned to legacy", versioned, legacy, true},
		{"bound to bound (a retry)", bound, bound, false},
		{"legacy to versioned (enabling)", legacy, versioned, false},
		{"versioned to bound (enabling)", versioned, bound, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckSuccessor(tc.current, tc.next)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckSuccessor err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "write level") {
				t.Fatalf("the refusal does not say why: %v", err)
			}
		})
	}
}

func TestCheckSuccessor_aGapIsTheGapError(t *testing.T) {
	cur := Root{CurrentID: "3", CurrentIKM: "gen-3"}
	err := CheckSuccessor(cur, Root{CurrentID: "5", CurrentIKM: "gen-5"})
	if !errors.Is(err, ErrGenerationGap) {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(CheckSuccessor(cur, Root{CurrentID: "2", CurrentIKM: "gen-2"}), ErrGenerationGap) {
		t.Fatal("a rollback was reported as a gap")
	}
}

// A gateway that missed one fan-out sees every later push one generation too
// far ahead. The registry says what the root is.
func TestResolveSuccessor_aGatewayThatMissedAFanOutAdoptsTheRegistrysRoot(t *testing.T) {
	store, _ := registryStore(t)
	ctx := context.Background()
	gen4 := Root{CurrentID: "4", CurrentIKM: "gen-4", PreviousID: "3", PreviousIKM: "gen-3", WriteVersioned: true, WriteBound: true}
	if err := saveToRegistry(ctx, store, gen4); err != nil {
		t.Fatal(err)
	}
	held := Root{CurrentID: "3", CurrentIKM: "gen-3"}

	// Generation 4 was missed; the push is for 5, and the registry is at 5.
	gen5 := Root{CurrentID: "5", CurrentIKM: "gen-5", PreviousID: "4", PreviousIKM: "gen-4", WriteVersioned: true, WriteBound: true}
	if err := saveToRegistry(ctx, store, gen5); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveSuccessor(ctx, store, held, gen5)
	if err != nil || got.CurrentID != "5" || got.CurrentIKM != "gen-5" {
		t.Fatalf("got %+v, %v", got, err)
	}

	// The registry is further on than the push: it wins.
	gen6 := Root{CurrentID: "6", CurrentIKM: "gen-6", PreviousID: "5", PreviousIKM: "gen-5", WriteVersioned: true, WriteBound: true}
	if err := saveToRegistry(ctx, store, gen6); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveSuccessor(ctx, store, held, gen5)
	if err != nil || got.CurrentID != "6" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestResolveSuccessor_whatTheRegistryCannotConfirmIsRefused(t *testing.T) {
	ctx := context.Background()
	held := Root{CurrentID: "3", CurrentIKM: "gen-3"}
	pushed := Root{CurrentID: "5", CurrentIKM: "gen-5", PreviousID: "4", PreviousIKM: "gen-4"}

	t.Run("no registry", func(t *testing.T) {
		if _, err := ResolveSuccessor(ctx, nil, held, pushed); !errors.Is(err, ErrGenerationGap) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the registry has no root", func(t *testing.T) {
		store, _ := registryStore(t)
		if _, err := ResolveSuccessor(ctx, store, held, pushed); !errors.Is(err, ErrGenerationGap) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the registry is behind the push", func(t *testing.T) {
		store, _ := registryStore(t)
		if err := saveToRegistry(ctx, store, Root{CurrentID: "4", CurrentIKM: "gen-4"}); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveSuccessor(ctx, store, held, pushed); !errors.Is(err, ErrGenerationGap) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the registry holds another key at the pushed generation", func(t *testing.T) {
		store, _ := registryStore(t)
		if err := saveToRegistry(ctx, store, Root{CurrentID: "5", CurrentIKM: "someone-else"}); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveSuccessor(ctx, store, held, pushed); !errors.Is(err, ErrGenerationGap) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an unreachable registry", func(t *testing.T) {
		store := failingStore{err: errors.New("connection refused")}
		if _, err := ResolveSuccessor(ctx, store, held, pushed); !errors.Is(err, ErrGenerationGap) || !strings.Contains(err.Error(), "connection refused") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestResolveSuccessor_aPushThatIsNotAGapIsJudgedAsBefore(t *testing.T) {
	store, _ := registryStore(t)
	ctx := context.Background()
	held := Root{CurrentID: "3", CurrentIKM: "gen-3"}
	next := Root{CurrentID: "4", CurrentIKM: "gen-4", PreviousID: "3", PreviousIKM: "gen-3"}
	got, err := ResolveSuccessor(ctx, store, held, next)
	if err != nil || got.CurrentIKM != "gen-4" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// A rollback is refused, and the registry is not consulted to excuse it.
	if err := saveToRegistry(ctx, store, Root{CurrentID: "2", CurrentIKM: "gen-2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSuccessor(ctx, store, held, Root{CurrentID: "2", CurrentIKM: "gen-2"}); err == nil {
		t.Fatal("a rollback was accepted")
	}
}

// A node of an existing cluster that has no root file when it reaches this
// code (a rolling upgrade) must not start writing envelopes older gateways of
// the same cluster cannot read.
func TestLoadOrMaterialize_aNodeOfAClusterThatStoresThingsStartsAtTheLegacyLevel(t *testing.T) {
	for name, seed := range map[string]string{
		"a deployment":         `INSERT INTO deployments (id, namespace, environment) VALUES ('d1', 'acme', '')`,
		"a sealed deployment":  `INSERT INTO deployments (id, namespace, environment) VALUES ('d1', 'acme', 'enc:v1:1:xyz')`,
		"a node's agent token": `INSERT INTO wireguard_peers (node_id, agent_token) VALUES ('n1', 'enc:abc')`,
	} {
		t.Run(name, func(t *testing.T) {
			store, db := registryStore(t)
			if _, err := db.Exec(seed); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "secrets")
			r, err := LoadOrMaterialize(context.Background(), store, dir, "", "cluster-secret")
			if err != nil {
				t.Fatal(err)
			}
			if r.CurrentID != FirstID || r.CurrentIKM != "cluster-secret" || r.WriteVersioned || r.WriteBound {
				t.Fatalf("root = %+v, want generation 1 at the legacy write level", r)
			}
			again, err := LoadOrMaterialize(context.Background(), failingStore{err: errors.New("no such table: encryption_roots")}, dir, "", "")
			if err != nil || again.WriteVersioned || again.WriteBound {
				t.Fatalf("the cache = %+v, %v; want the legacy level kept", again, err)
			}
		})
	}
}

func TestLoadOrMaterialize_aClusterThatStoresNothingYetStartsWithBoundWrites(t *testing.T) {
	store, db := registryStore(t)
	// Rows that hold no secret do not make a cluster old.
	if _, err := db.Exec(`INSERT INTO wireguard_peers (node_id, agent_token) VALUES ('n1', '')`); err != nil {
		t.Fatal(err)
	}
	r, err := LoadOrMaterialize(context.Background(), store, filepath.Join(t.TempDir(), "secrets"), "", "cluster-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !r.WriteVersioned || !r.WriteBound {
		t.Fatalf("first root = %+v, want versioned and bound writes", r)
	}
}

// A registry without the tables yet (schema apply runs after the secrets
// manager is built) is a registry with nothing in it.
func TestLoadOrMaterialize_aRegistryWithoutTheTablesIsNew(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r, err := LoadOrMaterialize(context.Background(), rqlite.NewClient(db), filepath.Join(t.TempDir(), "secrets"), "", "cluster-secret")
	if err != nil || !r.WriteBound {
		t.Fatalf("root = %+v, %v", r, err)
	}
}

func TestLoadOrMaterialize_aRegistryThatCannotBeAskedIsNotANewCluster(t *testing.T) {
	// The root lookup tolerates a missing table; the emptiness check must not
	// tolerate a broken registry.
	store := &selectiveStore{failOn: "FROM deployments", err: errors.New("connection reset")}
	_, err := LoadOrMaterialize(context.Background(), store, filepath.Join(t.TempDir(), "secrets"), "", "cluster-secret")
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err = %v", err)
	}
}

// selectiveStore has no root recorded and fails the query that contains failOn.
type selectiveStore struct {
	failOn string
	err    error
}

func (s *selectiveStore) Query(_ context.Context, _ any, query string, _ ...any) error {
	if strings.Contains(query, s.failOn) {
		return s.err
	}
	if strings.Contains(query, "encryption_roots") {
		return errors.New("no such table: encryption_roots")
	}
	return nil
}
func (s *selectiveStore) Exec(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("no such table: encryption_roots")
}

func TestSeal_aHolderThatCannotMakeKeysIsAnErrorNotTheStaticKey(t *testing.T) {
	static := []byte("0123456789abcdef0123456789abcdef")
	empty := NewHolder(Root{})
	if _, err := Seal(empty, "p", static, "secret"); err == nil || !strings.Contains(err.Error(), "encryption keys") {
		t.Fatalf("Seal under a holder with no root: %v", err)
	}
	if _, err := SealBound(empty, "p", static, []byte("aad"), "secret"); err == nil || !strings.Contains(err.Error(), "encryption keys") {
		t.Fatalf("SealBound under a holder with no root: %v", err)
	}

	// No holder at all is the static key, as before.
	if sealed, err := Seal(nil, "p", static, "secret"); err != nil || sealed == "" {
		t.Fatalf("Seal with no holder: %q, %v", sealed, err)
	}
	if sealed, err := SealBound(nil, "p", static, []byte("aad"), "secret"); err != nil || sealed == "" {
		t.Fatalf("SealBound with no holder: %q, %v", sealed, err)
	}
	// A working holder seals under its root.
	ok := NewHolder(Root{CurrentID: "1", CurrentIKM: strings.Repeat("a", 64), WriteVersioned: true})
	sealed, err := Seal(ok, "p", static, "secret")
	if err != nil || !strings.HasPrefix(sealed, "enc:v1:1:") {
		t.Fatalf("Seal under a root: %q, %v", sealed, err)
	}
}

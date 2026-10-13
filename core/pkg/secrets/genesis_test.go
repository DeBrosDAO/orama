package secrets

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A cluster's first root is created with bound writes on: there is no older
// gateway to hand an enc:v2: envelope to.
func TestLoadOrMaterialize_aFirstRootStartsWithBoundWrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	r, err := LoadOrMaterialize(context.Background(), nil, dir, "", "cluster-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !r.WriteVersioned || !r.WriteBound || r.CurrentID != FirstID {
		t.Fatalf("first root = %+v, want generation 1 with versioned and bound writes", r)
	}

	// The level survives a restart that cannot reach the registry's row.
	again, err := LoadOrMaterialize(context.Background(), nil, dir, "", "cluster-secret")
	if err != nil || !again.WriteBound {
		t.Fatalf("reloaded from the cache = %+v, %v; want bound writes kept", again, err)
	}
}

// An upgraded cluster has a root already: its files, with no level, are the
// legacy level, and stay there until an operator runs rotate-secrets.
func TestLoadOrMaterialize_anExistingRootKeepsTodaysBehaviour(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	writeRoot(t, dir, "3", strings.Repeat("c", 64))
	if _, err := os.Stat(filepath.Join(dir, FileLevelName)); err == nil {
		t.Fatal("the fixture already has a level file")
	}

	r, err := LoadOrMaterialize(context.Background(), nil, dir, "", "cluster-secret")
	if err != nil {
		t.Fatal(err)
	}
	if r.WriteVersioned || r.WriteBound {
		t.Fatalf("an existing root = %+v, want the legacy write level", r)
	}

	enabled, err := EnableVersionedWrites(context.Background(), nil, dir, r)
	if err != nil || !enabled.WriteBound {
		t.Fatalf("EnableVersionedWrites = %+v, %v", enabled, err)
	}
	reloaded, err := LoadOrMaterialize(context.Background(), nil, dir, "", "")
	if err != nil || !reloaded.WriteBound {
		t.Fatalf("reloaded = %+v, %v; want the enabled level kept", reloaded, err)
	}
}

func TestLoadOrMaterialize_aCorruptLevelFileIsAnError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	writeRoot(t, dir, "1", strings.Repeat("c", 64))
	if err := os.WriteFile(filepath.Join(dir, FileLevelName), []byte("lots"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrMaterialize(context.Background(), nil, dir, "", ""); err == nil {
		t.Fatal("an unreadable write level was ignored")
	}
}

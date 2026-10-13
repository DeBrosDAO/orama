package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Concurrent `orama network add` in one HOME (a CI script configuring several
// clusters in parallel) lost environments: each read the file, appended its own
// and wrote the whole file back, so the last writer won (stagenet e2e,
// 2026-09-30).
func TestAddEnvironment_concurrentAddsAreAllKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".orama", "environments.json")
	orig := getEnvironmentConfigPathFn
	getEnvironmentConfigPathFn = func() (string, error) { return path, nil }
	t.Cleanup(func() { getEnvironmentConfigPathFn = orig })

	const adds = 24
	var wg sync.WaitGroup
	errs := make(chan error, adds)
	for i := range adds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- AddEnvironment(fmt.Sprintf("env-%d", i), "https://gw.example", "")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AddEnvironment: %v", err)
		}
	}

	cfg, err := LoadEnvironmentConfig()
	if err != nil {
		t.Fatalf("the file is unreadable after concurrent adds: %v", err)
	}
	if len(cfg.Environments) != adds {
		t.Fatalf("%d environments kept of %d added", len(cfg.Environments), adds)
	}
}

// A write is a rename of a finished file, so a reader never sees half of one
// and a crash leaves the old file, not a truncated one.
func TestAddEnvironment_replacesAtomicallyAndLeavesNoTemp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".orama")
	path := filepath.Join(dir, "environments.json")
	orig := getEnvironmentConfigPathFn
	getEnvironmentConfigPathFn = func() (string, error) { return path, nil }
	t.Cleanup(func() { getEnvironmentConfigPathFn = orig })

	if err := AddEnvironment("a", "https://a.example", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddEnvironment("b", "https://b.example", ""); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "environments.json" && e.Name() != "environments.json"+environmentLockSuffix {
			t.Errorf("a write left %s behind", e.Name())
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != environmentFilePerm {
		t.Errorf("environments.json mode %v, want %v", info.Mode().Perm(), os.FileMode(environmentFilePerm))
	}
}

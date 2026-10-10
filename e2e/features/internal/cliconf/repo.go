//go:build e2e_fleet

// Package cliconf holds what the CLI conformance features share: the command
// tree docs/whitepaper/technical-reference/appendices/d-cli-reference.md documents, the tree the live `orama --help`
// output shows, and the checks every command must pass (help consistent with
// the reference, usage mistakes exit with the usage code, --json accepted).
package cliconf

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// EnvRepoRoot names the checkout the run was built from. The runner sets it
// for the provisioner; a feature package started by `go test` runs inside the
// checkout, so walking up from the working directory finds it too.
const EnvRepoRoot = "E2E_REPO_ROOT"

// repoMarkers are the files that identify the root of the orama checkout.
var repoMarkers = []string{"core/go.mod", "e2e/go.mod", ReferencePath}

// RepoRoot returns the checkout the docs are read from.
func RepoRoot(t testing.TB) string {
	t.Helper()
	root, err := FindRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// FindRepoRoot is RepoRoot without a test: E2E_REPO_ROOT, else the first
// directory above the working directory that holds every repo marker.
func FindRepoRoot() (string, error) {
	if dir := os.Getenv(EnvRepoRoot); dir != "" {
		if err := isRepoRoot(dir); err != nil {
			return "", fmt.Errorf("%s=%s: %w", EnvRepoRoot, dir, err)
		}
		return dir, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to read the working directory: %w", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if isRepoRoot(dir) == nil {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			return "", fmt.Errorf("no orama checkout above %s; set %s", wd, EnvRepoRoot)
		}
	}
}

func isRepoRoot(dir string) error {
	for _, m := range repoMarkers {
		if _, err := os.Stat(filepath.Join(dir, m)); err != nil {
			return fmt.Errorf("not the orama checkout (no %s): %w", m, err)
		}
	}
	return nil
}

// ReadRepoFile reads rel (slash separated) from the checkout.
func ReadRepoFile(t testing.TB, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(RepoRoot(t), filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s does not exist in the checkout", rel)
	}
	if err != nil {
		t.Fatalf("failed to read %s: %v", rel, err)
	}
	return string(raw)
}

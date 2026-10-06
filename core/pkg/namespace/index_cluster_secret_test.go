package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func writeClusterSecret(t *testing.T, oramaDir, content string) {
	t.Helper()
	dir := filepath.Join(oramaDir, "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cluster-secret"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadClusterSecret_trimsTheStoredValue(t *testing.T) {
	dir := t.TempDir()
	writeClusterSecret(t, dir, "  abc123\n")
	got, err := readClusterSecret(filepath.Join(dir, "secrets", "cluster-secret"))
	if err != nil || got != "abc123" {
		t.Fatalf("readClusterSecret = %q, %v; want abc123", got, err)
	}
}

func TestReadClusterSecret_missingIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets", "cluster-secret")
	if _, err := readClusterSecret(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("readClusterSecret on a missing file = %v, want an error naming %s", err, path)
	}
}

func TestReadClusterSecret_emptyIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeClusterSecret(t, dir, " \n\t")
	if _, err := readClusterSecret(filepath.Join(dir, "secrets", "cluster-secret")); err == nil {
		t.Fatal("a whitespace-only cluster secret was accepted")
	}
}

// EnsureIPFSCluster used to start the daemon with CLUSTER_SECRET="" when the
// secret could not be read. It must refuse before it writes an env file or
// touches a unit.
func TestEnsureIPFSCluster_refusesWithoutASecret(t *testing.T) {
	oramaDir := t.TempDir()
	s := &IndexSupervisor{oramaDir: oramaDir, dataDir: filepath.Join(oramaDir, "data"), logger: zap.NewNop()}
	err := s.EnsureIPFSCluster("12D3KooWtest")
	if err == nil || !strings.Contains(err.Error(), "cluster-secret") {
		t.Fatalf("EnsureIPFSCluster without a secret = %v, want an error naming the secret file", err)
	}
}

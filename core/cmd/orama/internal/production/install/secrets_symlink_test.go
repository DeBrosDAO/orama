package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	joinhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/join"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// The join flow writes the cluster's secrets as root into secrets/, which the
// orama user owns on a re-install: a symlink planted there must be refused,
// not written through.
func TestSaveSecretsFromJoinResponse_symlinkRefused(t *testing.T) {
	oramaDir := filepath.Join(t.TempDir(), ".orama")
	if err := os.MkdirAll(filepath.Join(oramaDir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(target, []byte("root-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(oramaDir, "secrets", "cluster-secret")); err != nil {
		t.Fatal(err)
	}
	o := &Orchestrator{oramaDir: oramaDir}
	err := o.saveSecretsFromJoinResponse(&joinhandlers.JoinResponse{ClusterSecret: "pwned"})
	if !errors.Is(err, rootfs.ErrSymlink) {
		t.Fatalf("err = %v, want a refused symlink", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "root-only" {
		t.Fatalf("symlink target written: %q", data)
	}
}

func TestSaveSecretsFromJoinResponse_writesSecrets(t *testing.T) {
	oramaDir := filepath.Join(t.TempDir(), ".orama")
	o := &Orchestrator{oramaDir: oramaDir}
	if err := o.saveSecretsFromJoinResponse(&joinhandlers.JoinResponse{ClusterSecret: "c", RQLitePassword: "p"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"cluster-secret": "c", "rqlite-password": "p", "encryption-root": "c", "encryption-root.id": "1"} {
		path := filepath.Join(oramaDir, "secrets", name)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", name, data, err, want)
		}
		if info, err := os.Stat(path); err == nil && info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", name, info.Mode().Perm())
		}
	}
}

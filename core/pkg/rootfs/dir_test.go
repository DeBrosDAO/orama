//go:build unix

package rootfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDirOwner(t *testing.T) {
	anchor := t.TempDir()
	dir := filepath.Join(anchor, "data", "deployments", "alice-web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	uid, _, err := At(anchor).DirOwner(dir)
	if err != nil {
		t.Fatalf("DirOwner: %v", err)
	}
	if int(uid) != os.Getuid() {
		t.Errorf("uid %d, want %d", uid, os.Getuid())
	}
}

// A symlink at the directory, or at any component above it, is refused rather
// than followed: PID 1 would follow it when it binds the path.
func TestDirOwner_refusesASymlinkAnywhere(t *testing.T) {
	anchor := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(anchor, "data", "deployments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(anchor, "data", "deployments", "alice-web")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := At(anchor).DirOwner(filepath.Join(anchor, "data", "deployments", "alice-web")); !errors.Is(err, ErrSymlink) {
		t.Errorf("a symlinked instance directory: err = %v, want ErrSymlink", err)
	}

	anchor2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(elsewhere, "alice-web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(anchor2, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(anchor2, "data", "deployments")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := At(anchor2).DirOwner(filepath.Join(anchor2, "data", "deployments", "alice-web")); !errors.Is(err, ErrSymlink) {
		t.Errorf("a symlinked parent: err = %v, want ErrSymlink", err)
	}
}

func TestDirOwner_refusesAFileAndAMissingPath(t *testing.T) {
	anchor := t.TempDir()
	file := filepath.Join(anchor, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := At(anchor).DirOwner(file); err == nil {
		t.Error("a regular file was accepted as a directory")
	}
	if _, _, err := At(anchor).DirOwner(filepath.Join(anchor, "missing")); err == nil {
		t.Error("a missing directory was accepted")
	}
}

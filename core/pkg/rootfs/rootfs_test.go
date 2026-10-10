//go:build unix

package rootfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fifoTimeout bounds a call that must not block on a FIFO.

const fifoTimeout = 5 * time.Second

// newTree is an anchor with configs/ below it, and a directory outside it

// holding the file an attacker would point a symlink at.

func newTree(t *testing.T) (Root, string, string) {
	t.Helper()
	anchor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(anchor, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	target := filepath.Join(outside, "shadow")
	if err := os.WriteFile(target, []byte("root-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	return At(anchor), anchor, target
}

func assertUntouched(t *testing.T, target string) {
	t.Helper()
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read symlink target: %v", err)
	}
	if string(data) != "root-only" {
		t.Fatalf("symlink target was written: %q", data)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("symlink target mode changed to %v", info.Mode().Perm())
	}
}

func assertSymlinkError(t *testing.T, err error, path string) {
	t.Helper()
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("err = %v, want ErrSymlink", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name %s", err, path)
	}
}

// Overwriting replaces the file with a new inode (atomic rename), applies the

// requested mode even when the old file had another one, and keeps the owner.

// A failure after the temporary file exists leaves the old file whole and no

// temporary file behind.

func TestReadFile_readsWithinLimit(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "configs", "node.yaml")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := r.ReadFile(path, 5)
	if err != nil || string(data) != "12345" {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
	if _, err := r.ReadFile(path, 4); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("a file over the limit was read: %v", err)
	}
}

func TestReadFile_emptyFile(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "configs", "empty")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := r.ReadFile(path, 0)
	if err != nil || len(data) != 0 {
		t.Fatalf("ReadFile = %q, %v", data, err)
	}
}

func TestReadFile_missingIsNotExist(t *testing.T) {
	r, anchor, _ := newTree(t)
	for _, path := range []string{
		filepath.Join(anchor, "configs", "absent"),
		filepath.Join(anchor, "absent", "node.yaml"),
	} {
		if _, err := r.ReadFile(path, 1024); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile(%s) err = %v, want fs.ErrNotExist", path, err)
		}
	}
}

func TestReadFile_symlinksRefused(t *testing.T) {
	r, anchor, target := newTree(t)
	leaf := filepath.Join(anchor, "configs", "node.yaml")
	if err := os.Symlink(target, leaf); err != nil {
		t.Fatal(err)
	}
	_, err := r.ReadFile(leaf, 1024)
	assertSymlinkError(t, err, leaf)

	dir := filepath.Join(anchor, "data")
	if err := os.Symlink(filepath.Dir(target), dir); err != nil {
		t.Fatal(err)
	}
	_, err = r.ReadFile(filepath.Join(dir, "shadow"), 1024)
	assertSymlinkError(t, err, dir)
}

// A FIFO planted where a file is expected must not hang root.

func TestReadFile_fifoRefusedWithoutBlocking(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "configs", "node.yaml")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	checks := map[string]func() error{
		"ReadFile": func() error { _, err := r.ReadFile(path, 1024); return err },
		"Chmod":    func() error { return r.Chmod(path, 0o600) },
		"WriteFile": func() error {
			return r.WriteFile(path, []byte("x"), 0o600)
		},
	}
	for name, call := range checks {
		done := make(chan error, 1)
		go func() { done <- call() }()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s on a FIFO succeeded", name)
			}
		case <-time.After(fifoTimeout):
			t.Fatalf("%s blocked on a FIFO", name)
		}
	}
}

func TestMkdirAll_createsNestedAndToleratesExisting(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "data", "ipfs", "repo")
	for i := 0; i < 2; i++ {
		if err := r.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll #%d: %v", i, err)
		}
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("%s not created: %v", path, err)
	}
}

func TestMkdirAll_createsMissingAnchor(t *testing.T) {
	anchor := filepath.Join(t.TempDir(), "opt-orama")
	path := filepath.Join(anchor, ".orama", "secrets")
	if err := At(anchor).MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o700 != 0o700 || info.Mode().Perm()&writableByOthers != 0 {
		t.Errorf("anchor mode = %v", info.Mode().Perm())
	}
}

func TestMkdirAll_symlinkIntermediateRefused(t *testing.T) {
	r, anchor, target := newTree(t)
	link := filepath.Join(anchor, "data")
	if err := os.Symlink(filepath.Dir(target), link); err != nil {
		t.Fatal(err)
	}
	assertSymlinkError(t, r.MkdirAll(filepath.Join(link, "vault"), 0o755), link)
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), "vault")); !os.IsNotExist(err) {
		t.Errorf("a directory was created through the symlink: %v", err)
	}
}

func TestMkdirAll_fileInTheWay(t *testing.T) {
	r, anchor, _ := newTree(t)
	file := filepath.Join(anchor, "configs", "node.yaml")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.MkdirAll(filepath.Join(file, "sub"), 0o755); err == nil {
		t.Fatal("MkdirAll through a regular file succeeded")
	}
}

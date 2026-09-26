//go:build unix

package rootfs

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWriteFile_createsWithMode(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "configs", "node.yaml")
	if err := r.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	if got, _ := os.ReadFile(path); string(got) != "a: 1\n" {
		t.Errorf("content = %q", got)
	}
}

func TestWriteFile_overwriteReplacesAtomicallyAndKeepsOwner(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "configs", "node.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	var before unix.Stat_t
	if err := unix.Stat(path, &before); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	var after unix.Stat_t
	if err := unix.Stat(path, &after); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}
	if after.Ino == before.Ino {
		t.Error("file was rewritten in place, not replaced by rename")
	}
	if after.Mode&0o777 != 0o600 {
		t.Errorf("mode = %#o, want 0600", after.Mode&0o777)
	}
	if after.Uid != before.Uid || after.Gid != before.Gid {
		t.Errorf("owner = %d:%d, want %d:%d", after.Uid, after.Gid, before.Uid, before.Gid)
	}
	assertNoTempFiles(t, filepath.Join(anchor, "configs"))
}

func TestWriteFile_symlinkLeafRefused(t *testing.T) {
	r, anchor, target := newTree(t)
	path := filepath.Join(anchor, "configs", "node.yaml")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	assertSymlinkError(t, r.WriteFile(path, []byte("pwned"), 0o644), path)
	assertUntouched(t, target)
	if info, err := os.Lstat(path); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced: %v %v", info, err)
	}
}

func TestWriteFile_symlinkIntermediateRefused(t *testing.T) {
	r, anchor, target := newTree(t)
	link := filepath.Join(anchor, "secrets")
	if err := os.Symlink(filepath.Dir(target), link); err != nil {
		t.Fatal(err)
	}
	err := r.WriteFile(filepath.Join(link, "shadow"), []byte("pwned"), 0o644)
	assertSymlinkError(t, err, link)
	assertUntouched(t, target)
}

func TestWriteFile_outsideAnchorRefused(t *testing.T) {
	r, anchor, target := newTree(t)
	for _, path := range []string{
		filepath.Join(anchor, "..", filepath.Base(filepath.Dir(target)), "shadow"),
		"configs/node.yaml",
		anchor,
	} {
		if err := r.WriteFile(path, []byte("x"), 0o644); err == nil {
			t.Errorf("WriteFile(%q) succeeded", path)
		}
	}
	assertUntouched(t, target)
}

func TestWriteFile_missingParentIsNotExist(t *testing.T) {
	r, anchor, _ := newTree(t)
	err := r.WriteFile(filepath.Join(anchor, "absent", "x"), []byte("x"), 0o644)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestWriteFile_failedRenameLeavesOldFile(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "configs", "node.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	renameat = func(int, string, int, string) error { return unix.EIO }
	t.Cleanup(func() { renameat = unix.Renameat })

	if err := r.WriteFile(path, []byte("new"), 0o600); !errors.Is(err, unix.EIO) {
		t.Fatalf("err = %v, want EIO", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Errorf("content = %q, want old", got)
	}
	assertNoTempFiles(t, filepath.Join(anchor, "configs"))
}

func TestWriteFile_directoryAtLeafRefused(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "configs")
	if err := r.WriteFile(path, []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile over a directory succeeded")
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temporary file %s left behind", e.Name())
		}
	}
}

func TestWriteFile_emptyData(t *testing.T) {
	r, anchor, _ := newTree(t)
	path := filepath.Join(anchor, "configs", "empty")
	if err := r.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, []byte{}) {
		t.Fatalf("content = %q, %v", got, err)
	}
}

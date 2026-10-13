//go:build unix

package rootfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChmodChown_applyToFilesAndDirectories(t *testing.T) {
	r, anchor, _ := newTree(t)
	file := filepath.Join(anchor, "configs", "node.yaml")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(anchor, "configs")
	for path, mode := range map[string]fs.FileMode{file: 0o600, dir: 0o700} {
		if err := r.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := r.Chown(path, os.Getuid(), os.Getgid()); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("%s mode = %v, want %v", path, info.Mode().Perm(), mode)
		}
	}
}

func TestChmodChown_symlinkRefused(t *testing.T) {
	r, anchor, target := newTree(t)
	path := filepath.Join(anchor, "configs", "node.yaml")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	assertSymlinkError(t, r.Chmod(path, 0o666), path)
	assertSymlinkError(t, r.Chown(path, os.Getuid(), os.Getgid()), path)
	assertUntouched(t, target)
}

// A hard link can name a file outside the tree; its mode must not change.

func TestChmod_hardLinkRefused(t *testing.T) {
	r, anchor, _ := newTree(t)
	orig := filepath.Join(anchor, "orig")
	if err := os.WriteFile(orig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(anchor, "configs", "node.yaml")
	if err := os.Link(orig, link); err != nil {
		t.Fatal(err)
	}
	if err := r.Chmod(link, 0o666); err == nil || !strings.Contains(err.Error(), "hard links") {
		t.Fatalf("Chmod of a hard-linked file: %v", err)
	}
	if info, _ := os.Stat(orig); info.Mode().Perm() != 0o600 {
		t.Errorf("hard-linked file mode changed to %v", info.Mode().Perm())
	}
}

func TestRemove_removesFileAndEmptyDirectory(t *testing.T) {
	r, anchor, _ := newTree(t)
	file := filepath.Join(anchor, "configs", "stray")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(filepath.Join(anchor, "configs")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(anchor, "configs")); !os.IsNotExist(err) {
		t.Errorf("configs still exists: %v", err)
	}
	if err := r.Remove(file); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("removing a missing file: err = %v, want fs.ErrNotExist", err)
	}
}

func TestRemove_symlinkRefused(t *testing.T) {
	r, anchor, target := newTree(t)
	path := filepath.Join(anchor, "configs", "stray")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	assertSymlinkError(t, r.Remove(path), path)
	assertUntouched(t, target)
}

func TestCheckAnchor_rootRequiresRootOwnedNotWritableByOthers(t *testing.T) {
	cases := []struct {
		name    string
		uid     uint32
		mode    uint32
		euid    int
		wantErr bool
	}{
		{"root-owned 0755 as root", 0, 0o755, 0, false},
		{"orama-owned as root", 1001, 0o755, 0, true},
		{"group-writable as root", 0, 0o775, 0, true},
		{"world-writable sticky as root", 0, 0o1777, 0, true},
		{"not root: anchor not checked", 1001, 0o777, 1001, false},
	}
	for _, tc := range cases {
		err := checkAnchor("/opt/orama", tc.uid, tc.mode, tc.euid)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

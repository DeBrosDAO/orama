package durablefile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

func TestWrite_replacesByRenameWithTheModeAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := inode(t, path)
	if err := Write(path, []byte("new contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new contents" {
		t.Fatalf("read %q %v", got, err)
	}
	if inode(t, path) == before {
		t.Fatal("the file was rewritten in place, not replaced by rename")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("a temporary file was left behind: %d entries", len(entries))
	}
}

func TestWrite_missingDirectoryFailsAndWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "config")
	if err := Write(path, []byte("x"), 0o600); err == nil {
		t.Fatal("a write into a missing directory succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a failed write left a file")
	}
}

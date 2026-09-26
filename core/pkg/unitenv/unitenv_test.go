package unitenv

import (
	"os"
	"path/filepath"
	"testing"
)

func self() Owner { return Owner{UID: os.Getuid(), GID: os.Getgid()} }

func TestWrite_GroupReadableRootOwnedTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unit-env")
	if err := Write(dir, "anchat", "gateway", []byte("A=1\n"), self()); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		dir:                            dirMode,
		filepath.Join(dir, "anchat"):   dirMode,
		Path(dir, "anchat", "gateway"): fileMode,
	} {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != want {
			t.Errorf("%s: mode %v (%v), want %v", path, st.Mode().Perm(), err, want)
		}
	}
	if got, _ := os.ReadFile(Path(dir, "anchat", "gateway")); string(got) != "A=1\n" {
		t.Errorf("content %q", got)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "anchat"))
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

// The attack this closes: a namespace directory the attacker controls, with
// <svc>.env symlinked to a root-only file. Neither name may reach the tree.
func TestWrite_RefusesNamesThatCouldEscape(t *testing.T) {
	dir := t.TempDir()
	for _, c := range [][2]string{{"../x", "gateway"}, {"a/b", "gateway"}, {".x", "gateway"}, {"ns", "../gateway"}, {"ns", "Gateway"}, {"ns", ""}, {"", "gateway"}} {
		if err := Write(dir, c[0], c[1], nil, self()); err == nil {
			t.Errorf("%q/%q must be refused", c[0], c[1])
		}
	}
}

func TestClearNamespace_RemovesItsFilesOnly(t *testing.T) {
	dir := t.TempDir()
	Write(dir, "a", "gateway", []byte("x"), self())
	Write(dir, "b", "gateway", []byte("y"), self())
	if err := ClearNamespace(dir, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path(dir, "a", "gateway")); !os.IsNotExist(err) {
		t.Error("namespace a's env survived")
	}
	if _, err := os.Stat(Path(dir, "b", "gateway")); err != nil {
		t.Error("namespace b's env was removed")
	}
	if err := ClearNamespace(dir, "../b"); err == nil {
		t.Error("a traversing namespace must be refused")
	}
}

package deploysecrets

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWrite_RootOnlyFilesUnderARootOnlyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deploy")
	if err := Write(dir, "acme-web", Env, []byte("A=1\n")); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v, want 0700", st.Mode().Perm())
	}
	fi, err := os.Stat(Path(dir, "acme-web", Env))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, %v", fi, err)
	}
	if got, _ := os.ReadFile(Path(dir, "acme-web", Env)); string(got) != "A=1\n" {
		t.Errorf("content %q", got)
	}
}

// A symlink planted at the target is replaced, never written through: the
// file the link pointed at is untouched.
func TestWrite_ReplacesASymlinkInsteadOfFollowingIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deploy")
	os.MkdirAll(dir, 0o700)
	victim := filepath.Join(t.TempDir(), "shadow")
	os.WriteFile(victim, []byte("root:secret"), 0o600)
	os.Symlink(victim, Path(dir, "x", Token))
	os.Symlink(victim, Path(dir, "x", Token)+".tmp")

	if err := Write(dir, "x", Token, []byte("tok")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "root:secret" {
		t.Fatalf("the symlink target was written: %q", got)
	}
	fi, _ := os.Lstat(Path(dir, "x", Token))
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the token is still a symlink")
	}
}

func TestWrite_RefusesInstanceNamesThatCouldBePaths(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", "../x", "a/b", ".hidden", "a b", "a\nb"} {
		if err := Write(dir, bad, Env, nil); err == nil {
			t.Errorf("instance %q must be refused", bad)
		}
	}
}

func TestClear_RemovesBothAndToleratesAbsence(t *testing.T) {
	dir := t.TempDir()
	Write(dir, "acme-web", Env, []byte("x"))
	Write(dir, "acme-web", Token, []byte("y"))
	if err := Clear(dir, "acme-web"); err != nil {
		t.Fatal(err)
	}
	for _, k := range []Kind{Env, Token} {
		if _, err := os.Stat(Path(dir, "acme-web", k)); !os.IsNotExist(err) {
			t.Errorf("%s file survived", k)
		}
	}
	if err := Clear(dir, "acme-web"); err != nil {
		t.Errorf("clearing twice: %v", err)
	}
}

// The helper serves requests in parallel; two writes of one instance's file
// used to race on a fixed "<target>.tmp" and one of them failed.
func TestWrite_concurrentWritesOfOneInstance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deploy")
	const writers = 16
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			errs <- Write(dir, "ns-app", Env, []byte(fmt.Sprintf("N=%d\n", i)))
		}(i)
	}
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent write failed: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want only the target", names)
	}
}

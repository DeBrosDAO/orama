package install

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// redirectPrivHelper points the install at a temporary directory for one test.
func redirectPrivHelper(t *testing.T) (home, dest, sudoers string) {
	t.Helper()
	root := t.TempDir()
	home = filepath.Join(root, "opt-orama")
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sudoers.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest = filepath.Join(root, "usr-local-bin", "orama-privhelper")
	sudoers = filepath.Join(root, "sudoers.d", "orama-namespaces")

	oldDest, oldSudoers, oldChown := privHelperDest, oramaSudoersPath, chownRoot
	privHelperDest, oramaSudoersPath = dest, sudoers
	chownRoot = func(string) error { return nil }
	t.Cleanup(func() { privHelperDest, oramaSudoersPath, chownRoot = oldDest, oldSudoers, oldChown })
	return home, dest, sudoers
}

func TestEnsurePrivHelper_InstallsTheHelperThenGrantsIt(t *testing.T) {
	home, dest, sudoers := redirectPrivHelper(t)
	if err := os.WriteFile(filepath.Join(home, "bin", privHelperBinary), []byte("#!helper"), 0o755); err != nil {
		t.Fatal(err)
	}

	ps := NewProductionSetup(home, io.Discard, false, true)
	if err := ps.EnsurePrivHelper(); err != nil {
		t.Fatalf("EnsurePrivHelper: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "#!helper" {
		t.Fatalf("helper not installed intact: %q, %v", got, err)
	}
	if st, _ := os.Stat(dest); st.Mode().Perm() != 0o755 {
		t.Errorf("helper mode = %v, want 0755", st.Mode().Perm())
	}
	rule, err := os.ReadFile(sudoers)
	if err != nil {
		t.Fatalf("sudoers grant not written: %v", err)
	}
	if string(rule) != privhelper.SudoersRule("orama") {
		t.Errorf("sudoers = %q", rule)
	}
}

// The grant must never name a file that is not there: a release without the
// helper fails before anything is granted, instead of leaving sudoers
// pointing at nothing and the node unable to start a service.
func TestEnsurePrivHelper_MissingHelperFailsBeforeGranting(t *testing.T) {
	home, _, sudoers := redirectPrivHelper(t)

	ps := NewProductionSetup(home, io.Discard, false, true)
	err := ps.EnsurePrivHelper()
	if err == nil || !strings.Contains(err.Error(), "missing from the release") {
		t.Fatalf("expected a missing-helper error, got %v", err)
	}
	if _, statErr := os.Stat(sudoers); !os.IsNotExist(statErr) {
		t.Errorf("sudoers must not be written when the helper is missing (stat: %v)", statErr)
	}
}

func TestSameContent_DetectsATruncatedCopy(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(a, []byte("full helper"), 0o644)
	os.WriteFile(b, []byte("full"), 0o644)
	if err := sameContent(a, b); err == nil {
		t.Error("a truncated copy must be detected")
	}
	os.WriteFile(b, []byte("full helper"), 0o644)
	if err := sameContent(a, b); err != nil {
		t.Errorf("identical files: %v", err)
	}
}

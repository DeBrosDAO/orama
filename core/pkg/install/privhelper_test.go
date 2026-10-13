package install

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

type systemctlRecorder struct{ calls []string }

// redirectPrivHelper points the install at a temporary directory for one test
// and records the systemctl calls instead of making them.
func redirectPrivHelper(t *testing.T) (home, dest, unitDir, sudoers string, rec *systemctlRecorder) {
	t.Helper()
	root := t.TempDir()
	home = filepath.Join(root, "opt-orama")
	unitDir = filepath.Join(root, "systemd")
	for _, d := range []string{filepath.Join(home, "bin"), unitDir, filepath.Join(root, "sudoers.d")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dest = filepath.Join(root, "usr-local-bin", "orama-privhelper")
	sudoers = filepath.Join(root, "sudoers.d", "orama-namespaces")
	rec = &systemctlRecorder{}

	oldDest, oldUnits, oldSudoers, oldChown, oldRun := privHelperDest, systemdUnitDir, legacySudoersPath, chownRoot, runSystemctl
	privHelperDest, systemdUnitDir, legacySudoersPath = dest, unitDir, sudoers
	chownRoot = func(string) error { return nil }
	runSystemctl = func(args ...string) ([]byte, error) {
		rec.calls = append(rec.calls, strings.Join(args, " "))
		return nil, nil
	}
	t.Cleanup(func() {
		privHelperDest, systemdUnitDir, legacySudoersPath, chownRoot, runSystemctl = oldDest, oldUnits, oldSudoers, oldChown, oldRun
	})
	return home, dest, unitDir, sudoers, rec
}

func TestEnsurePrivHelper_InstallsTheHelperThenItsSocket(t *testing.T) {
	home, dest, unitDir, sudoers, rec := redirectPrivHelper(t)
	if err := os.WriteFile(filepath.Join(home, "bin", privHelperBinary), []byte("#!helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An upgraded node still has the old wildcard rules; they must go.
	if err := os.WriteFile(sudoers, []byte("orama ALL=(root) NOPASSWD: /bin/systemctl start orama-namespace-*\n"), 0o440); err != nil {
		t.Fatal(err)
	}

	ps := NewProductionSetup(home, io.Discard, false, true)
	if err := ps.EnsurePrivHelper(); err != nil {
		t.Fatalf("EnsurePrivHelper: %v", err)
	}

	if got, err := os.ReadFile(dest); err != nil || string(got) != "#!helper" {
		t.Fatalf("helper not installed intact: %q, %v", got, err)
	}
	if st, _ := os.Stat(dest); st.Mode().Perm() != 0o755 {
		t.Errorf("helper mode = %v, want 0755", st.Mode().Perm())
	}
	for name, want := range map[string]string{privhelper.SocketUnitName: privhelper.SocketUnit, privhelper.ServiceUnitName: privhelper.ServiceUnit} {
		if got, err := os.ReadFile(filepath.Join(unitDir, name)); err != nil || string(got) != want {
			t.Errorf("unit %s not written as expected (%v)", name, err)
		}
	}
	if want := "daemon-reload|enable orama-privhelper.socket|restart orama-privhelper.socket"; strings.Join(rec.calls, "|") != want {
		t.Errorf("systemctl calls = %q, want %q", strings.Join(rec.calls, "|"), want)
	}
	if _, err := os.Stat(sudoers); !os.IsNotExist(err) {
		t.Errorf("the legacy wildcard sudoers file must be removed (stat: %v)", err)
	}
}

// Nothing may listen for a helper that is not there: a release without it
// fails before any unit is written or started.
func TestEnsurePrivHelper_MissingHelperFailsBeforeTheSocket(t *testing.T) {
	home, _, unitDir, _, rec := redirectPrivHelper(t)

	ps := NewProductionSetup(home, io.Discard, false, true)
	err := ps.EnsurePrivHelper()
	if err == nil || !strings.Contains(err.Error(), "missing from the release") {
		t.Fatalf("expected a missing-helper error, got %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("systemctl must not be called, got %v", rec.calls)
	}
	if entries, _ := os.ReadDir(unitDir); len(entries) != 0 {
		t.Errorf("no unit may be written, found %d", len(entries))
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

// The helper is copied from bin/, which stage-archive replaces: it must be
// copied under the archive lock, or it could come from a half-swapped build.
func TestEnsurePrivHelper_holdsTheArchiveLock(t *testing.T) {
	home, _, _, _, rec := redirectPrivHelper(t)
	if err := os.WriteFile(filepath.Join(home, "bin", privHelperBinary), []byte("#!helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := lockArchive
	t.Cleanup(func() { lockArchive = prev })
	var locked []string
	held := false
	lockArchive = func(dir string) (func() error, error) {
		locked = append(locked, dir)
		held = true
		return func() error { held = false; return nil }, nil
	}
	prevRun := runSystemctl
	var unlockedCalls []string
	runSystemctl = func(args ...string) ([]byte, error) {
		if !held {
			unlockedCalls = append(unlockedCalls, strings.Join(args, " "))
		}
		return prevRun(args...)
	}
	if err := NewProductionSetup(home, io.Discard, false, true).EnsurePrivHelper(); err != nil {
		t.Fatal(err)
	}
	if len(locked) != 1 || locked[0] != home || held {
		t.Fatalf("lock calls %v, still held %v", locked, held)
	}
	if len(unlockedCalls) != 0 || len(rec.calls) == 0 {
		t.Fatalf("systemctl calls %v; outside the lock: %v", rec.calls, unlockedCalls)
	}
}

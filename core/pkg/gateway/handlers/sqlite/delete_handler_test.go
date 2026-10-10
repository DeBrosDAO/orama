package sqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WAL mode leaves a -wal holding committed pages and a -shm holding the shared
// index beside the .db. Removing only the .db means a database created again
// under the same name opens on top of the previous tenant's uncheckpointed
// writes.

func writeFiles(t *testing.T, base string, suffixes ...string) {
	t.Helper()
	for _, suffix := range suffixes {
		if err := os.WriteFile(base+suffix, []byte("x"), 0600); err != nil {
			t.Fatalf("write %s: %v", base+suffix, err)
		}
	}
}

func TestRemoveSQLiteFiles_removes_the_wal_sidecars(t *testing.T) {
	base := filepath.Join(t.TempDir(), "app.db")
	writeFiles(t, base, "", "-wal", "-shm")

	removed, err := removeSQLiteFiles(base)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("removed %v, want the db and both sidecars", removed)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(base + suffix); !os.IsNotExist(err) {
			t.Errorf("%s still exists", base+suffix)
		}
	}
}

// The delete has to be repeatable after a partial failure, so a file that is
// already gone is not an error.
func TestRemoveSQLiteFiles_is_idempotent(t *testing.T) {
	base := filepath.Join(t.TempDir(), "app.db")
	writeFiles(t, base, "")

	if _, err := removeSQLiteFiles(base); err != nil {
		t.Fatalf("first remove: %v", err)
	}
	removed, err := removeSQLiteFiles(base)
	if err != nil {
		t.Fatalf("second remove must succeed: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed %v on the second pass, want nothing", removed)
	}
}

func TestRemoveSQLiteFiles_empty_path(t *testing.T) {
	removed, err := removeSQLiteFiles("")
	if err != nil {
		t.Fatalf("an empty path is not an error: %v", err)
	}
	if removed != nil {
		t.Errorf("removed %v, want nothing", removed)
	}
}

// A file inside a directory with no write permission cannot be unlinked. An
// operator fixing that needs every failing path, not only the first.
func TestRemoveSQLiteFiles_reports_every_failure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	dir := t.TempDir()
	base := filepath.Join(dir, "app.db")
	writeFiles(t, base, "", "-wal")

	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0700) })

	_, err := removeSQLiteFiles(base)
	if err == nil {
		t.Fatal("an unremovable file must be an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "app.db") || !strings.Contains(msg, "app.db-wal") {
		t.Errorf("error %q must name every failing path", msg)
	}
}

func TestRemoveError_joins_every_failure(t *testing.T) {
	err := &removeError{failures: []string{"a: denied", "b: denied"}}
	if got := err.Error(); !strings.Contains(got, "a: denied") || !strings.Contains(got, "b: denied") {
		t.Errorf("Error() = %q", got)
	}
}

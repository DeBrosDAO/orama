package inspector

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteResults_PrivateModes(t *testing.T) {
	dir := t.TempDir()
	results := &Results{Checks: []CheckResult{{
		ID: "x", Name: "x", Subsystem: "system", Status: StatusPass, Severity: Low,
	}}}
	out, err := WriteResults(dir, "test", results, &ClusterData{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("report dir mode %o, want 0700", st.Mode().Perm())
	}
	sum, err := os.Stat(filepath.Join(out, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Mode().Perm() != 0o600 {
		t.Fatalf("summary.md mode %o, want 0600", sum.Mode().Perm())
	}
}

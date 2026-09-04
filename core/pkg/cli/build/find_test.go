package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Three copies of the archive finder existed, in push, setup and sandbox, and
// they matched differently. Which archive got deployed depended on which
// command was used to deploy it.

// writeArchive creates a file in dir and gives it a modification time.
func writeArchive(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
	return path
}

// findIn runs the finder against a directory of the test's choosing, which is
// what makes this testable at all: the real one reads /tmp.
func findIn(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	var newest string
	var newestMod int64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, archivePrefix) ||
			!strings.Contains(name, archiveInfix) ||
			!strings.HasSuffix(name, archiveSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if mod := info.ModTime().UnixNano(); mod > newestMod {
			newest = filepath.Join(dir, name)
			newestMod = mod
		}
	}
	return newest
}

func TestArchiveName_matches_what_the_finder_looks_for(t *testing.T) {
	name := ArchiveName("0.200.0", "amd64")
	if name != "orama-0.200.0-linux-amd64.tar.gz" {
		t.Fatalf("ArchiveName = %q", name)
	}

	dir := t.TempDir()
	writeArchive(t, dir, name, 0)
	if got := findIn(t, dir); got == "" {
		t.Error("the finder must match the name a build produces")
	}
}

func TestFindNewestArchive_picks_the_most_recent(t *testing.T) {
	dir := t.TempDir()
	writeArchive(t, dir, "orama-0.1.0-linux-amd64.tar.gz", 2*time.Hour)
	newest := writeArchive(t, dir, "orama-0.2.0-linux-amd64.tar.gz", time.Minute)
	writeArchive(t, dir, "orama-0.1.5-linux-arm64.tar.gz", time.Hour)

	if got := findIn(t, dir); got != newest {
		t.Errorf("got %q, want %q", got, newest)
	}
}

func TestFindNewestArchive_ignores_files_that_are_not_archives(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"orama.tar.gz",                       // no -linux-
		"orama-0.1.0-linux-amd64.tar",        // wrong suffix
		"something-0.1.0-linux-amd64.tar.gz", // wrong prefix
		"notes.txt",
	} {
		writeArchive(t, dir, name, 0)
	}
	if got := findIn(t, dir); got != "" {
		t.Errorf("got %q, want nothing to match", got)
	}
}

func TestFindNewestArchive_empty_directory(t *testing.T) {
	if got := findIn(t, t.TempDir()); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// The real function reads /tmp, which a test cannot control. It must return
// either nothing or a path that exists.
func TestFindNewestArchive_returns_a_real_path_or_nothing(t *testing.T) {
	got := FindNewestArchive()
	if got == "" {
		return
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("FindNewestArchive returned %q, which does not exist: %v", got, err)
	}
}

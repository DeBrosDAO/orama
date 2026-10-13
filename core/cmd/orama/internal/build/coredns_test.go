package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallRQLitePlugin_copiesTheSourcesAndNotTheTests(t *testing.T) {
	src, dir := t.TempDir(), t.TempDir()
	for name, content := range map[string]string{
		"plugin.go":      "package rqlite",
		"names.go":       "package rqlite",
		"plugin_test.go": "package rqlite",
		"README.md":      "docs",
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(src, "sub.go"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := installRQLitePlugin(src, dir); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "plugin", "rqlite"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != 2 || got[0] != "names.go" || got[1] != "plugin.go" {
		t.Fatalf("copied %v, want only names.go and plugin.go (no test files, docs or directories)", got)
	}
}

func TestInstallRQLitePlugin_missingSourceIsAnError(t *testing.T) {
	if err := installRQLitePlugin(filepath.Join(t.TempDir(), "absent"), t.TempDir()); err == nil {
		t.Fatal("a missing plugin directory was not reported")
	}
}

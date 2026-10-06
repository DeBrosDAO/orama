package rqlite

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallAuthFile_missing(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallAuthFile("", dir); err == nil {
		t.Fatal("empty path must refuse to start")
	}
	if _, err := InstallAuthFile(filepath.Join(dir, "nope.json"), dir); err == nil {
		t.Fatal("missing file must refuse to start")
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte("  \n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAuthFile(empty, dir); err == nil {
		t.Fatal("empty file must refuse to start")
	}
}

func TestInstallAuthFile_copies(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.json")
	body := `[{"username":"orama","password":"deadbeef","perms":["all"]}]`
	if err := os.WriteFile(src, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(dir, "data")
	dest, err := InstallAuthFile(src, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("copied %q", got)
	}
}

func writeAuth(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), AuthFileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestJoinUser_picksAUserAllowedToJoin(t *testing.T) {
	for body, want := range map[string]string{
		`[{"username":"orama","password":"x","perms":["all"]}]`:                                                          "orama",
		`[{"username":"reader","password":"x","perms":["query"]},{"username":"joiner","password":"y","perms":["join"]}]`: "joiner",
	} {
		got, err := JoinUser(writeAuth(t, body))
		if err != nil || got != want {
			t.Errorf("JoinUser(%s) = %q, %v; want %q", body, got, err, want)
		}
	}
}

// Without a user that may join, rqlited would be refused as "unauthorized" on
// every attempt; that is a start error, not something to discover in the log.
func TestJoinUser_refusesWhenNoUserMayJoin(t *testing.T) {
	for _, body := range []string{
		`[{"username":"reader","password":"x","perms":["query","status"]}]`,
		`[{"username":"","password":"x","perms":["all"]}]`,
		`[]`,
		`not json`,
	} {
		if got, err := JoinUser(writeAuth(t, body)); err == nil {
			t.Errorf("JoinUser(%s) = %q, want an error", body, got)
		}
	}
	if _, err := JoinUser(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("a missing auth file produced a join user")
	}
}

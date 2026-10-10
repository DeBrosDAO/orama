package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRememberHostKeys_keepsAPinForTheNextCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	line := "1.2.3.4 ssh-ed25519 AAAAB3NzaC1lZDI1NTE5AAAAITest"
	if err := rememberHostKeys([]string{line}); err != nil {
		t.Fatal(err)
	}
	if err := rememberHostKeys([]string{line, "1.2.3.4 ssh-ed25519 BBB"}); err != nil {
		t.Fatal(err)
	}
	path, err := operatorKnownHosts()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(home, ".orama", "known_hosts") {
		t.Fatalf("join-via would read %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(got), "AAAAB3") != 1 || !strings.Contains(string(got), "BBB") {
		t.Fatalf("known_hosts = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
}

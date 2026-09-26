package installers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The real tail of `ntfy --help` for 2.11.0. `ntfy --version` is not a flag,
// so reading it made IsInstalled false forever.
const ntfyHelpTail = `   --help, -h   show help

ntfy 2.11.0 (d11b100), runtime go1.22.2, built at 2024-05-13T20:16:12Z
Copyright (C) 2022 Philipp C. Heckel, licensed under Apache License 2.0 & GPLv2
`

func TestNtfyReportsVersion(t *testing.T) {
	if !ntfyReportsVersion(ntfyHelpTail, "2.11.0") {
		t.Error("the installed version was not recognised")
	}
	for _, v := range []string{"2.11", "2.1", "2.11.1", "1.0.0"} {
		if ntfyReportsVersion(ntfyHelpTail, v) {
			t.Errorf("version %q matched help for 2.11.0", v)
		}
	}
	if ntfyReportsVersion("Incorrect Usage: flag provided but not defined: -version", "2.11.0") {
		t.Error("an error message was read as a version")
	}
}

// Every re-install wrote into the running binary and failed "text file busy".
// A replacement has to leave the old file's contents intact for whoever has it
// open, and swap the path.
func TestReplaceBinary_swapsThePathNotTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ntfy")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	running, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()

	if err := replaceBinary(path, strings.NewReader("new")); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("path holds %q, want the new binary", got)
	}
	old := make([]byte, 3)
	if _, err := running.ReadAt(old, 0); err != nil || string(old) != "old" {
		t.Errorf("the open file changed under its reader: %q, %v", old, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".ntfy.new-*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestReplaceBinary_refusesAnOversizedBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ntfy")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.NewReader(strings.Repeat("x", maxNtfyBinaryBytes+1))
	if err := replaceBinary(path, big); err == nil {
		t.Fatal("an oversized binary was installed")
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Errorf("a refused binary still replaced the old one: %q", got[:min(len(got), 8)])
	}
}

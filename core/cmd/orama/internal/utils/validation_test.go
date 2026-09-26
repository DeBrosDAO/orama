package utils

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// node.yaml belongs to the orama user and install validates it as root: a
// symlink in its place is refused rather than parsed.
func TestValidateGeneratedConfig_symlinkRefused(t *testing.T) {
	oramaDir := filepath.Join(t.TempDir(), ".orama")
	if err := os.MkdirAll(filepath.Join(oramaDir, "configs"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(target, []byte("root:x:0:0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(oramaDir, "configs", "node.yaml")); err != nil {
		t.Fatal(err)
	}
	err := ValidateGeneratedConfig(oramaDir)
	if !errors.Is(err, rootfs.ErrSymlink) {
		t.Fatalf("err = %v, want a refused symlink", err)
	}
	if strings.Contains(err.Error(), "root:x") {
		t.Fatalf("the symlink target's contents leaked into the error: %v", err)
	}
}

func TestValidateGeneratedConfig_missing(t *testing.T) {
	err := ValidateGeneratedConfig(filepath.Join(t.TempDir(), ".orama"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not found", err)
	}
}

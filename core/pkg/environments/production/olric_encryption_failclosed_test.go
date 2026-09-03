package production

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateOlricConfig_emptyKeyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	cg := NewConfigGenerator(dir)
	_, err := cg.GenerateOlricConfig("127.0.0.1", 3320, "127.0.0.1", 3322, "local", "", nil)
	if err == nil {
		t.Fatal("missing olric-encryption-key must fail closed")
	}
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets", "olric-encryption-key"), []byte("dGVzdGtleXRlc3RrZXl0ZXN0a2V5dGVzdA==\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := cg.GenerateOlricConfig("127.0.0.1", 3320, "127.0.0.1", 3322, "local", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "encryptionKey:") {
		t.Fatalf("expected encryptionKey in config, got %q", out)
	}
}

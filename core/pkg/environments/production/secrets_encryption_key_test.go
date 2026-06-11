package production

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureSecretsEncryptionKey_generatesAndPersists verifies that a fresh
// oramaDir produces a valid 32-byte hex key written to disk.
func TestEnsureSecretsEncryptionKey_generatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	sg := NewSecretGenerator(dir)

	key, err := sg.EnsureSecretsEncryptionKey()
	if err != nil {
		t.Fatalf("EnsureSecretsEncryptionKey failed: %v", err)
	}
	if len(key) != 64 {
		t.Fatalf("expected 64 hex chars, got %d (%q)", len(key), key)
	}
	raw, err := hex.DecodeString(key)
	if err != nil || len(raw) != 32 {
		t.Fatalf("key is not 32 bytes hex: err=%v len=%d", err, len(raw))
	}

	// Persisted to the expected path.
	data, err := os.ReadFile(filepath.Join(dir, "secrets", "secrets-encryption-key"))
	if err != nil {
		t.Fatalf("reading persisted key failed: %v", err)
	}
	if strings.TrimSpace(string(data)) != key {
		t.Errorf("persisted key %q != returned key %q", strings.TrimSpace(string(data)), key)
	}
}

// TestEnsureSecretsEncryptionKey_idempotent verifies the key is stable across
// calls — this is the property that makes secrets survive restarts and stay
// identical across cluster nodes (bugboard #837).
func TestEnsureSecretsEncryptionKey_idempotent(t *testing.T) {
	dir := t.TempDir()
	sg := NewSecretGenerator(dir)

	first, err := sg.EnsureSecretsEncryptionKey()
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	second, err := sg.EnsureSecretsEncryptionKey()
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if first != second {
		t.Errorf("key changed between calls: %q != %q", first, second)
	}
}

// TestEnsureSecretsEncryptionKey_regeneratesInvalid verifies a corrupt/empty
// on-disk key (wrong length) is replaced with a fresh valid one.
func TestEnsureSecretsEncryptionKey_regeneratesInvalid(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	keyPath := filepath.Join(secretsDir, "secrets-encryption-key")
	if err := os.WriteFile(keyPath, []byte("too-short"), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	sg := NewSecretGenerator(dir)
	key, err := sg.EnsureSecretsEncryptionKey()
	if err != nil {
		t.Fatalf("EnsureSecretsEncryptionKey failed: %v", err)
	}
	if len(key) != 64 {
		t.Errorf("expected regenerated 64-char key, got %d (%q)", len(key), key)
	}
}

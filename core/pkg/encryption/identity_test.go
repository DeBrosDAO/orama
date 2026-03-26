package encryption

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateIdentity(t *testing.T) {
	t.Run("returns non-nil IdentityInfo", func(t *testing.T) {
		id, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("GenerateIdentity() returned error: %v", err)
		}
		if id == nil {
			t.Fatal("GenerateIdentity() returned nil")
		}
	})

	t.Run("PeerID is non-empty", func(t *testing.T) {
		id, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("GenerateIdentity() returned error: %v", err)
		}
		if id.PeerID == "" {
			t.Error("expected non-empty PeerID")
		}
	})

	t.Run("PrivateKey is non-nil", func(t *testing.T) {
		id, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("GenerateIdentity() returned error: %v", err)
		}
		if id.PrivateKey == nil {
			t.Error("expected non-nil PrivateKey")
		}
	})

	t.Run("PublicKey is non-nil", func(t *testing.T) {
		id, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("GenerateIdentity() returned error: %v", err)
		}
		if id.PublicKey == nil {
			t.Error("expected non-nil PublicKey")
		}
	})

	t.Run("two calls produce different identities", func(t *testing.T) {
		id1, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("first GenerateIdentity() returned error: %v", err)
		}
		id2, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("second GenerateIdentity() returned error: %v", err)
		}
		if id1.PeerID == id2.PeerID {
			t.Errorf("expected different PeerIDs, both got %s", id1.PeerID)
		}
	})
}

func TestSaveAndLoadIdentity(t *testing.T) {
	t.Run("round-trip preserves PeerID", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "identity.key")

		id, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("GenerateIdentity() returned error: %v", err)
		}

		if err := SaveIdentity(id, path); err != nil {
			t.Fatalf("SaveIdentity() returned error: %v", err)
		}

		loaded, err := LoadIdentity(path)
		if err != nil {
			t.Fatalf("LoadIdentity() returned error: %v", err)
		}

		if id.PeerID != loaded.PeerID {
			t.Errorf("PeerID mismatch: saved %s, loaded %s", id.PeerID, loaded.PeerID)
		}
	})

	t.Run("round-trip preserves key material", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "identity.key")

		id, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("GenerateIdentity() returned error: %v", err)
		}

		if err := SaveIdentity(id, path); err != nil {
			t.Fatalf("SaveIdentity() returned error: %v", err)
		}

		loaded, err := LoadIdentity(path)
		if err != nil {
			t.Fatalf("LoadIdentity() returned error: %v", err)
		}

		if loaded.PrivateKey == nil {
			t.Error("loaded PrivateKey is nil")
		}
		if loaded.PublicKey == nil {
			t.Error("loaded PublicKey is nil")
		}
		if !id.PrivateKey.Equals(loaded.PrivateKey) {
			t.Error("PrivateKey does not match after round-trip")
		}
		if !id.PublicKey.Equals(loaded.PublicKey) {
			t.Error("PublicKey does not match after round-trip")
		}
	})

	t.Run("save creates parent directories", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nested", "deep", "identity.key")

		id, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("GenerateIdentity() returned error: %v", err)
		}

		if err := SaveIdentity(id, path); err != nil {
			t.Fatalf("SaveIdentity() should create parent dirs, got error: %v", err)
		}

		// Verify the file actually exists
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Error("expected file to exist after SaveIdentity")
		}
	})

	t.Run("load from non-existent file returns error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "does-not-exist.key")

		_, err := LoadIdentity(path)
		if err == nil {
			t.Error("expected error when loading from non-existent file, got nil")
		}
	})

	t.Run("load from corrupted file returns error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "corrupted.key")

		// Write garbage bytes
		if err := os.WriteFile(path, []byte("this is not a valid key"), 0600); err != nil {
			t.Fatalf("failed to write corrupted file: %v", err)
		}

		_, err := LoadIdentity(path)
		if err == nil {
			t.Error("expected error when loading corrupted file, got nil")
		}
	})

	t.Run("load from empty file returns error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.key")

		if err := os.WriteFile(path, []byte{}, 0600); err != nil {
			t.Fatalf("failed to write empty file: %v", err)
		}

		_, err := LoadIdentity(path)
		if err == nil {
			t.Error("expected error when loading empty file, got nil")
		}
	})
}

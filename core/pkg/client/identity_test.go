package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/encryption"
)

func TestPersistentIdentity_NoPath(t *testing.T) {
	// Without IdentityPath, Connect() generates a random ID each time.
	// We can't easily test Connect() (needs network), so verify config defaults.
	cfg := DefaultClientConfig("test-app")
	if cfg.IdentityPath != "" {
		t.Fatalf("expected empty IdentityPath by default, got %q", cfg.IdentityPath)
	}
}

func TestPersistentIdentity_GenerateAndReload(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "identity.key")

	// 1. No file exists — generate + save
	id1, err := encryption.GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	if err := encryption.SaveIdentity(id1, keyPath); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	// File should exist
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Fatal("identity key file was not created")
	}

	// 2. Load it back — same PeerID
	id2, err := encryption.LoadIdentity(keyPath)
	if err != nil {
		t.Fatalf("LoadIdentity: %v", err)
	}
	if id1.PeerID != id2.PeerID {
		t.Fatalf("PeerID mismatch: generated %s, loaded %s", id1.PeerID, id2.PeerID)
	}

	// 3. Load again — still the same
	id3, err := encryption.LoadIdentity(keyPath)
	if err != nil {
		t.Fatalf("LoadIdentity (second): %v", err)
	}
	if id2.PeerID != id3.PeerID {
		t.Fatalf("PeerID changed across loads: %s vs %s", id2.PeerID, id3.PeerID)
	}
}

func TestPersistentIdentity_DifferentFromRandom(t *testing.T) {
	// A persistent identity should be different from a freshly generated one
	id1, err := encryption.GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	id2, err := encryption.GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	if id1.PeerID == id2.PeerID {
		t.Fatal("two independently generated identities should have different PeerIDs")
	}
}

func TestPersistentIdentity_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "subdir", "identity.key")

	id, err := encryption.GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	if err := encryption.SaveIdentity(id, keyPath); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Fatalf("expected file permissions 0600, got %o", perm)
	}
}

package gateway

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

// TestDeriveEd25519Seed_deterministic guards bug #215: every gateway in a
// cluster must end up with the same Ed25519 keypair so JWTs verify on any
// node. Same cluster secret + same purpose label MUST produce the same seed.
func TestDeriveEd25519Seed_deterministic(t *testing.T) {
	a, err := deriveEd25519Seed("super-secret-cluster-key")
	if err != nil {
		t.Fatalf("derive #1: %v", err)
	}
	b, err := deriveEd25519Seed("super-secret-cluster-key")
	if err != nil {
		t.Fatalf("derive #2: %v", err)
	}
	if len(a) != ed25519.SeedSize {
		t.Errorf("seed size = %d, want %d", len(a), ed25519.SeedSize)
	}
	if string(a) != string(b) {
		t.Error("seed not deterministic for same secret")
	}
}

// TestDeriveEd25519Seed_differentSecretsDifferentSeeds rules out a trivial
// implementation that ignores the input.
func TestDeriveEd25519Seed_differentSecretsDifferentSeeds(t *testing.T) {
	a, err := deriveEd25519Seed("secret-a")
	if err != nil {
		t.Fatalf("derive a: %v", err)
	}
	b, err := deriveEd25519Seed("secret-b")
	if err != nil {
		t.Fatalf("derive b: %v", err)
	}
	if string(a) == string(b) {
		t.Error("different secrets produced identical seed")
	}
}

func TestDeriveEd25519Seed_emptySecret(t *testing.T) {
	if _, err := deriveEd25519Seed(""); err == nil {
		t.Error("expected error for empty cluster secret, got nil")
	}
}

// TestLoadOrCreateEdSigningKey_clusterSecretSharedAcrossNodes simulates two
// gateways with separate dataDirs but the same cluster secret. They MUST end
// up with the same Ed25519 private key so JWTs verify cross-node.
func TestLoadOrCreateEdSigningKey_clusterSecretSharedAcrossNodes(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	const clusterSecret = "shared-cluster-secret-for-test"

	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)

	keyA, err := loadOrCreateEdSigningKey(dirA, clusterSecret, logger)
	if err != nil {
		t.Fatalf("node A: %v", err)
	}
	keyB, err := loadOrCreateEdSigningKey(dirB, clusterSecret, logger)
	if err != nil {
		t.Fatalf("node B: %v", err)
	}
	if !ed25519.PrivateKey(keyA).Equal(keyB) {
		t.Fatal("two nodes with same cluster secret produced different Ed25519 keys")
	}
	// PEMs should also be persisted.
	if _, err := os.Stat(filepath.Join(dirA, "secrets", eddsaKeyFileName)); err != nil {
		t.Errorf("PEM not written on node A: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirB, "secrets", eddsaKeyFileName)); err != nil {
		t.Errorf("PEM not written on node B: %v", err)
	}
}

// TestLoadOrCreateEdSigningKey_emptySecretFallback verifies the legacy
// per-node behaviour is preserved when no cluster secret is available
// (single-node test rigs, dev). Two nodes get DIFFERENT random keys.
func TestLoadOrCreateEdSigningKey_emptySecretFallback(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)

	keyA, err := loadOrCreateEdSigningKey(dirA, "", logger)
	if err != nil {
		t.Fatalf("node A: %v", err)
	}
	keyB, err := loadOrCreateEdSigningKey(dirB, "", logger)
	if err != nil {
		t.Fatalf("node B: %v", err)
	}
	if ed25519.PrivateKey(keyA).Equal(keyB) {
		t.Error("two nodes without cluster secret unexpectedly produced identical keys")
	}
}

// TestLoadOrCreateEdSigningKey_overwritesStaleOnDiskKey covers the upgrade
// path: a gateway that previously generated a per-node random key (fix #215
// not yet deployed) now restarts with cluster-secret derivation enabled. The
// random key on disk MUST be replaced with the canonical cluster-derived one.
func TestLoadOrCreateEdSigningKey_overwritesStaleOnDiskKey(t *testing.T) {
	dir := t.TempDir()
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)

	// First boot: no cluster secret -> per-node random key.
	keyV1, err := loadOrCreateEdSigningKey(dir, "", logger)
	if err != nil {
		t.Fatalf("v1: %v", err)
	}

	// Second boot: cluster secret now configured -> must rewrite to canonical.
	const clusterSecret = "now-i-have-a-secret"
	keyV2, err := loadOrCreateEdSigningKey(dir, clusterSecret, logger)
	if err != nil {
		t.Fatalf("v2: %v", err)
	}
	if ed25519.PrivateKey(keyV1).Equal(keyV2) {
		t.Fatal("stale per-node key was not replaced when cluster secret became available")
	}

	// And the new key must match a fresh derivation from the same secret.
	seed, err := deriveEd25519Seed(clusterSecret)
	if err != nil {
		t.Fatalf("derive seed: %v", err)
	}
	canonical := ed25519.NewKeyFromSeed(seed)
	if !ed25519.PrivateKey(keyV2).Equal(canonical) {
		t.Error("rewritten key does not match canonical derivation")
	}

	// Third boot with same secret: must be stable, no rewrite, same key.
	keyV3, err := loadOrCreateEdSigningKey(dir, clusterSecret, logger)
	if err != nil {
		t.Fatalf("v3: %v", err)
	}
	if !ed25519.PrivateKey(keyV2).Equal(keyV3) {
		t.Error("canonical key is not stable across restarts")
	}
}

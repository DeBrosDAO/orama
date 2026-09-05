package gateway

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

// The derivation is kept for one reason: tokens minted before every gateway
// got its own key carry the kid of the key it produced, and have to keep
// verifying across the upgrade. Nothing signs with it.
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

// The signing key is this gateway's own now, not one every node in the cluster
// can derive from a secret they all hold.

func TestLoadOrCreateEdSigningKey_isDifferentOnEveryGateway(t *testing.T) {
	logger := newSigningKeyLogger(t)

	first, err := loadOrCreateEdSigningKey(t.TempDir(), logger)
	if err != nil {
		t.Fatalf("first gateway: %v", err)
	}
	second, err := loadOrCreateEdSigningKey(t.TempDir(), logger)
	if err != nil {
		t.Fatalf("second gateway: %v", err)
	}

	if first.Equal(second) {
		t.Fatal("two gateways generated the same signing key, so either could mint the other's tokens")
	}
}

func TestLoadOrCreateEdSigningKey_survivesARestart(t *testing.T) {
	dir := t.TempDir()
	logger := newSigningKeyLogger(t)

	first, err := loadOrCreateEdSigningKey(dir, logger)
	if err != nil {
		t.Fatalf("first boot: %v", err)
	}
	second, err := loadOrCreateEdSigningKey(dir, logger)
	if err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if !first.Equal(second) {
		t.Error("a restart generated a new key, which invalidates every token already issued")
	}
}

func TestLoadOrCreateEdSigningKey_writesTheKeyUnreadableToAnyoneElse(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadOrCreateEdSigningKey(dir, newSigningKeyLogger(t)); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "secrets", eddsaKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("the signing key is mode %o, want 0600", perm)
	}
}

// Generating a replacement would silently invalidate every token this gateway
// has issued, and overwrite the only copy of a key that might be recoverable.
func TestLoadOrCreateEdSigningKey_refusesAnUnreadableKeyRatherThanReplacingIt(t *testing.T) {
	dir := t.TempDir()
	secrets := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secrets, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, eddsaKeyFileName), []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadOrCreateEdSigningKey(dir, newSigningKeyLogger(t)); err == nil {
		t.Fatal("an unreadable key was silently replaced")
	}
}

// The previous key has to keep verifying across the upgrade, and only across
// it: every node can derive it, so a node that kept accepting it could forge
// any namespace's tokens for ever.
func TestLegacyClusterSigningKey_isTheKeyEveryNodeUsedToDerive(t *testing.T) {
	const secret = "cluster-secret-for-the-test"

	pub, err := LegacyClusterSigningKey(secret)
	if err != nil {
		t.Fatalf("LegacyClusterSigningKey: %v", err)
	}

	seed, err := deriveEd25519Seed(secret)
	if err != nil {
		t.Fatal(err)
	}
	want := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if !pub.Equal(want) {
		t.Error("the legacy key is not the one tokens were signed with, so they will be refused after the upgrade")
	}

	if _, err := LegacyClusterSigningKey(""); err == nil {
		t.Error("a legacy key was derived from an empty cluster secret")
	}
}

// newSigningKeyLogger is the logger these tests pass in; nothing reads what it
// writes.
func newSigningKeyLogger(t *testing.T) *logging.ColoredLogger {
	t.Helper()
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	return logger
}

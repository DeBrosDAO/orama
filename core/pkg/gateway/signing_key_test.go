package gateway

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
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

	first, _, err := loadOrCreateEdSigningKey(t.TempDir(), "", logger)
	if err != nil {
		t.Fatalf("first gateway: %v", err)
	}
	second, _, err := loadOrCreateEdSigningKey(t.TempDir(), "", logger)
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

	first, _, err := loadOrCreateEdSigningKey(dir, "", logger)
	if err != nil {
		t.Fatalf("first boot: %v", err)
	}
	second, _, err := loadOrCreateEdSigningKey(dir, "", logger)
	if err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if !first.Equal(second) {
		t.Error("a restart generated a new key, which invalidates every token already issued")
	}
}

func TestLoadOrCreateEdSigningKey_writesTheKeyUnreadableToAnyoneElse(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := loadOrCreateEdSigningKey(dir, "", newSigningKeyLogger(t)); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, eddsaKeyFileName))
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
	if err := os.WriteFile(filepath.Join(dir, eddsaKeyFileName), []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := loadOrCreateEdSigningKey(dir, "", newSigningKeyLogger(t)); err == nil {
		t.Fatal("an unreadable key was silently replaced")
	}
}

// The index gateway and a tenant gateway on the same host used to resolve the
// same key file under <oramaDir>/secrets. Each resolves its own now, under its
// own namespace directory.
func TestLoadOrCreateEdSigningKey_indexAndTenantOnOneHostHaveTheirOwnFiles(t *testing.T) {
	nsBase := filepath.Join(t.TempDir(), "data", "namespaces")
	logger := newSigningKeyLogger(t)
	indexDir := constants.GatewayStateDir(nsBase, constants.IndexNamespace)
	tenantDir := constants.GatewayStateDir(nsBase, "acme")
	for _, dir := range []string{indexDir, tenantDir} {
		if err := ensureStateDir(dir); err != nil {
			t.Fatal(err)
		}
	}

	indexKey, _, err := loadOrCreateEdSigningKey(indexDir, "", logger)
	if err != nil {
		t.Fatalf("index gateway: %v", err)
	}
	tenantKey, _, err := loadOrCreateEdSigningKey(tenantDir, "", logger)
	if err != nil {
		t.Fatalf("tenant gateway: %v", err)
	}
	if indexKey.Equal(tenantKey) {
		t.Fatal("the index and a tenant gateway on one host sign with the same key")
	}
	for _, dir := range []string{indexDir, tenantDir} {
		if _, err := os.Stat(filepath.Join(dir, eddsaKeyFileName)); err != nil {
			t.Errorf("%s has no key of its own: %v", dir, err)
		}
	}
	if _, err := loadOrCreateSigningKey(indexDir, logger); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(indexDir, jwtKeyFileName)); err != nil {
		t.Errorf("the RSA key is not in the gateway's state directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tenantDir, jwtKeyFileName)); !os.IsNotExist(err) {
		t.Error("one gateway's RSA key landed in another's state directory")
	}
}

// A 0.122.x node's key file is the cluster-derived key, and the upgrade carries
// it into the index gateway's state directory. Loading it would sign with a
// key every node can compute; it is replaced instead.
func TestLoadOrCreateEdSigningKey_replacesTheClusterDerivedKey(t *testing.T) {
	const secret = "cluster-secret-for-the-test"
	dir := t.TempDir()
	seed, err := deriveEd25519Seed(secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.PersistSigningKey(dir, ed25519.NewKeyFromSeed(seed)); err != nil {
		t.Fatal(err)
	}

	key, migrated, err := loadOrCreateEdSigningKey(dir, secret, newSigningKeyLogger(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !migrated {
		t.Fatal("replacing the cluster-derived key did not report the migration")
	}
	legacy, _ := LegacyClusterSigningKey(secret)
	if legacy.Equal(key.Public()) {
		t.Fatal("the gateway signs with the key every node can derive from the cluster secret")
	}
	again, migrated, err := loadOrCreateEdSigningKey(dir, secret, newSigningKeyLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Fatal("loading the replacement reported another migration")
	}
	if !again.Equal(key) {
		t.Error("the replacement was not persisted; the next boot generated yet another key")
	}
}

// A key of the gateway's own is kept even when the cluster secret is known.
func TestLoadOrCreateEdSigningKey_keepsItsOwnKeyWithAClusterSecret(t *testing.T) {
	dir := t.TempDir()
	logger := newSigningKeyLogger(t)
	first, migrated, err := loadOrCreateEdSigningKey(dir, "cluster-secret", logger)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Fatal("generating a gateway's own key reported a migration")
	}
	second, migrated, err := loadOrCreateEdSigningKey(dir, "cluster-secret", logger)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Fatal("loading the gateway's own key reported a migration")
	}
	if !first.Equal(second) {
		t.Error("a gateway's own key was replaced on restart")
	}
}

// The index gateway is the control plane; its key signs for every namespace.
// Its client_namespace is "index", which used to be read as a tenant and bound
// its key to a namespace called "index".
func TestSigningKeyNamespace_indexAndLobbyAreUnbound(t *testing.T) {
	for _, ns := range []string{"", "  ", "default", constants.IndexNamespace} {
		if got := signingKeyNamespace(ns); got != "" {
			t.Errorf("signingKeyNamespace(%q) = %q, want unbound", ns, got)
		}
	}
	if got := signingKeyNamespace("acme"); got != "acme" {
		t.Errorf("a tenant gateway's key is bound to %q, want acme", got)
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

// The RSA loader generated a replacement for a key it could not parse, which
// the EdDSA loader refuses to do for the same reason.
func TestLoadOrCreateSigningKey_refusesAnUnreadableKeyRatherThanReplacingIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, jwtKeyFileName)
	if err := os.WriteFile(path, []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateSigningKey(dir, newSigningKeyLogger(t)); err == nil {
		t.Fatal("an unreadable key was silently replaced")
	}
	if got, _ := os.ReadFile(path); string(got) != "not a key" {
		t.Error("the unreadable key was overwritten")
	}
}

// Signing in without a namespace on the index gateway asked for a namespace
// called "index" and was refused NAMESPACE_UNKNOWN, and the cluster registry's
// raw-database guard stopped applying to the index gateway: both read the
// gateway's own namespace, which stopped being the lobby's name.
func TestOwnNamespace_theClusterGatewayIsTheLobby(t *testing.T) {
	for in, want := range map[string]string{
		"":                       auth.LobbyNamespace,
		constants.IndexNamespace: auth.LobbyNamespace,
		"default":                auth.LobbyNamespace,
		"acme":                   "acme",
	} {
		if got := ownNamespace(&Config{ClientNamespace: in}); got != want {
			t.Errorf("ownNamespace(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ownNamespace(nil); got != auth.LobbyNamespace {
		t.Errorf("ownNamespace(nil) = %q", got)
	}
}

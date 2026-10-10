package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gatewaykeys"
)

// credentialsFor lays out what systemd hands the index unit: both keys, made
// the way the installer makes them.
func credentialsFor(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := gatewaykeys.Ensure(root, func(string) ([]byte, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, constants.IndexNamespace)
}

func TestIndexSigningKey_isTheCredentialAndNeverRegenerated(t *testing.T) {
	cred := credentialsFor(t)
	state := t.TempDir()
	want, err := os.ReadFile(filepath.Join(cred, constants.GatewayRSAKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got, err := loadIndexSigningKey(cred, state)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("boot %d signed with a key that is not the credential", i)
		}
	}
	sink := func(string, []byte) error { t.Fatal("a restart stored a new key"); return nil }
	first, migrated, err := loadIndexEdSigningKey(cred, state, "", sink, newSigningKeyLogger(t))
	if err != nil || migrated {
		t.Fatalf("EdDSA key: migrated=%v err=%v", migrated, err)
	}
	second, _, err := loadIndexEdSigningKey(cred, state, "", sink, newSigningKeyLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(second) {
		t.Fatal("a restart minted a different EdDSA key")
	}
}

func TestIndexSigningKey_refusesToStartWithoutTheCredential(t *testing.T) {
	state := t.TempDir()
	sink := func(string, []byte) error { t.Fatal("generated a key without a credential"); return nil }

	if _, err := loadIndexSigningKey("", state); err == nil || !strings.Contains(err.Error(), "CREDENTIALS_DIRECTORY") {
		t.Fatalf("no credentials directory: %v", err)
	}
	if _, _, err := loadIndexEdSigningKey("", state, "", sink, newSigningKeyLogger(t)); err == nil || !strings.Contains(err.Error(), "orama node upgrade") {
		t.Fatalf("no credentials directory (EdDSA): %v", err)
	}
	empty := t.TempDir()
	if _, err := loadIndexSigningKey(empty, state); err == nil || !strings.Contains(err.Error(), constants.GatewayRSAKeyFileName) {
		t.Fatalf("missing credential: %v", err)
	}
	if _, _, err := loadIndexEdSigningKey(empty, state, "", sink, newSigningKeyLogger(t)); err == nil || !strings.Contains(err.Error(), constants.GatewayEdDSAKeyFileName) {
		t.Fatalf("missing credential (EdDSA): %v", err)
	}
	if _, err := os.Stat(filepath.Join(empty, constants.GatewayRSAKeyFileName)); !os.IsNotExist(err) {
		t.Fatalf("a key was written to the credentials directory: %v", err)
	}
}

func TestIndexSigningKey_removesTheCopyAnOlderBuildLeftInTheStateDirectory(t *testing.T) {
	cred := credentialsFor(t)
	state := t.TempDir()
	for _, name := range gatewaykeys.Names() {
		if err := os.WriteFile(filepath.Join(state, name), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := loadIndexSigningKey(cred, state); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadIndexEdSigningKey(cred, state, "", nil, newSigningKeyLogger(t)); err != nil {
		t.Fatal(err)
	}
	for _, name := range gatewaykeys.Names() {
		if _, err := os.Stat(filepath.Join(state, name)); !os.IsNotExist(err) {
			t.Errorf("%s is still in the state directory a tenant gateway can read: %v", name, err)
		}
	}
}

func TestIndexSigningKey_unreadableEdDSAKeyIsRefusedNotReplaced(t *testing.T) {
	cred := credentialsFor(t)
	if err := os.Chmod(filepath.Join(cred, constants.GatewayEdDSAKeyFileName), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cred, constants.GatewayEdDSAKeyFileName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	sink := func(string, []byte) error { t.Fatal("replaced an unreadable key"); return nil }
	if _, _, err := loadIndexEdSigningKey(cred, t.TempDir(), "", sink, newSigningKeyLogger(t)); err == nil {
		t.Fatal("started with an unreadable EdDSA key")
	}
}

func TestIndexSigningKey_clusterDerivedKeyIsReplacedAndStored(t *testing.T) {
	const secret = "cluster-secret-for-test"
	cred := credentialsFor(t)
	seed, err := deriveEd25519Seed(secret)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := marshalEdPrivateKey(ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(cred, constants.GatewayEdDSAKeyFileName), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cred, constants.GatewayEdDSAKeyFileName), derived, 0o600); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	sink := func(name string, pem []byte) error {
		if name != constants.GatewayEdDSAKeyFileName {
			t.Errorf("stored %s", name)
		}
		stored = pem
		return nil
	}
	key, migrated, err := loadIndexEdSigningKey(cred, t.TempDir(), secret, sink, newSigningKeyLogger(t))
	if err != nil || !migrated {
		t.Fatalf("migrated=%v err=%v", migrated, err)
	}
	if key.Equal(ed25519.NewKeyFromSeed(seed)) {
		t.Fatal("kept the cluster-derived key")
	}
	if len(stored) == 0 {
		t.Fatal("the replacement was not stored")
	}
}

// putInTree is what the privileged helper does for `gateway-key put`: it
// replaces the file in the root-only tree.
func putInTree(root string) func(string, []byte) error {
	return func(name string, pem []byte) error {
		return gatewaykeys.Write(root, constants.IndexNamespace, name, pem)
	}
}

// A rotation of the index gateway's key goes to the tree the credential is
// loaded from and nowhere in the state directory a tenant gateway can read; and
// the next boot signs with the rotated key, not the one it replaced.
func TestIndexRotation_storesInTheCredentialTreeAndSurvivesARestart(t *testing.T) {
	root := t.TempDir()
	if _, err := gatewaykeys.Ensure(root, func(string) ([]byte, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	cred := filepath.Join(root, constants.IndexNamespace)
	state := t.TempDir()

	boot, _, err := loadIndexEdSigningKey(cred, state, "", putInTree(root), newSigningKeyLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.NewService(nil, nil, "", "index")
	if err != nil {
		t.Fatal(err)
	}
	svc.SetEdDSAKey(boot, "")
	svc.SetKeyStore(indexKeyStore(putInTree(root)))

	next, err := svc.Rotate(context.Background())
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if next.KID == auth.KeyIDFor(boot.Public().(ed25519.PublicKey)) {
		t.Fatal("rotation kept the key")
	}
	if _, err := os.Stat(filepath.Join(state, eddsaKeyFileName)); !os.IsNotExist(err) {
		t.Errorf("the rotation left a key in the state directory a tenant gateway reads: %v", err)
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 0 {
		t.Errorf("state directory not empty after rotation: %v %v", entries, err)
	}

	restarted, migrated, err := loadIndexEdSigningKey(cred, state, "", putInTree(root), newSigningKeyLogger(t))
	if err != nil || migrated {
		t.Fatalf("restart: migrated=%v err=%v", migrated, err)
	}
	if auth.KeyIDFor(restarted.Public().(ed25519.PublicKey)) != next.KID {
		t.Fatal("after a restart the gateway signs with the key the rotation retired")
	}
	if info, err := os.Stat(filepath.Join(cred, eddsaKeyFileName)); err != nil || info.Mode().Perm() != 0o400 {
		t.Errorf("rotated key file: %v %v", info, err)
	}
}

func TestIndexKeyStore_reportsAFailedStoreAndNamesTheTree(t *testing.T) {
	store := indexKeyStore(func(string, []byte) error { return os.ErrPermission })
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Store(priv); err == nil || !strings.Contains(err.Error(), "replacement signing key") {
		t.Fatalf("store error: %v", err)
	}
	if !strings.HasPrefix(store.Where, gatewaykeys.Dir) {
		t.Errorf("Where = %q", store.Where)
	}
}

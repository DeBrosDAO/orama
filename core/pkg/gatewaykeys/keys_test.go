package gatewaykeys

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestWrite_rootOnlyAndReplaces(t *testing.T) {
	dir := t.TempDir()
	pem := []byte("-----BEGIN PRIVATE KEY-----\nYQ==\n-----END PRIVATE KEY-----\n")
	if err := Write(dir, constants.IndexNamespace, constants.GatewayRSAKeyFileName, pem); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, constants.IndexNamespace, constants.GatewayRSAKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(pem) {
		t.Fatalf("stored %q", got)
	}
	info, err := os.Stat(filepath.Join(dir, constants.IndexNamespace))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %o, want 0700", info.Mode().Perm())
	}
	info, err = os.Stat(filepath.Join(dir, constants.IndexNamespace, constants.GatewayRSAKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Errorf("file mode %o, want 0400", info.Mode().Perm())
	}
	if err := Write(dir, "alice", constants.GatewayRSAKeyFileName, pem); err == nil {
		t.Fatal("stored a tenant gateway's key in the index tree")
	}
	if err := Write(dir, constants.IndexNamespace, "id_rsa", pem); err == nil {
		t.Fatal("stored a file that is not a signing key")
	}
}

func noLegacy(string) ([]byte, error) { return nil, nil }

func TestEnsure_createsMissingKeysOnceAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	created, err := Ensure(dir, noLegacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("created %v, want both keys", created)
	}
	before := map[string][]byte{}
	for _, name := range Names() {
		path := filepath.Join(dir, constants.IndexNamespace, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o400 {
			t.Errorf("%s mode %o, want 0400", name, info.Mode().Perm())
		}
		if before[name], err = os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	created, err = Ensure(dir, func(string) ([]byte, error) { t.Fatal("read a legacy key for a key that exists"); return nil, nil })
	if err != nil || len(created) != 0 {
		t.Fatalf("second run created %v, err %v", created, err)
	}
	for _, name := range Names() {
		got, _ := os.ReadFile(filepath.Join(dir, constants.IndexNamespace, name))
		if string(got) != string(before[name]) {
			t.Errorf("%s was rewritten by a second run", name)
		}
	}
}

func TestEnsure_keepsAnExistingKeyAndCreatesOnlyTheMissingOne(t *testing.T) {
	dir := t.TempDir()
	existing := []byte("-----BEGIN RSA PRIVATE KEY-----\nYQ==\n-----END RSA PRIVATE KEY-----\n")
	if err := Write(dir, constants.IndexNamespace, constants.GatewayRSAKeyFileName, existing); err != nil {
		t.Fatal(err)
	}
	created, err := Ensure(dir, noLegacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0] != constants.GatewayEdDSAKeyFileName {
		t.Fatalf("created %v, want only the EdDSA key", created)
	}
	got, _ := os.ReadFile(filepath.Join(dir, constants.IndexNamespace, constants.GatewayRSAKeyFileName))
	if string(got) != string(existing) {
		t.Fatal("the existing RSA key was replaced")
	}
}

func TestEnsure_carriesTheStateDirectoryKeyForward(t *testing.T) {
	dir := t.TempDir()
	legacy := map[string][]byte{constants.GatewayRSAKeyFileName: []byte("legacy-rsa")}
	created, err := Ensure(dir, func(name string) ([]byte, error) { return legacy[name], nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("created %v", created)
	}
	got, _ := os.ReadFile(filepath.Join(dir, constants.IndexNamespace, constants.GatewayRSAKeyFileName))
	if string(got) != "legacy-rsa" {
		t.Fatalf("the migrated key is %q, want the one the old build was running with", got)
	}
	generated, _ := os.ReadFile(filepath.Join(dir, constants.IndexNamespace, constants.GatewayEdDSAKeyFileName))
	if len(generated) == 0 || string(generated) == "legacy-rsa" {
		t.Fatal("the key with no legacy copy was not generated")
	}
}

func TestEnsure_refusesWhatItCannotTrust(t *testing.T) {
	dir := t.TempDir()
	keyDir := filepath.Join(dir, constants.IndexNamespace)
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, constants.GatewayRSAKeyFileName), nil, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(dir, noLegacy); err == nil {
		t.Fatal("replaced an empty key file instead of refusing")
	}
	other := t.TempDir()
	if _, err := Ensure(other, func(string) ([]byte, error) { return nil, os.ErrPermission }); err == nil {
		t.Fatal("generated a key when the legacy copy could not be read")
	}
}

func TestGenerate_rejectsAnUnknownName(t *testing.T) {
	if _, err := Generate("id_rsa"); err == nil {
		t.Fatal("generated a key that is not a signing key")
	}
	for _, name := range Names() {
		if pem, err := Generate(name); err != nil || len(pem) == 0 || len(pem) > MaxPEM {
			t.Fatalf("Generate(%s): %d bytes, %v", name, len(pem), err)
		}
	}
}

package gatewaykeys

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestWrite_rootOnlyAndReplaces(t *testing.T) {
	dir := t.TempDir()
	pem := mustGenerate(t, constants.GatewayRSAKeyFileName)
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
	existing := mustGenerate(t, constants.GatewayRSAKeyFileName)
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
	legacyRSA := mustGenerate(t, constants.GatewayRSAKeyFileName)
	legacy := map[string][]byte{constants.GatewayRSAKeyFileName: legacyRSA}
	created, err := Ensure(dir, func(name string) ([]byte, error) { return legacy[name], nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("created %v", created)
	}
	got, _ := os.ReadFile(filepath.Join(dir, constants.IndexNamespace, constants.GatewayRSAKeyFileName))
	if string(got) != string(legacyRSA) {
		t.Fatalf("the migrated key is %q, want the one the old build was running with", got)
	}
	generated, _ := os.ReadFile(filepath.Join(dir, constants.IndexNamespace, constants.GatewayEdDSAKeyFileName))
	if len(generated) == 0 || string(generated) == string(legacyRSA) {
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

// TestWrite_refusesAKeyOfTheWrongKind: a put the gateway could not load would
// stop it at its next start, and Ensure never replaces a non-empty file.
func TestWrite_refusesAKeyOfTheWrongKind(t *testing.T) {
	dir := t.TempDir()
	rsaPEM, err := Generate(constants.GatewayRSAKeyFileName)
	if err != nil {
		t.Fatal(err)
	}
	edPEM, err := Generate(constants.GatewayEdDSAKeyFileName)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		file string
		pem  []byte
	}{
		"not PEM":               {constants.GatewayEdDSAKeyFileName, []byte("not a key")},
		"RSA under the Ed name": {constants.GatewayEdDSAKeyFileName, rsaPEM},
		"Ed under the RSA name": {constants.GatewayRSAKeyFileName, edPEM},
	} {
		if err := Write(dir, constants.IndexNamespace, c.file, c.pem); err == nil {
			t.Errorf("%s was written", name)
		}
	}
	for file, pem := range map[string][]byte{constants.GatewayRSAKeyFileName: rsaPEM, constants.GatewayEdDSAKeyFileName: edPEM} {
		if err := Write(dir, constants.IndexNamespace, file, pem); err != nil {
			t.Errorf("a valid %s was refused: %v", file, err)
		}
	}
}

func mustGenerate(t *testing.T, name string) []byte {
	t.Helper()
	pem, err := Generate(name)
	if err != nil {
		t.Fatal(err)
	}
	return pem
}

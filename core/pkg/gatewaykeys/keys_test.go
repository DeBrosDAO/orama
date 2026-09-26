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

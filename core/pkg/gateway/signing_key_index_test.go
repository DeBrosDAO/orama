package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestIndexSigningKey_isNotLeftWhereATenantGatewayCanReadIt(t *testing.T) {
	state := t.TempDir()
	cred := t.TempDir()
	stored := map[string][]byte{}
	sink := func(name string, pem []byte) error {
		stored[name] = append([]byte(nil), pem...)
		return os.WriteFile(filepath.Join(cred, name), pem, 0o400)
	}
	logger := newSigningKeyLogger(t)

	first, err := loadOrCreateIndexSigningKey("", state, sink, logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, constants.GatewayRSAKeyFileName)); !os.IsNotExist(err) {
		t.Fatalf("the RSA key is in the state directory: %v", err)
	}
	if len(stored[constants.GatewayRSAKeyFileName]) == 0 {
		t.Fatal("the RSA key was not sealed")
	}

	second, err := loadOrCreateIndexSigningKey(cred, state, sink, logger)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("a restart minted a different RSA key")
	}

	ed, migrated, err := loadOrCreateIndexEdSigningKey("", state, "", sink, logger)
	if err != nil {
		t.Fatal(err)
	}
	if migrated || ed == nil {
		t.Fatalf("fresh EdDSA key: migrated=%v key=%v", migrated, ed)
	}
	legacy := filepath.Join(state, constants.GatewayEdDSAKeyFileName)
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("the EdDSA key is in the state directory: %v", err)
	}
	// An older build left the key in the state directory. Once systemd is
	// handing over the sealed copy, that file has to go.
	if err := os.WriteFile(legacy, stored[constants.GatewayEdDSAKeyFileName], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadOrCreateIndexEdSigningKey(cred, state, "", sink, logger); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("the old EdDSA key is still in the state directory: %v", err)
	}
}

package boot

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// vaultKeyPath is the Ed25519 private key used as the vault identity.
// It lives on the unencrypted rootfs, next to WireGuard config, because
// the agent must present it before the LUKS volume is unlocked.
var vaultKeyPath = "/etc/orama/vault.ed25519"

type vaultIdentity struct {
	identity string
	pubHex   string
	priv     ed25519.PrivateKey
}

func loadOrCreateVaultIdentity() (*vaultIdentity, error) {
	if err := os.MkdirAll(filepath.Dir(vaultKeyPath), 0700); err != nil {
		return nil, fmt.Errorf("vault identity dir: %w", err)
	}

	data, err := os.ReadFile(vaultKeyPath)
	if err == nil {
		if len(data) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("vault identity file has length %d, want %d", len(data), ed25519.PrivateKeySize)
		}
		return identityFromPrivate(ed25519.PrivateKey(data)), nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read vault identity: %w", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate vault identity: %w", err)
	}
	if err := os.WriteFile(vaultKeyPath, priv, 0600); err != nil {
		return nil, fmt.Errorf("write vault identity: %w", err)
	}
	return identityFromPrivate(priv), nil
}

func identityFromPrivate(priv ed25519.PrivateKey) *vaultIdentity {
	pub := priv.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return &vaultIdentity{
		identity: hex.EncodeToString(sum[:]),
		pubHex:   hex.EncodeToString(pub),
		priv:     priv,
	}
}

func (id *vaultIdentity) sign(message string) string {
	return hex.EncodeToString(ed25519.Sign(id.priv, []byte(message)))
}

package gateway

import (
	"encoding/hex"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// secretsEncryptionDerivePurpose is the HKDF info label used to derive the
// function-secrets AES-256 key from the encryption root. Deriving it (instead
// of generating a per-node crypto/rand key file) guarantees every gateway in
// the cluster computes the IDENTICAL key — eliminating the key-divergence
// class that kept get_secret broken for days (bugboard #837).
//
// This label is a domain separator, not a rotation handle. Rotating stored
// secrets is `orama operator rotate-secrets --rotate`, which changes the IKM
// and re-encrypts. Editing this string orphans every stored function secret.
const secretsEncryptionDerivePurpose = "orama-secrets-encryption-v1"

// resolveSecretsEncryptionKeyHex returns the hex-encoded AES-256 key the
// serverless secrets manager should use to encrypt/decrypt function secrets.
//
// Primary: derive from the encryption-root IKM via HKDF (the caller passes
// that IKM; it starts as a copy of the cluster secret). TrimSpace so a stray
// newline cannot silently diverge keys across nodes (bugboard #837).
//
// Fallback: when no IKM is available (single-node test rigs / legacy
// deployments), fall back to an explicitly-configured key file. An empty
// result then makes the production secrets manager fail loud
// (NewDBSecretsManager with allowEphemeral=false), rather than silently using a
// per-process ephemeral key.
func resolveSecretsEncryptionKeyHex(clusterSecret, fileKeyHex string) (string, error) {
	if cs := strings.TrimSpace(clusterSecret); cs != "" {
		key, err := secrets.DeriveKey(cs, secretsEncryptionDerivePurpose)
		if err != nil {
			return "", err
		}
		return hex.EncodeToString(key), nil
	}
	return strings.TrimSpace(fileKeyHex), nil
}

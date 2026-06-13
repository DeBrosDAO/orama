package gateway

import (
	"encoding/hex"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// secretsEncryptionDerivePurpose is the HKDF info label used to derive the
// function-secrets AES-256 key from the cluster secret. Deriving it (instead of
// generating a per-node crypto/rand key file) guarantees every gateway in the
// cluster computes the IDENTICAL key, so a secret written on one node decrypts
// on every other node and survives rolling upgrades — eliminating the
// key-divergence / convergence-window class that kept get_secret broken for
// days (bugboard #837). Same pattern as the cluster-wide JWT signing key
// (jwtEdDSADerivePurpose) and the TURN encryption key ("turn-encryption").
//
// Bumping the version label (e.g. "...-v2") is a DELIBERATE rotation that
// invalidates every stored function secret (they must be re-`set`). It must
// never be changed casually.
const secretsEncryptionDerivePurpose = "orama-secrets-encryption-v1"

// resolveSecretsEncryptionKeyHex returns the hex-encoded AES-256 key the
// serverless secrets manager should use to encrypt/decrypt function secrets.
//
// Primary: derive deterministically from the cluster secret via HKDF, so the
// key is identical on every gateway in the cluster and stable across restarts
// and rolling upgrades. The cluster secret is TrimSpace'd first so a stray
// trailing newline on one node's secret file can't silently diverge its derived
// key from the rest of the cluster (the host gateway reads the file untrimmed
// while the namespace gateway trims it — without this they could derive
// different keys and reintroduce #837).
//
// Fallback: when no cluster secret is available (single-node test rigs / legacy
// deployments without a shared secret), fall back to an explicitly-configured
// key file. An empty result then makes the production secrets manager fail loud
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

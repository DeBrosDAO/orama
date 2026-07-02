package gateway

import (
	"encoding/hex"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// Bugboard #837 — the function-secrets AES key must be DERIVED from the cluster
// secret (not a per-node random file), so every gateway computes the identical
// key and stored secrets survive rolling upgrades. These pin the derivation.

func TestResolveSecretsEncryptionKeyHex_deterministic(t *testing.T) {
	// Same cluster secret → byte-identical key, every time. This is the whole
	// point: any gateway in the cluster derives the same key, so a secret set on
	// one node decrypts on all others.
	const cs = "cluster-secret-abc123"
	a, err := resolveSecretsEncryptionKeyHex(cs, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	b, err := resolveSecretsEncryptionKeyHex(cs, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if a == "" || a != b {
		t.Fatalf("derivation not deterministic: %q vs %q", a, b)
	}
	// Valid AES-256 key: 32 bytes = 64 hex chars.
	raw, err := hex.DecodeString(a)
	if err != nil || len(raw) != 32 {
		t.Errorf("derived key is not 32-byte hex: len(raw)=%d err=%v", len(raw), err)
	}
}

func TestResolveSecretsEncryptionKeyHex_trimInvariant(t *testing.T) {
	// A trailing newline on one node's cluster-secret file must NOT change the
	// derived key — otherwise the host gateway (reads untrimmed) and a namespace
	// gateway (reads trimmed) would diverge and reintroduce #837.
	trimmed, _ := resolveSecretsEncryptionKeyHex("cluster-secret-abc123", "")
	withNL, _ := resolveSecretsEncryptionKeyHex("cluster-secret-abc123\n", "")
	withSpaces, _ := resolveSecretsEncryptionKeyHex("  cluster-secret-abc123\t\n", "")
	if trimmed != withNL || trimmed != withSpaces {
		t.Errorf("derived key is not whitespace-invariant: %q / %q / %q", trimmed, withNL, withSpaces)
	}
}

func TestResolveSecretsEncryptionKeyHex_distinctSecretsDistinctKeys(t *testing.T) {
	a, _ := resolveSecretsEncryptionKeyHex("cluster-secret-A", "")
	b, _ := resolveSecretsEncryptionKeyHex("cluster-secret-B", "")
	if a == b {
		t.Errorf("distinct cluster secrets must derive distinct keys; both = %q", a)
	}
}

func TestResolveSecretsEncryptionKeyHex_purposeSeparatedFromTURN(t *testing.T) {
	// The secrets key must NOT equal the TURN key derived from the same cluster
	// secret — domain separation via the HKDF info label.
	const cs = "cluster-secret-abc123"
	secretsHex, _ := resolveSecretsEncryptionKeyHex(cs, "")
	turnKey, err := secrets.DeriveKey(cs, "turn-encryption")
	if err != nil {
		t.Fatalf("derive turn key: %v", err)
	}
	if secretsHex == hex.EncodeToString(turnKey) {
		t.Error("secrets key collides with the TURN key — HKDF purpose label not providing domain separation")
	}
}

func TestResolveSecretsEncryptionKeyHex_emptyClusterSecretUsesFileKey(t *testing.T) {
	// Legacy/test rigs with no cluster secret fall back to the explicitly
	// configured file key (trimmed).
	const fileKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := resolveSecretsEncryptionKeyHex("", fileKey+"\n")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != fileKey {
		t.Errorf("empty cluster secret should return the trimmed file key; got %q", got)
	}
}

func TestResolveSecretsEncryptionKeyHex_emptyBothReturnsEmpty(t *testing.T) {
	// No cluster secret AND no file key → empty result, which makes the
	// production secrets manager fail loud (allowEphemeral=false) instead of
	// silently using an ephemeral key.
	got, err := resolveSecretsEncryptionKeyHex("", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "" {
		t.Errorf("want empty result when neither source has a key; got %q", got)
	}
}

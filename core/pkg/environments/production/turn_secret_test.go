package production

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureTURNSecret_generatesAndPersists verifies that a fresh oramaDir
// produces a valid 32-byte hex secret written to secrets/turn-secret.
func TestEnsureTURNSecret_generatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	sg := NewSecretGenerator(dir)

	secret, err := sg.EnsureTURNSecret()
	if err != nil {
		t.Fatalf("EnsureTURNSecret failed: %v", err)
	}
	if len(secret) != 64 {
		t.Fatalf("expected 64 hex chars, got %d (%q)", len(secret), secret)
	}
	raw, err := hex.DecodeString(secret)
	if err != nil || len(raw) != 32 {
		t.Fatalf("secret is not 32 bytes hex: err=%v len=%d", err, len(raw))
	}

	data, err := os.ReadFile(filepath.Join(dir, "secrets", "turn-secret"))
	if err != nil {
		t.Fatalf("reading persisted secret failed: %v", err)
	}
	if strings.TrimSpace(string(data)) != secret {
		t.Errorf("persisted secret %q != returned secret %q", strings.TrimSpace(string(data)), secret)
	}
}

// TestEnsureTURNSecret_idempotent verifies the secret is stable across calls —
// the property that keeps TURN credentials valid across restarts and identical
// across cluster nodes (feat-124 #913).
func TestEnsureTURNSecret_idempotent(t *testing.T) {
	dir := t.TempDir()
	sg := NewSecretGenerator(dir)

	first, err := sg.EnsureTURNSecret()
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	second, err := sg.EnsureTURNSecret()
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if first != second {
		t.Errorf("secret changed between calls: %q != %q", first, second)
	}
}

// TestEnsureTURNSecret_regeneratesInvalid verifies a corrupt/short on-disk
// secret is replaced with a fresh valid one.
func TestEnsureTURNSecret_regeneratesInvalid(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secretsDir, "turn-secret"), []byte("too-short"), 0600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	sg := NewSecretGenerator(dir)
	secret, err := sg.EnsureTURNSecret()
	if err != nil {
		t.Fatalf("EnsureTURNSecret failed: %v", err)
	}
	if len(secret) != 64 {
		t.Errorf("expected regenerated 64-char secret, got %d (%q)", len(secret), secret)
	}
}

// writeNodeYAML is a test helper that writes content to the canonical node
// config path the config generator reads/writes.
func writeNodeYAML(t *testing.T, oramaDir, content string) {
	t.Helper()
	configDir := filepath.Join(oramaDir, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("mkdir configs failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "node.yaml"), []byte(content), 0644); err != nil {
		t.Fatalf("write node.yaml failed: %v", err)
	}
}

// TestGenerateNodeConfig_preservesExistingWebRTC is the regression test for the
// feat-124 #913 outage: a regen must NOT wipe an operator's webrtc block. We
// write a node.yaml with a full webrtc block, regenerate, and assert the block
// (enabled, sfu_port, turn_domain, turn_secret) survives — and that the secret
// gets persisted to the durable secrets file.
func TestGenerateNodeConfig_preservesExistingWebRTC(t *testing.T) {
	const turnSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const turnDomain = "turn.ns-anchat.dbrs.space"

	dir := t.TempDir()
	writeNodeYAML(t, dir, `http_gateway:
  enabled: true
  webrtc:
    enabled: true
    sfu_port: 30007
    turn_domain: "turn.ns-anchat.dbrs.space"
    turn_secret: "`+turnSecret+`"
`)

	cg := NewConfigGenerator(dir)
	out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "node-1.dbrs.space", "dbrs.space", false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig failed: %v", err)
	}

	for _, want := range []string{
		"webrtc:",
		"turn_secret: \"" + turnSecret + "\"",
		"turn_domain: \"" + turnDomain + "\"",
		"sfu_port: 30007",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("regenerated node.yaml missing %q\n---\n%s", want, out)
		}
	}

	// The secret must now be durable in the secrets file (yaml-had-secret →
	// file gets persisted), so the NEXT regen survives even if the operator's
	// yaml is gone.
	persisted, err := os.ReadFile(filepath.Join(dir, "secrets", "turn-secret"))
	if err != nil {
		t.Fatalf("TURN secret was not persisted to secrets dir: %v", err)
	}
	if strings.TrimSpace(string(persisted)) != turnSecret {
		t.Errorf("persisted secret %q != yaml secret %q", strings.TrimSpace(string(persisted)), turnSecret)
	}
}

// TestGenerateNodeConfig_persistedSecretSurvivesWipedYAML verifies the durable
// mechanism: once the secret is in secrets/turn-secret, a regen from a node.yaml
// that LOST its webrtc block still renders turn_secret (defaulting sfu_port).
func TestGenerateNodeConfig_persistedSecretSurvivesWipedYAML(t *testing.T) {
	const turnSecret = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		t.Fatalf("mkdir secrets failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secretsDir, "turn-secret"), []byte(turnSecret), 0600); err != nil {
		t.Fatalf("write turn-secret failed: %v", err)
	}
	// Existing node.yaml with NO webrtc block (simulates the wiped state).
	writeNodeYAML(t, dir, "http_gateway:\n  enabled: true\n")

	cg := NewConfigGenerator(dir)
	out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "node-1.dbrs.space", "dbrs.space", false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig failed: %v", err)
	}

	if !strings.Contains(out, "turn_secret: \""+turnSecret+"\"") {
		t.Errorf("rendered node.yaml missing persisted turn_secret\n---\n%s", out)
	}
	// sfu_port had no source → defaults to the named constant.
	if !strings.Contains(out, "sfu_port: 30000") {
		t.Errorf("expected default sfu_port 30000, got:\n%s", out)
	}
}

// TestGenerateNodeConfig_noWebRTCOmitsBlock verifies clusters without any TURN
// config render no webrtc block at all (no empty values leak in).
func TestGenerateNodeConfig_noWebRTCOmitsBlock(t *testing.T) {
	dir := t.TempDir()
	cg := NewConfigGenerator(dir)

	out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "node-1.dbrs.space", "dbrs.space", false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig failed: %v", err)
	}
	if strings.Contains(out, "webrtc:") {
		t.Errorf("expected no webrtc block when no TURN config present, got:\n%s", out)
	}
	// Sanity: ensure no orphan turn-secret file was created.
	if _, err := os.Stat(filepath.Join(dir, "secrets", "turn-secret")); !os.IsNotExist(err) {
		t.Errorf("turn-secret file should not exist when no TURN config present")
	}
}

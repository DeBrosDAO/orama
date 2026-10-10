package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

func TestCopyRedacted_agentLogMaskedMissingIsNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "agent.log")
	if err := os.WriteFile(src, []byte("unlocked with pw-agent-12345678\nready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.log")
	if err := copyRedacted(src, dst, secrets.NewRedactor("pw-agent-12345678").Redact); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(dst)
	if strings.Contains(string(raw), "pw-agent-12345678") || !strings.Contains(string(raw), "ready") {
		t.Fatalf("copy %q", raw)
	}
	if err := copyRedacted(filepath.Join(dir, "absent"), filepath.Join(dir, "x"), secrets.NewRedactor().Redact); err != nil {
		t.Fatal(err)
	}
}

// TestRedactText_sealedSecretsMaskedInRunnerOutput: an error the runner
// prints carries no secret, sealed or in the environment.
func TestRedactText_sealedSecretsMaskedInRunnerOutput(t *testing.T) {
	t.Setenv("E2E_FLEET_STATE", "")
	if err := secrets.Seal(map[string]string{"HCLOUD_TOKEN": "hc-printed-12345678"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { secrets.Unseal("HCLOUD_TOKEN") })
	if got := redactText("hetzner said hc-printed-12345678 Authorization: Bearer x-123456789"); strings.Contains(got, "hc-printed") || strings.Contains(got, "x-123456789") {
		t.Fatalf("got %q", got)
	}
}

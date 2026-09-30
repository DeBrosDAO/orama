package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// configuredEnvironment is a HOME whose only environment, "devnet", names a
// gateway nothing listens on, with no credential and no ORAMA_TOKEN.
func configuredEnvironment(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ORAMA_TOKEN", "")
	t.Setenv("ORAMA_API_URL", "")
	if err := os.MkdirAll(filepath.Join(home, ".orama"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, err := json.Marshal(map[string]any{
		"environments":       []map[string]string{{"name": "devnet", "gateway_url": "http://127.0.0.1:1"}},
		"active_environment": "devnet",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".orama", "environments.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Bug: with no login, migrate-conf looked for the node inventory first and
// failed with a message about nodes.conf, exiting 1. There is nothing to
// register nodes with, and that is an authentication failure.
func TestMigrateConf_noCredentialIsTheAuthExit(t *testing.T) {
	configuredEnvironment(t)
	migrateConfEnv = "devnet"
	t.Cleanup(func() { migrateConfEnv = "" })

	err := migrateConfCmd.RunE(migrateConfCmd, nil)
	if got := clierr.CodeOf(err); got != clierr.CodeAuth || !strings.Contains(err.Error(), "orama auth login") {
		t.Fatalf("exit code %d (%v), want %d and the login hint", got, err, clierr.CodeAuth)
	}
}

func TestMigrateConf_anEnvironmentThatIsNotConfiguredIsUsage(t *testing.T) {
	configuredEnvironment(t)
	migrateConfEnv = "e2e-cli-absent"
	t.Cleanup(func() { migrateConfEnv = "" })

	err := migrateConfCmd.RunE(migrateConfCmd, nil)
	if got := clierr.CodeOf(err); got != clierr.CodeUsage || !strings.Contains(err.Error(), "e2e-cli-absent") {
		t.Fatalf("exit code %d (%v), want %d naming the environment", got, err, clierr.CodeUsage)
	}
}

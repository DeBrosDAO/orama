package namespace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Bugboard #160 fix: SpawnGateway must forward the host's API-key HMAC
// secret into the spawned namespace gateway's YAML so it hashes/verifies
// API keys the same way the main gateway does. These tests exercise the
// read-and-forward step directly; the systemd start that follows it is not
// available in this test sandbox, so SpawnGateway is expected to return a
// (systemd) error in the "present" case too — what matters is that the
// config file was already written, with the secret, before that happens.

// setupOramaDirs creates "<root>/secrets" and "<root>/data/namespaces" and
// returns the namespaceBase path SystemdSpawner expects, mirroring the
// convention every production caller uses (pkg/node/gateway.go's
// baseDataDir, ClusterManagerConfig.BaseDataDir).
func setupOramaDirs(t *testing.T) (root, namespaceBase string) {
	t.Helper()
	root = t.TempDir()
	namespaceBase = filepath.Join(root, "data", "namespaces")
	if err := os.MkdirAll(namespaceBase, 0755); err != nil {
		t.Fatalf("mkdir namespaceBase: %v", err)
	}
	return root, namespaceBase
}

func writeAPIKeyHMACSecret(t *testing.T, root, contents string) string {
	t.Helper()
	secretsDir := filepath.Join(root, "secrets")
	if err := os.MkdirAll(secretsDir, 0755); err != nil {
		t.Fatalf("mkdir secrets dir: %v", err)
	}
	path := filepath.Join(secretsDir, apiKeyHMACSecretFileName)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	return path
}

func TestSpawnGateway_apiKeyHMACSecretPresent_renderedYAMLContainsSecret(t *testing.T) {
	withOverlayIP(t, "10.0.0.5", nil)
	root, namespaceBase := setupOramaDirs(t)
	// Trailing whitespace/newline must be trimmed, same as the main gateway
	// (pkg/node/gateway.go:52).
	writeAPIKeyHMACSecret(t, root, "the-hmac-secret\n")

	s := NewSystemdSpawner(namespaceBase, "", zap.NewNop())
	cfg := gateway.InstanceConfig{Namespace: "anchat-test", HTTPPort: 6101}

	// Ignore the return: systemd isn't available in this sandbox, so
	// StartService fails after the config is written. That's fine — we only
	// need the config file it produced.
	_ = s.SpawnGateway(context.Background(), "anchat-test", "node-1", cfg)

	configPath := filepath.Join(namespaceBase, "anchat-test", "configs", "gateway-node-1.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected gateway config to be written before the systemd error, got: %v", err)
	}

	var onDisk gateway.GatewayYAMLConfig
	if err := yaml.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("unmarshal rendered gateway config: %v", err)
	}
	if onDisk.APIKeyHMACSecret != "the-hmac-secret" {
		t.Errorf("APIKeyHMACSecret = %q, want %q", onDisk.APIKeyHMACSecret, "the-hmac-secret")
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("gateway config file mode = %o, want 0600 (it now embeds the API-key HMAC secret)", mode)
	}
}

func TestSpawnGateway_apiKeyHMACSecretMissing_returnsErrorNoConfigEmitted(t *testing.T) {
	// No secrets/api-key-hmac-secret file is created — the namespace gateway
	// has no way to authenticate anything, so SpawnGateway must fail loud
	// instead of silently writing a config with an empty secret.
	_, namespaceBase := setupOramaDirs(t)

	s := NewSystemdSpawner(namespaceBase, "", zap.NewNop())
	cfg := gateway.InstanceConfig{Namespace: "anchat-test", HTTPPort: 6101}

	err := s.SpawnGateway(context.Background(), "anchat-test", "node-1", cfg)
	if err == nil {
		t.Fatal("expected an error when the API-key HMAC secret file is missing")
	}
	if !strings.Contains(err.Error(), apiKeyHMACSecretFileName) {
		t.Errorf("error %q does not mention the secret path/file", err.Error())
	}

	configPath := filepath.Join(namespaceBase, "anchat-test", "configs", "gateway-node-1.yaml")
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Errorf("expected no gateway config to be written when the secret is missing, stat err = %v", statErr)
	}
}

func TestSpawnGateway_apiKeyHMACSecretWhitespaceOnly_returnsErrorNoConfigEmitted(t *testing.T) {
	// A secret file containing only whitespace must be treated as empty and
	// rejected — otherwise the namespace gateway would silently boot unable
	// to authenticate anything.
	root, namespaceBase := setupOramaDirs(t)
	writeAPIKeyHMACSecret(t, root, "   \n\t  ")

	s := NewSystemdSpawner(namespaceBase, "", zap.NewNop())
	cfg := gateway.InstanceConfig{Namespace: "anchat-test", HTTPPort: 6101}

	err := s.SpawnGateway(context.Background(), "anchat-test", "node-1", cfg)
	if err == nil {
		t.Fatal("expected an error when the API-key HMAC secret file is whitespace-only")
	}

	configPath := filepath.Join(namespaceBase, "anchat-test", "configs", "gateway-node-1.yaml")
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Errorf("expected no gateway config to be written when the secret is whitespace-only, stat err = %v", statErr)
	}
}

func TestSystemdSpawner_oramaDir_derivesFromNamespaceBase(t *testing.T) {
	s := &SystemdSpawner{namespaceBase: filepath.Join("/opt/orama", "data", "namespaces")}
	want := filepath.Clean("/opt/orama")
	if got := s.oramaDir(); got != want {
		t.Errorf("oramaDir() = %q, want %q", got, want)
	}
}

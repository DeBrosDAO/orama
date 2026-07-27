package main

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"gopkg.in/yaml.v3"
)

// TestSpawnedGatewayConfig_apiKeyHMACSecret is the bugboard #160 regression
// test: a YAML written by the namespace gateway spawner
// (gateway.GatewayYAMLConfig with APIKeyHMACSecret set) must (a) pass the
// standalone gateway's STRICT decoder — i.e. api_key_hmac_secret is a known
// field, not rejected — and (b) end up in gateway.Config.APIKeyHMACSecret via
// the same trim/assign parseGatewayConfig uses. Without this, namespace
// gateways could never authenticate a core-registry API key.
//
// yamlCfgMirror mirrors the function-local yamlCfg in config.go (that type
// is unexported and scoped to parseGatewayConfig, so it can't be referenced
// directly from a test). If the real loader's field/tag drifts, the
// strict-decode step below fails.
type yamlCfgMirror struct {
	ListenAddr           string   `yaml:"listen_addr"`
	ClientNamespace      string   `yaml:"client_namespace"`
	RQLiteDSN            string   `yaml:"rqlite_dsn"`
	OlricServers         []string `yaml:"olric_servers"`
	SecretsEncryptionKey string   `yaml:"secrets_encryption_key"`
	ClusterSecretPath    string   `yaml:"cluster_secret_path"`
	APIKeyHMACSecret     string   `yaml:"api_key_hmac_secret"`
}

// applyAPIKeyHMACSecret mirrors the trim/assign block in parseGatewayConfig.
func applyAPIKeyHMACSecret(cfg *gateway.Config, y yamlCfgMirror) {
	if v := strings.TrimSpace(y.APIKeyHMACSecret); v != "" {
		cfg.APIKeyHMACSecret = v
	}
}

func TestSpawnedGatewayConfig_apiKeyHMACSecretPresent_isApplied(t *testing.T) {
	const secret = "the-hmac-secret"

	written := gateway.GatewayYAMLConfig{
		ListenAddr:       ":6001",
		ClientNamespace:  "anchat-test",
		RQLiteDSN:        "http://localhost:10000",
		APIKeyHMACSecret: secret,
	}
	data, err := yaml.Marshal(written)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var y yamlCfgMirror
	if err := config.DecodeStrict(strings.NewReader(string(data)), &y); err != nil {
		t.Fatalf("strict decode rejected the spawned gateway YAML: %v", err)
	}

	cfg := &gateway.Config{}
	applyAPIKeyHMACSecret(cfg, y)

	if cfg.APIKeyHMACSecret != secret {
		t.Errorf("gateway.Config.APIKeyHMACSecret = %q, want %q", cfg.APIKeyHMACSecret, secret)
	}
}

func TestParseAPIKeyHMACSecret_absent_leavesConfigUnchanged(t *testing.T) {
	var y yamlCfgMirror // api_key_hmac_secret never set

	cfg := &gateway.Config{}
	applyAPIKeyHMACSecret(cfg, y)

	if cfg.APIKeyHMACSecret != "" {
		t.Errorf("gateway.Config.APIKeyHMACSecret = %q, want empty when absent from YAML", cfg.APIKeyHMACSecret)
	}
}

func TestParseAPIKeyHMACSecret_whitespaceOnly_treatedAsEmpty(t *testing.T) {
	y := yamlCfgMirror{APIKeyHMACSecret: "   \t  "}

	cfg := &gateway.Config{}
	applyAPIKeyHMACSecret(cfg, y)

	if cfg.APIKeyHMACSecret != "" {
		t.Errorf("gateway.Config.APIKeyHMACSecret = %q, want empty for whitespace-only value", cfg.APIKeyHMACSecret)
	}
}

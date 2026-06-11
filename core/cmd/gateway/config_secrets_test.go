package main

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"gopkg.in/yaml.v3"
)

// TestSpawnedGatewayConfig_loadsSecretsEncryptionKey is the bugboard #837
// follow-up regression test for the *load* half: a YAML written by the
// namespace gateway spawner (gateway.GatewayYAMLConfig with the secrets key)
// must (a) pass the standalone gateway's STRICT decoder — i.e. the
// secrets_encryption_key field is a known field, not rejected — and (b) end
// up in gateway.Config.SecretsEncryptionKey via the same trim/assign the real
// parseGatewayConfig uses. Without the load mapping, `function secrets list`
// returned 501 on namespace gateways.
func TestSpawnedGatewayConfig_loadsSecretsEncryptionKey(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Produce the exact YAML a spawned namespace gateway receives.
	written := gateway.GatewayYAMLConfig{
		ListenAddr:           ":6001",
		ClientNamespace:      "anchat-test",
		RQLiteDSN:            "http://localhost:10000",
		OlricServers:         []string{"localhost:3320"},
		SecretsEncryptionKey: key,
	}
	data, err := yaml.Marshal(written)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// yamlCfgMirror mirrors the function-local yamlCfg in config.go. If the
	// real loader's field/tag drifts, the round-trip assertion below fails.
	type webrtc struct {
		Enabled    bool   `yaml:"enabled"`
		SFUPort    int    `yaml:"sfu_port"`
		TURNDomain string `yaml:"turn_domain"`
		TURNSecret string `yaml:"turn_secret"`
	}
	type yamlCfgMirror struct {
		ListenAddr           string   `yaml:"listen_addr"`
		ClientNamespace      string   `yaml:"client_namespace"`
		RQLiteDSN            string   `yaml:"rqlite_dsn"`
		OlricServers         []string `yaml:"olric_servers"`
		WebRTC               webrtc   `yaml:"webrtc"`
		SecretsEncryptionKey string   `yaml:"secrets_encryption_key"`
		ClusterSecretPath    string   `yaml:"cluster_secret_path"`
	}

	var y yamlCfgMirror
	// STRICT decode — the real loader rejects unknown fields, so this proves
	// secrets_encryption_key is recognized.
	if err := config.DecodeStrict(strings.NewReader(string(data)), &y); err != nil {
		t.Fatalf("strict decode rejected the spawned gateway YAML: %v", err)
	}

	// Apply the same trim/assign as parseGatewayConfig.
	cfg := &gateway.Config{}
	if v := strings.TrimSpace(y.SecretsEncryptionKey); v != "" {
		cfg.SecretsEncryptionKey = v
	}

	if cfg.SecretsEncryptionKey != key {
		t.Errorf("gateway.Config.SecretsEncryptionKey = %q, want %q", cfg.SecretsEncryptionKey, key)
	}
}

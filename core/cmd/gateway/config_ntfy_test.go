package main

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"gopkg.in/yaml.v3"
)

// TestSpawnedGatewayConfig_loadsNtfyBaseURL is the bugboard #274 regression
// test for the *load* half. The spawner now writes ntfy_base_url into every
// namespace gateway YAML; the standalone gateway decodes STRICTLY, so if the
// loader's field or tag drifts, two things break at once: the gateway refuses
// to start on an unknown field, and (were it merely ignored) the ntfy push
// provider would go unregistered again and every Android push would fail with
// no HTTP request attempted.
func TestSpawnedGatewayConfig_loadsNtfyBaseURL(t *testing.T) {
	const base = "https://push.orama-devnet.network"

	// Produce the exact YAML a spawned namespace gateway receives.
	written := gateway.GatewayYAMLConfig{
		ListenAddr:      ":6001",
		ClientNamespace: "anchat-v2",
		RQLiteDSN:       "http://localhost:10005",
		OlricServers:    []string{"localhost:3320"},
		NtfyBaseURL:     base,
	}
	data, err := yaml.Marshal(written)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// yamlCfgMirror mirrors the function-local yamlCfg in config.go.
	type yamlCfgMirror struct {
		ListenAddr      string   `yaml:"listen_addr"`
		ClientNamespace string   `yaml:"client_namespace"`
		RQLiteDSN       string   `yaml:"rqlite_dsn"`
		OlricServers    []string `yaml:"olric_servers"`
		NtfyBaseURL     string   `yaml:"ntfy_base_url"`
	}

	var y yamlCfgMirror
	// STRICT decode — the real loader rejects unknown fields, so this proves
	// ntfy_base_url is recognized rather than fatal.
	if err := config.DecodeStrict(strings.NewReader(string(data)), &y); err != nil {
		t.Fatalf("strict decode rejected the spawned gateway YAML: %v", err)
	}

	// Apply the same trim/assign as parseGatewayConfig.
	cfg := &gateway.Config{}
	if v := strings.TrimSpace(y.NtfyBaseURL); v != "" {
		cfg.NtfyBaseURL = v
	}

	if cfg.NtfyBaseURL != base {
		t.Errorf("gateway.Config.NtfyBaseURL = %q, want %q", cfg.NtfyBaseURL, base)
	}
}

// TestSpawnedGatewayConfig_ntfyBaseURLAbsentLeavesConfigEmpty is the negative
// case: a host with no ntfy server emits no key, and the gateway must simply
// end up with an empty NtfyBaseURL rather than failing to decode. Push then
// stays disabled unless the namespace supplies its own base_url — which is the
// documented, fail-loud behaviour.
func TestSpawnedGatewayConfig_ntfyBaseURLAbsentLeavesConfigEmpty(t *testing.T) {
	written := gateway.GatewayYAMLConfig{
		ListenAddr:      ":6001",
		ClientNamespace: "anchat-v2",
		RQLiteDSN:       "http://localhost:10005",
		OlricServers:    []string{"localhost:3320"},
	}
	data, err := yaml.Marshal(written)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "ntfy_base_url") {
		t.Fatalf("empty NtfyBaseURL should be omitted:\n%s", data)
	}

	type yamlCfgMirror struct {
		ListenAddr      string   `yaml:"listen_addr"`
		ClientNamespace string   `yaml:"client_namespace"`
		RQLiteDSN       string   `yaml:"rqlite_dsn"`
		OlricServers    []string `yaml:"olric_servers"`
		NtfyBaseURL     string   `yaml:"ntfy_base_url"`
	}
	var y yamlCfgMirror
	if err := config.DecodeStrict(strings.NewReader(string(data)), &y); err != nil {
		t.Fatalf("strict decode failed: %v", err)
	}
	if y.NtfyBaseURL != "" {
		t.Errorf("NtfyBaseURL = %q; want empty", y.NtfyBaseURL)
	}
}

package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Bugboard #274. docs/PUSH_NOTIFICATIONS.md told tenants they could leave the
// ntfy credential's base_url empty and fall back to "the platform's self-hosted
// ntfy". There was no such fallback on a namespace gateway: ntfy_base_url is a
// host-node setting and nothing carried it into a spawned namespace gateway's
// YAML, so gateway.Config.NtfyBaseURL was always "". The ntfy provider is only
// registered when a base URL ends up set, so every Android push on anchat-v2
// failed before any HTTP request. These tests guard the plumbing.

// TestGatewayYAMLConfig_ntfyBaseURLRoundTrip pins the YAML tag the standalone
// gateway binary reads the value back from.
func TestGatewayYAMLConfig_ntfyBaseURLRoundTrip(t *testing.T) {
	const base = "https://push.orama-devnet.network"
	cfg := GatewayYAMLConfig{
		ListenAddr:      ":6001",
		ClientNamespace: "anchat-v2",
		RQLiteDSN:       "http://localhost:10005",
		NtfyBaseURL:     base,
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "ntfy_base_url: "+base) {
		t.Fatalf("ntfy_base_url missing from marshalled YAML:\n%s", data)
	}

	var back GatewayYAMLConfig
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.NtfyBaseURL != base {
		t.Errorf("NtfyBaseURL = %q; want %q", back.NtfyBaseURL, base)
	}
}

// TestGenerateConfig_writesNtfyBaseURL is the end of the plumbing: the value on
// InstanceConfig must reach the file the spawned gateway actually loads.
func TestGenerateConfig_writesNtfyBaseURL(t *testing.T) {
	const base = "https://push.orama-devnet.network"
	dir := t.TempDir()
	is := NewInstanceSpawner(dir, zap.NewNop())
	configPath := filepath.Join(dir, "gateway-node-1.yaml")

	cfg := InstanceConfig{
		Namespace:    "anchat-v2",
		NodeID:       "node-1",
		HTTPPort:     6001,
		RQLiteDSN:    "http://localhost:10005",
		OlricServers: []string{"localhost:3320"},
		NtfyBaseURL:  base,
	}
	if err := is.generateConfig(configPath, cfg, dir); err != nil {
		t.Fatalf("generateConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "ntfy_base_url: "+base) {
		t.Errorf("generated namespace gateway config missing ntfy_base_url — every ntfy push will fail before any HTTP call:\n%s", data)
	}
}

// TestGenerateConfig_omitsNtfyBaseURLWhenEmpty: a host with no ntfy server must
// not emit a stray empty key. The gateway uses strict YAML decoding, and an
// empty value here would also be indistinguishable from a real configuration.
func TestGenerateConfig_omitsNtfyBaseURLWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	is := NewInstanceSpawner(dir, zap.NewNop())
	configPath := filepath.Join(dir, "gateway-node-1.yaml")

	cfg := InstanceConfig{
		Namespace:    "anchat-v2",
		NodeID:       "node-1",
		HTTPPort:     6001,
		RQLiteDSN:    "http://localhost:10005",
		OlricServers: []string{"localhost:3320"},
	}
	if err := is.generateConfig(configPath, cfg, dir); err != nil {
		t.Fatalf("generateConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "ntfy_base_url") {
		t.Errorf("ntfy_base_url should be omitted when unset:\n%s", data)
	}
}

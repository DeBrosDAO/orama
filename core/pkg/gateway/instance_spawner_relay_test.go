package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// relay_allowed_suffixes is read from node.yaml by the node, handed to the
// index gateway's spawner and written into the YAML that gateway loads
// (bugboard #266). Only the index gateway serves /v1/proxy/relay.
func TestGenerateConfig_writesRelayAllowedSuffixes(t *testing.T) {
	dir := t.TempDir()
	is := NewInstanceSpawner(dir, zap.NewNop())
	path := filepath.Join(dir, "gateway-index.yaml")
	cfg := InstanceConfig{
		Namespace: "index", NodeID: "node-1", HTTPPort: 6001, RQLiteDSN: "http://localhost:10005",
		OlricServers:         []string{"localhost:3320"},
		RelayAllowedSuffixes: []string{"partner.example", "other.example.org"},
	}
	if err := is.generateConfig(path, cfg, dir); err != nil {
		t.Fatalf("generateConfig: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"relay_allowed_suffixes:", "- partner.example", "- other.example.org"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the generated config lacks %q:\n%s", want, data)
		}
	}
}

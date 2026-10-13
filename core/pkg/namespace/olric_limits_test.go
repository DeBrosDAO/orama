package namespace

import (
	"os"
	"path/filepath"
	"testing"

	oramaolric "github.com/DeBrosOfficial/network/pkg/olric"
	olricconfig "github.com/olric-data/olric/config"
	"gopkg.in/yaml.v3"
)

// The namespace Olric config, written out and read back by olric-server's own
// loader, bounds every DMap with LRU eviction. Olric's default was no eviction
// and no limit, so a cache in use grew until systemd killed it.
func TestBuildOlricConfig_boundsEveryDMapWithLRU(t *testing.T) {
	cfg := buildOlricConfig(oramaolric.InstanceConfig{BindAddr: "10.0.0.1", HTTPPort: 10002, MemberlistPort: 10003, PeerAddresses: []string{"10.0.0.2:10003"}})
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "olric.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := olricconfig.Load(path)
	if err != nil {
		t.Fatalf("olric-server would not load the config: %v\n%s", err, data)
	}
	if loaded.DMaps.EvictionPolicy != olricconfig.LRUEviction || loaded.DMaps.MaxInuse != oramaolric.DMapMaxInuseBytes {
		t.Fatalf("olric reads eviction %q, maxInuse %d; want LRU, %d", loaded.DMaps.EvictionPolicy, loaded.DMaps.MaxInuse, oramaolric.DMapMaxInuseBytes)
	}
}

// A config on disk from before the limit is drift: the reconcile rewrites it.
func TestOlricConfigInSync_missingLimitIsDrift(t *testing.T) {
	desired := buildOlricConfig(oramaolric.InstanceConfig{BindAddr: "10.0.0.1", HTTPPort: 10002, MemberlistPort: 10003})
	old := desired
	old.DMaps = olricDMapsConfig{}
	if olricConfigInSync(old, desired) {
		t.Fatal("a config without the DMap limit reads as in sync, so it would never be rewritten")
	}
}

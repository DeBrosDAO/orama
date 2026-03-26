package discovery

import (
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	cfg := Config{DiscoveryInterval: 5 * time.Second, MaxConnections: 3}
	if cfg.DiscoveryInterval <= 0 || cfg.MaxConnections <= 0 {
		t.Fatalf("invalid config: %+v", cfg)
	}
}

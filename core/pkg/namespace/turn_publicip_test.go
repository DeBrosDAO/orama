package namespace

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// TestSpawnTURN_emptyPublicIP_refuses is the regression guard for bugboard #846:
// the boot-time TURN restore path used to pass an empty PublicIP, which produced
// a TURN config that crash-loops on "turn.public_ip: must not be empty". A
// crash-looped TURN gives zero ICE relay candidates AND prevents SpawnTURN from
// ever reaching AddWebRTCRules (so relay ports stay firewalled). SpawnTURN must
// reject an empty public_ip loudly — before writing any config or touching
// systemd — so the failure is visible instead of an invisible crash-loop.
func TestSpawnTURN_emptyPublicIP_refuses(t *testing.T) {
	s := &SystemdSpawner{namespaceBase: t.TempDir(), logger: zap.NewNop()}

	err := s.SpawnTURN(context.Background(), "test-ns", "node-1", TURNInstanceConfig{
		Namespace:      "test-ns",
		ListenAddr:     "0.0.0.0:3478",
		PublicIP:       "", // the bug: empty public IP
		Realm:          "example.net",
		AuthSecret:     "secret",
		RelayPortStart: 49152,
		RelayPortEnd:   49951,
	})
	if err == nil {
		t.Fatal("expected SpawnTURN to refuse an empty public_ip, got nil")
	}
	if !strings.Contains(err.Error(), "public_ip is empty") {
		t.Errorf("expected a clear empty-public_ip error, got: %v", err)
	}
}

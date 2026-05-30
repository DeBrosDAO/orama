package namespace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Bugboard #25 (warm reconcile) — gatewayWebRTCInSync decides whether a
// running namespace gateway's on-disk WebRTC block already matches the
// desired config. ReconcileGateway restarts the gateway ONLY when this
// returns false, so the function is the guard against both (a) leaving a
// drifted gateway broken and (b) restart-looping a correct one on every
// boot.

func desiredEnabled() gateway.InstanceConfig {
	return gateway.InstanceConfig{
		WebRTCEnabled: true,
		SFUPort:       30000,
		TURNDomain:    "turn.ns-anchat-test.orama-devnet.network",
		TURNSecret:    "the-secret",
	}
}

func TestGatewayWebRTCInSync_driftedBlockMissing_returnsFalse(t *testing.T) {
	// The exact bug-25 warm case: the running config has NO webrtc block
	// (enabled=false, port 0, empty secret) but the DB-desired config has
	// it enabled. MUST report out-of-sync so ReconcileGateway restarts.
	onDisk := gateway.GatewayYAMLWebRTC{} // zero value = no block
	if gatewayWebRTCInSync(onDisk, desiredEnabled()) {
		t.Fatal("BUG #25 REGRESSION: empty on-disk block vs DB-enabled desired must be out-of-sync (needs restart)")
	}
}

func TestGatewayWebRTCInSync_matchingBlock_returnsTrue(t *testing.T) {
	// After a reconcile fixes the config, the on-disk block matches the
	// desired. MUST report in-sync so the NEXT boot does NOT restart again
	// (no restart loop — this is why we compare the actual on-disk config
	// instead of the stale state file).
	onDisk := gateway.GatewayYAMLWebRTC{
		Enabled:    true,
		SFUPort:    30000,
		TURNDomain: "turn.ns-anchat-test.orama-devnet.network",
		TURNSecret: "the-secret",
	}
	if !gatewayWebRTCInSync(onDisk, desiredEnabled()) {
		t.Error("matching on-disk block must be in-sync (no restart) — else restart loop on every boot")
	}
}

func TestGatewayWebRTCInSync_eachFieldDriftDetected(t *testing.T) {
	// Any single drifted field must trigger a restart. Pins that the
	// comparison covers all four webrtc fields (a future refactor that
	// drops one would silently let that field drift forever).
	base := gateway.GatewayYAMLWebRTC{
		Enabled: true, SFUPort: 30000,
		TURNDomain: "turn.ns-anchat-test.orama-devnet.network", TURNSecret: "the-secret",
	}
	mutations := []struct {
		name string
		mut  func(w *gateway.GatewayYAMLWebRTC)
	}{
		{"enabled flipped off", func(w *gateway.GatewayYAMLWebRTC) { w.Enabled = false }},
		{"sfu port changed", func(w *gateway.GatewayYAMLWebRTC) { w.SFUPort = 30001 }},
		{"turn domain changed", func(w *gateway.GatewayYAMLWebRTC) { w.TURNDomain = "turn.other" }},
		{"turn secret rotated", func(w *gateway.GatewayYAMLWebRTC) { w.TURNSecret = "rotated" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			tc.mut(&d)
			if gatewayWebRTCInSync(d, desiredEnabled()) {
				t.Errorf("drift in %q not detected — gateway would keep serving stale config", tc.name)
			}
		})
	}
}

func TestGatewayWebRTCInSync_bothDisabled_returnsTrue(t *testing.T) {
	// A namespace genuinely without WebRTC: on-disk block empty, desired
	// disabled. In-sync → no restart. (Avoids churning non-webrtc
	// namespaces on every boot.)
	if !gatewayWebRTCInSync(gateway.GatewayYAMLWebRTC{}, gateway.InstanceConfig{}) {
		t.Error("disabled on-disk + disabled desired must be in-sync (no restart)")
	}
}

// ReconcileGateway I/O paths that DON'T restart (the restart path needs
// real systemd, so it's covered by the pure helper above). These pin
// that a matching config is a clean no-op and that an unreadable config
// surfaces an error instead of blind-restarting.

func writeGatewayConfig(t *testing.T, base, ns, nodeID string, wr gateway.GatewayYAMLWebRTC) {
	t.Helper()
	dir := filepath.Join(base, ns, "configs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	b, _ := yaml.Marshal(gateway.GatewayYAMLConfig{ClientNamespace: ns, WebRTC: wr})
	if err := os.WriteFile(filepath.Join(dir, "gateway-"+nodeID+".yaml"), b, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileGateway_inSyncIsNoOpNoError(t *testing.T) {
	base := t.TempDir()
	ns, node := "anchat-test", "node-1"
	writeGatewayConfig(t, base, ns, node, gateway.GatewayYAMLWebRTC{
		Enabled: true, SFUPort: 30000,
		TURNDomain: "turn.ns-anchat-test.orama-devnet.network", TURNSecret: "the-secret",
	})
	s := NewSystemdSpawner(base, "", zap.NewNop())

	// Desired == on-disk → must return nil WITHOUT attempting a restart
	// (RestartGateway would error here since there's no real systemd, so
	// a nil return proves we never reached it).
	err := s.ReconcileGateway(context.Background(), ns, node, desiredEnabled())
	if err != nil {
		t.Errorf("in-sync config must be a clean no-op; got %v (did it try to restart?)", err)
	}
}

func TestReconcileGateway_missingConfigReturnsErrorNotRestart(t *testing.T) {
	// No config file on disk → return an error so the caller leaves the
	// running gateway alone, rather than blind-restarting a healthy one.
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())
	err := s.ReconcileGateway(context.Background(), "anchat-test", "node-1", desiredEnabled())
	if err == nil {
		t.Error("missing config must return an error (don't blind-restart a healthy gateway)")
	}
}

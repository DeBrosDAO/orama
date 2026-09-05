package namespace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
	// comparison covers all five webrtc fields (a future refactor that
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
		{"stealth domain changed", func(w *gateway.GatewayYAMLWebRTC) { w.TURNStealthDomain = "cdn-deadbeef0000.orama-devnet.network" }},
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

func cfgInSync(onDisk gateway.GatewayYAMLConfig, cfg gateway.InstanceConfig, hmac string) bool {
	return gatewayConfigInSync(onDisk, cfg, hmac, "", "10.0.0.5:10104")
}

func TestGatewayConfigInSync_secretsKeyMissingOnDisk_returnsFalse(t *testing.T) {
	cfg := gateway.InstanceConfig{SecretsEncryptionKey: "the-key"}
	onDisk := gatewayYAMLFromInstance(cfg, "", "", "10.0.0.5:10104")
	onDisk.SecretsEncryptionKey = ""
	if cfgInSync(onDisk, cfg, "") {
		t.Fatal("empty on-disk secrets key vs non-empty desired must be out-of-sync (needs restart to enable secrets)")
	}
}

func TestGatewayConfigInSync_secretsKeyMatches_returnsTrue(t *testing.T) {
	cfg := gateway.InstanceConfig{SecretsEncryptionKey: "the-key"}
	onDisk := gatewayYAMLFromInstance(cfg, "", "", "10.0.0.5:10104")
	if !cfgInSync(onDisk, cfg, "") {
		t.Error("matching secrets key must be in-sync (no restart) — else restart loop on every boot")
	}
}

func TestGatewayConfigInSync_bothSecretsKeysEmpty_returnsTrue(t *testing.T) {
	cfg := gateway.InstanceConfig{}
	onDisk := gatewayYAMLFromInstance(cfg, "", "", "10.0.0.5:10104")
	if !cfgInSync(onDisk, cfg, "") {
		t.Error("empty on-disk + empty desired secrets key must be in-sync (no restart loop)")
	}
}

func TestGatewayConfigInSync_secretsKeyRotated_returnsFalse(t *testing.T) {
	cfg := gateway.InstanceConfig{SecretsEncryptionKey: "new-key"}
	onDisk := gatewayYAMLFromInstance(cfg, "", "", "10.0.0.5:10104")
	onDisk.SecretsEncryptionKey = "old-key"
	if cfgInSync(onDisk, cfg, "") {
		t.Error("rotated secrets key (old != new) must be out-of-sync")
	}
}

func TestGatewayConfigInSync_webrtcDriftStillDetected(t *testing.T) {
	cfg := gateway.InstanceConfig{WebRTCEnabled: true, SFUPort: 30000}
	onDisk := gatewayYAMLFromInstance(gateway.InstanceConfig{}, "", "", "10.0.0.5:10104")
	if cfgInSync(onDisk, cfg, "") {
		t.Error("WebRTC drift must still be detected by the combined in-sync check")
	}
}

func TestGatewayConfigInSync_hmacSecretMissingOnDisk_returnsFalse(t *testing.T) {
	cfg := gateway.InstanceConfig{Namespace: "ns", HTTPPort: 6101}
	onDisk := gatewayYAMLFromInstance(cfg, "the-hmac", "", "10.0.0.5:10104")
	onDisk.APIKeyHMACSecret = ""
	if cfgInSync(onDisk, cfg, "the-hmac") {
		t.Fatal("empty on-disk HMAC secret vs non-empty desired must be out-of-sync (bugboard #165)")
	}
}

func TestGatewayConfigInSync_hmacSecretMatches_returnsTrue(t *testing.T) {
	cfg := gateway.InstanceConfig{Namespace: "ns", HTTPPort: 6101}
	onDisk := gatewayYAMLFromInstance(cfg, "the-hmac", "", "10.0.0.5:10104")
	if !cfgInSync(onDisk, cfg, "the-hmac") {
		t.Error("matching HMAC secret must be in-sync (no restart loop)")
	}
}

func TestGatewayConfigInSync_bothHmacSecretsEmpty_returnsTrue(t *testing.T) {
	cfg := gateway.InstanceConfig{Namespace: "ns", HTTPPort: 6101}
	onDisk := gatewayYAMLFromInstance(cfg, "", "", "10.0.0.5:10104")
	if !cfgInSync(onDisk, cfg, "") {
		t.Error("empty on-disk + empty desired HMAC must be in-sync")
	}
}

func TestGatewayConfigInSync_hmacSecretRotated_returnsFalse(t *testing.T) {
	cfg := gateway.InstanceConfig{Namespace: "ns", HTTPPort: 6101}
	onDisk := gatewayYAMLFromInstance(cfg, "new-hmac", "", "10.0.0.5:10104")
	onDisk.APIKeyHMACSecret = "old-hmac"
	if cfgInSync(onDisk, cfg, "new-hmac") {
		t.Error("rotated HMAC secret must be out-of-sync")
	}
}

func TestGatewayYAMLEqual_anyFieldChangeIsDrift(t *testing.T) {
	// Exhaustive-by-construction (bugboard #165): mutating any exported
	// GatewayYAMLConfig / GatewayYAMLWebRTC field must make equality fail.
	// A newly added YAML field that is not compared will fail this test.
	base := gatewayYAMLFromInstance(gateway.InstanceConfig{
		Namespace:            "ns",
		HTTPPort:             6101,
		SecretsEncryptionKey: "k",
	}, "hmac", "/cluster-secret", "10.0.0.5:6101")
	if !gatewayYAMLEqual(base, base) {
		t.Fatal("a config must equal itself")
	}

	checkStruct := func(prefix string, sample any) {
		t.Helper()
		typ := reflect.TypeOf(sample)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			t.Run(prefix+f.Name, func(t *testing.T) {
				mutated := base
				mv := reflect.ValueOf(&mutated).Elem()
				var fv reflect.Value
				if prefix == "WebRTC." {
					fv = mv.FieldByName("WebRTC").FieldByName(f.Name)
				} else {
					fv = mv.FieldByName(f.Name)
				}
				if !fv.IsValid() || !fv.CanSet() {
					t.Fatalf("cannot set %s%s", prefix, f.Name)
				}
				switch fv.Kind() {
				case reflect.String:
					fv.SetString(fv.String() + "-mutated")
				case reflect.Bool:
					fv.SetBool(!fv.Bool())
				case reflect.Int:
					fv.SetInt(fv.Int() + 1)
				case reflect.Slice:
					fv.Set(reflect.ValueOf([]string{"mutated"}))
				case reflect.Struct:
					if prefix == "" && f.Name == "WebRTC" {
						t.Skip("nested WebRTC fields covered separately")
					}
					t.Fatalf("unhandled struct %s%s — add nested coverage", prefix, f.Name)
				default:
					t.Fatalf("unhandled kind %s for %s%s", fv.Kind(), prefix, f.Name)
				}
				if gatewayYAMLEqual(base, mutated) {
					t.Errorf("mutating %s%s was not detected — add it to gatewayYAMLEqual", prefix, f.Name)
				}
			})
		}
	}
	checkStruct("", gateway.GatewayYAMLConfig{})
	checkStruct("WebRTC.", gateway.GatewayYAMLWebRTC{})
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
	withOverlayIP(t, "10.0.0.5", nil)
	root, nsBase := setupOramaDirs(t)
	writeAPIKeyHMACSecret(t, root, "the-hmac-secret\n")
	ns, node := "anchat-test", "node-1"
	s := NewSystemdSpawner(nsBase, "", zap.NewNop())
	cfg := desiredEnabled()
	cfg.Namespace = ns

	hmac, err := s.readAPIKeyHMACSecret()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(nsBase, ns, "configs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	listenAddr, err := gatewayListenAddr(cfg.Namespace, cfg.HTTPPort)
	if err != nil {
		t.Fatal(err)
	}
	b, err := yaml.Marshal(gatewayYAMLFromInstance(cfg, hmac, "", listenAddr))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gateway-"+node+".yaml"), b, 0600); err != nil {
		t.Fatal(err)
	}

	if err := s.ReconcileGateway(context.Background(), ns, node, cfg); err != nil {
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

func TestGatewayWebRTCInSync_stealthEnableDetectedAsDrift(t *testing.T) {
	// feat-124: enabling stealth must drift an otherwise-matching gateway so
	// the reconciler rewrites its yaml with turn_stealth_domain and restarts
	// it — that's how turn.credentials starts advertising turns:<host>:443.
	onDisk := gateway.GatewayYAMLWebRTC{
		Enabled: true, SFUPort: 30000,
		TURNDomain: "turn.ns-anchat-test.orama-devnet.network", TURNSecret: "the-secret",
	}
	desired := desiredEnabled()
	desired.TURNStealthDomain = "cdn-abc123def456.orama-devnet.network"
	if gatewayWebRTCInSync(onDisk, desired) {
		t.Error("stealth enable not detected as drift — gateway would never advertise the stealth URI")
	}

	// And once the yaml carries it, the same desired config is in-sync (no
	// restart loop).
	onDisk.TURNStealthDomain = desired.TURNStealthDomain
	if !gatewayWebRTCInSync(onDisk, desired) {
		t.Error("matching stealth domain reported as drift — restart loop")
	}
}

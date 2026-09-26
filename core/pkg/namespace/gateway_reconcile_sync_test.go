package namespace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gatewayspec"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// The reconcile loop this pins: the tenant sweep compared the on-disk
// (credentialed) gateway YAML against a skeleton config, saw drift every 60s,
// and restarted the gateway into a config it could not start with. Reconcile
// must compare against exactly what SpawnGateway writes.

// spawnedGateway runs SpawnGateway far enough to write its config (systemd is
// not available here, so the start fails afterwards) and returns the spawner,
// the config it was given and the bytes it wrote.
func spawnedGateway(t *testing.T) (*SystemdSpawner, gatewayspec.InstanceConfig, string, []byte) {
	t.Helper()
	withOverlayIP(t, "10.0.0.5", nil)
	root, namespaceBase := setupOramaDirs(t)
	writeAPIKeyHMACSecret(t, root, "the-hmac-secret\n")
	writeRQLitePassword(t, root, testRQLitePass+"\n")

	s := NewSystemdSpawner(namespaceBase, "", zap.NewNop())
	cfg := gatewayspec.InstanceConfig{
		Namespace:            "anchat-test",
		NodeID:               "node-1",
		HTTPPort:             deadPort(t),
		BaseDomain:           "orama-devnet.network",
		RQLiteDSN:            tenantRQLiteURL("10.0.0.5", 10200),
		GlobalRQLiteDSN:      "http://orama:" + testRQLitePass + "@10.0.0.1:10100",
		OlricServers:         []string{"10.0.0.5:10202", "10.0.0.6:10202"},
		OlricTimeout:         30 * time.Second,
		IPFSTimeout:          time.Minute,
		SecretsEncryptionKey: "the-secrets-key",
		NtfyBaseURL:          "https://push.orama-devnet.network",
		WebRTCEnabled:        true,
		SFUPort:              30000,
		TURNDomain:           "turn.ns-anchat-test.orama-devnet.network",
		TURNSecret:           "the-turn-secret",
	}
	_ = s.SpawnGateway(context.Background(), cfg.Namespace, cfg.NodeID, cfg)

	path := filepath.Join(namespaceBase, cfg.Namespace, "configs", "gateway-"+cfg.NodeID+".yaml")
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("SpawnGateway did not write its config: %v", err)
	}
	return s, cfg, path, written
}

// Spawn, then reconcile with the same (bare-DSN) config: in sync, no action.
func TestReconcileGateway_afterSpawnIsInSyncAndTakesNoAction(t *testing.T) {
	s, cfg, path, written := spawnedGateway(t)

	// A restart would stop the unit and re-run SpawnGateway, which fails
	// without systemd — so a nil error is the proof nothing was restarted.
	if err := s.ReconcileGateway(context.Background(), cfg.Namespace, cfg.NodeID, cfg); err != nil {
		t.Fatalf("a freshly spawned gateway was reported as drifted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, written) {
		t.Fatal("reconcile rewrote an in-sync gateway config")
	}
}

// The membership sweep with unchanged membership is a no-op too.
func TestReconcileGatewayMembership_unchangedMembershipIsNoOp(t *testing.T) {
	s, cfg, path, written := spawnedGateway(t)

	m := gatewayMembership{HTTPPort: cfg.HTTPPort, OlricServers: []string{"10.0.0.6:10202", "10.0.0.5:10202"}}
	if err := s.ReconcileGatewayMembership(context.Background(), cfg.Namespace, cfg.NodeID, m); err != nil {
		t.Fatalf("unchanged membership was reported as drift: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, written) {
		t.Fatal("membership reconcile rewrote an in-sync gateway config")
	}
}

// A real membership change is drift, and everything else is carried over from
// the running config — never a skeleton missing its DSN and secrets.
func TestReconcileGatewayMembership_changeKeepsTheRestOfTheConfig(t *testing.T) {
	s, cfg, _, written := spawnedGateway(t)

	var onDisk gatewayspec.GatewayYAMLConfig
	if err := yaml.Unmarshal(written, &onDisk); err != nil {
		t.Fatal(err)
	}
	desiredCfg, err := instanceFromGatewayYAML(onDisk, cfg.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	desiredCfg.OlricServers = []string{"10.0.0.5:10202", "10.0.0.7:10202"}
	desired, err := s.gatewayYAMLFor(cfg.Namespace, desiredCfg)
	if err != nil {
		t.Fatal(err)
	}
	if gatewayYAMLEqual(onDisk, desired) {
		t.Fatal("a changed Olric server set was not detected as drift")
	}
	onDisk.OlricServers = desired.OlricServers
	if !gatewayYAMLEqual(onDisk, desired) {
		t.Fatalf("a membership change altered more than the membership:\non disk %+v\ndesired %+v", onDisk, desired)
	}
}

// instanceFromGatewayYAML must invert gatewayYAMLFromInstance for every YAML
// field, or a config read back from disk re-renders differently and reads as
// drift. Every field is populated, so a field one side misses fails here.
func TestInstanceFromGatewayYAML_roundTripsEveryField(t *testing.T) {
	var y gatewayspec.GatewayYAMLConfig
	fillStrings(reflect.ValueOf(&y).Elem())
	y.ListenAddr = "10.0.0.5:10204"
	y.OlricServers = []string{"10.0.0.5:10202"}
	y.OlricTimeout = "30s"
	y.IPFSTimeout = "1m0s"
	y.IPFSReplicationFactor = 3
	y.WebRTC.Enabled = true
	y.WebRTC.SFUPort = 30000
	// Not carried by InstanceConfig: the host supplies them on re-render.
	y.BootstrapPeers, y.EnableHTTPS, y.TLSCacheDir = nil, false, ""

	cfg, err := instanceFromGatewayYAML(y, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	got := gatewayYAMLFromInstance(cfg, y.APIKeyHMACSecret, y.ClusterSecretPath, y.ListenAddr)
	if !reflect.DeepEqual(got, y) {
		t.Fatalf("round trip lost fields:\nin  %+v\nout %+v", y, got)
	}
}

func TestInstanceFromGatewayYAML_rejectsBadValues(t *testing.T) {
	for name, y := range map[string]gatewayspec.GatewayYAMLConfig{
		"no listen port": {ListenAddr: "10.0.0.5"},
		"bad port":       {ListenAddr: "10.0.0.5:http"},
		"bad timeout":    {ListenAddr: "10.0.0.5:10204", OlricTimeout: "soon"},
	} {
		if _, err := instanceFromGatewayYAML(y, "node-1"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// fillStrings sets every string field (recursively) to a distinct value.
func fillStrings(v reflect.Value) {
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(v.Type().Field(i).Name + "-value")
		case reflect.Struct:
			fillStrings(f)
		}
	}
}

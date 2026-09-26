package namespace

import (
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gatewayspec"
	"go.uber.org/zap"
)

// Every gateway on a host used to resolve its signing keys to the same files
// under <oramaDir>/secrets — read-only to the unit, and shared by the index and
// every tenant gateway alike. Each now gets a state directory of its own, under
// its namespace directory, which the unit may write.
func TestGatewayYAMLFor_eachGatewayOnAHostHasItsOwnStateDir(t *testing.T) {
	withOverlayIP(t, "10.0.0.5", nil)
	root, nsBase := setupOramaDirs(t)
	writeAPIKeyHMACSecret(t, root, "the-hmac-secret\n")
	writeRQLitePassword(t, root, testRQLitePass)
	s := NewSystemdSpawner(nsBase, filepath.Join(root, "secrets", "cluster-secret"), zap.NewNop())

	index, err := s.gatewayYAMLFor(BlueprintNameIndex, gatewayspec.InstanceConfig{
		Namespace: BlueprintNameIndex, HTTPPort: 10104, RQLiteDSN: "http://10.0.0.5:10100",
	})
	if err != nil {
		t.Fatalf("index gateway YAML: %v", err)
	}
	tenant, err := s.gatewayYAMLFor("acme", gatewayspec.InstanceConfig{
		Namespace: "acme", HTTPPort: 10004, RQLiteDSN: "http://10.0.0.5:10000",
	})
	if err != nil {
		t.Fatalf("tenant gateway YAML: %v", err)
	}

	if want := filepath.Join(nsBase, "index", "gateway"); index.StateDir != want {
		t.Errorf("index state_dir = %q, want %q", index.StateDir, want)
	}
	if want := filepath.Join(nsBase, "acme", "gateway"); tenant.StateDir != want {
		t.Errorf("tenant state_dir = %q, want %q", tenant.StateDir, want)
	}
	if index.StateDir == tenant.StateDir {
		t.Fatal("two gateways on one host share a state directory, so they share signing keys")
	}
}

// The state directory is host layout, not caller input: a caller cannot point
// a gateway's keys somewhere else.
func TestGatewayYAMLFor_stateDirIgnoresTheCaller(t *testing.T) {
	withOverlayIP(t, "10.0.0.5", nil)
	root, nsBase := setupOramaDirs(t)
	writeAPIKeyHMACSecret(t, root, "the-hmac-secret\n")
	writeRQLitePassword(t, root, testRQLitePass)
	s := NewSystemdSpawner(nsBase, "", zap.NewNop())

	y, err := s.gatewayYAMLFor("acme", gatewayspec.InstanceConfig{
		Namespace: "acme", HTTPPort: 10004, RQLiteDSN: "http://10.0.0.5:10000",
		StateDir: "/opt/orama/.orama/secrets",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(nsBase, "acme", "gateway"); y.StateDir != want {
		t.Errorf("state_dir = %q, want %q", y.StateDir, want)
	}
}

// A gateway YAML written before state_dir existed reads back as drift, so the
// reconciler rewrites it instead of leaving a gateway that refuses to start.
func TestGatewayYAMLEqual_missingStateDirIsDrift(t *testing.T) {
	desired := gatewayYAMLFromInstance(gatewayspec.InstanceConfig{
		Namespace: "acme", StateDir: "/opt/orama/.orama/data/namespaces/acme/gateway",
	}, "hmac", "/cluster-secret", "10.0.0.5:10004")
	old := desired
	old.StateDir = ""
	if gatewayYAMLEqual(old, desired) {
		t.Fatal("a pre-upgrade config with no state_dir compared in sync")
	}
	back, err := instanceFromGatewayYAML(desired, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if back.StateDir != desired.StateDir {
		t.Errorf("state_dir did not survive the round trip: %q", back.StateDir)
	}
}

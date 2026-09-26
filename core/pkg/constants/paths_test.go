package constants

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// productionOramaDir is where every systemd template puts the orama directory.
const productionOramaDir = "/opt/orama/.orama"

// unitFile reads one of core/systemd/*.service.
func unitFile(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "systemd", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// The gateway extracts deployments where the orama-deploy-*@ templates run
// them from. It used to extract into <oramaDir>/deployments, which no unit read.
func TestDeploymentsBaseDir_isWhereTheDeployTemplatesRun(t *testing.T) {
	base := DeploymentsBaseDir(productionOramaDir)
	for _, unit := range []string{"orama-deploy-go@.service", "orama-deploy-node@.service", "orama-deploy-npm@.service"} {
		if want := "WorkingDirectory=" + base + "/%i"; !strings.Contains(unitFile(t, unit), want) {
			t.Errorf("%s does not run from %s/%%i", unit, base)
		}
	}
}

// The index gateway writes the shared TURN config where orama-turn.service
// reads it.
func TestHostTURNConfigPath_isTheTURNUnitsConfig(t *testing.T) {
	want := "Environment=TURN_CONFIG=" + HostTURNConfigPath(productionOramaDir)
	if !strings.Contains(unitFile(t, "orama-turn.service"), want) {
		t.Errorf("orama-turn.service does not read %s", HostTURNConfigPath(productionOramaDir))
	}
}

// Everything a gateway writes has to be inside the gateway unit's
// ReadWritePaths; secrets/ and configs/ are not. What only the cluster gateway
// writes — every namespace's directory, the host TURN config — is in its
// instance's drop-in, and pkg/install checks that one.
func TestGatewayWrites_areInsideTheGatewayUnitsWritablePaths(t *testing.T) {
	unit := unitFile(t, "orama-namespace-gateway@.service")
	var writable []string
	for _, line := range strings.Split(unit, "\n") {
		if rest, ok := strings.CutPrefix(line, "ReadWritePaths="); ok {
			writable = append(writable, strings.Fields(rest)...)
		}
	}
	nsDir := NamespacesDir(productionOramaDir)
	for _, path := range []string{
		GatewayStateDir(nsDir, "%i"),
		SQLiteBaseDir(productionOramaDir),
		DeploymentsBaseDir(productionOramaDir),
	} {
		inside := false
		for _, w := range writable {
			if path == w || strings.HasPrefix(path, w+"/") {
				inside = true
			}
		}
		if !inside {
			t.Errorf("%s is outside the gateway unit's ReadWritePaths %v", path, writable)
		}
	}
}

func TestGatewayStateDir_isPerNamespace(t *testing.T) {
	nsDir := NamespacesDir(productionOramaDir)
	if got, want := GatewayStateDir(nsDir, IndexNamespace), "/opt/orama/.orama/data/namespaces/index/gateway"; got != want {
		t.Errorf("index state dir = %q, want %q", got, want)
	}
	if GatewayStateDir(nsDir, IndexNamespace) == GatewayStateDir(nsDir, "acme") {
		t.Error("two namespaces resolve the same state directory")
	}
}

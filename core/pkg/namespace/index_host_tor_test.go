package namespace

import (
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"go.uber.org/zap"
)

// Every node has Tor. A missing torrc means install/upgrade did not run its Tor
// phase; the old Anyone code logged "not installed; skipping" here, which left
// /v1/proxy/anon answering 503 with nothing in the node's own logs saying why.
func TestEnsureTor_missingTorrcIsAnError(t *testing.T) {
	if _, err := os.Stat(constants.TorConfigPath); err == nil {
		t.Skipf("%s exists on this machine", constants.TorConfigPath)
	}
	sup := NewIndexSupervisor(t.TempDir(), zap.NewNop())
	err := sup.EnsureTor("node-1")
	if err == nil {
		t.Fatal("EnsureTor with no torrc must fail, not skip")
	}
	if !strings.Contains(err.Error(), constants.TorConfigPath) {
		t.Errorf("error should name the missing torrc: %v", err)
	}
}

func TestBlueprintIndex_torClaimsTheSharedSOCKSPort(t *testing.T) {
	for _, spec := range BlueprintIndex().Services {
		if spec.Name != ServiceTor {
			continue
		}
		if len(spec.PortNeeds) != 1 || spec.PortNeeds[0].Fixed != constants.TorSOCKSPort {
			t.Errorf("tor port needs = %+v, want the fixed port %d", spec.PortNeeds, constants.TorSOCKSPort)
		}
		return
	}
	t.Fatal("the index blueprint has no tor service")
}

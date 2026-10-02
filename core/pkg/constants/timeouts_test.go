package constants

import "testing"

// The 30 s CLI timeout used to sit under a 180 s replica call: the user saw a
// timeout for a change that was still being applied, and never saw the 502
// that names the failed node.
func TestEnvChangeBudgets_areStrictlyNested(t *testing.T) {
	chain := []struct {
		name string
		d    int64
	}{
		{"DeploymentEnvReconfigureTimeout", int64(DeploymentEnvReconfigureTimeout)},
		{"DeploymentEnvCallTimeout", int64(DeploymentEnvCallTimeout)},
		{"DeploymentEnvChangeBudget", int64(DeploymentEnvChangeBudget)},
		{"GatewayProxyTimeout", int64(GatewayProxyTimeout)},
	}
	for i := 1; i < len(chain); i++ {
		if chain[i].d <= chain[i-1].d {
			t.Errorf("%s (%v) must be longer than %s (%v)", chain[i].name, chain[i].d, chain[i-1].name, chain[i-1].d)
		}
	}
	if DeploymentEnvChangeBudget >= GatewayServerWriteTimeout {
		t.Errorf("the handler's budget %v must end before the server's write deadline %v", DeploymentEnvChangeBudget, GatewayServerWriteTimeout)
	}
	if DeploymentEnvClientTimeout <= DeploymentEnvChangeBudget {
		t.Errorf("the CLI's wait %v must outlast the handler's budget %v", DeploymentEnvClientTimeout, DeploymentEnvChangeBudget)
	}
}

// The update chain used to wait 180 s for a replica behind a 120 s gateway hop:
// the caller was told "timed out" about an update that went through.
func TestUpdateBudgets_areStrictlyNested(t *testing.T) {
	chain := []struct {
		name string
		d    int64
	}{
		{"DeploymentReplicaCallTimeout", int64(DeploymentReplicaCallTimeout)},
		{"DeploymentUpdateBudget", int64(DeploymentUpdateBudget)},
		{"GatewayDeploymentProxyTimeout", int64(GatewayDeploymentProxyTimeout)},
		{"GatewayDeploymentWriteBudget", int64(GatewayDeploymentWriteBudget)},
	}
	for i := 1; i < len(chain); i++ {
		if chain[i].d <= chain[i-1].d {
			t.Errorf("%s (%v) must be longer than %s (%v)", chain[i].name, chain[i].d, chain[i-1].name, chain[i-1].d)
		}
	}
	if DeploymentEnvChangeBudget >= GatewayDeploymentProxyTimeout {
		t.Errorf("an env change's budget %v must end before the deployment proxy hop %v", DeploymentEnvChangeBudget, GatewayDeploymentProxyTimeout)
	}
}

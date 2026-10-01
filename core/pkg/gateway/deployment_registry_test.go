package gateway

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// A namespace gateway holds two databases. Its deployment family used the
// tenant one, where the table is always empty, so every deployment call on a
// namespace host answered "Deployment not found" for an app that was serving.
func TestDeploymentRegistry_isTheClusterRegistryOnEveryGateway(t *testing.T) {
	tenant, registry := rqlite.NewClient(nil), rqlite.NewClient(nil)
	if got := deploymentRegistry(&Dependencies{ORMClient: tenant, GlobalORMClient: registry}); got != registry {
		t.Fatal("a namespace gateway's deployments are not read from the cluster registry")
	}
	if got := deploymentRegistry(&Dependencies{ORMClient: registry, GlobalORMClient: registry}); got != registry {
		t.Fatal("the main gateway's deployments are not read from its registry")
	}
}

// The checker covers every namespace's replicas on its node: a namespace
// gateway running one too would check and restart them once per namespace.
func TestRunsDeploymentHealthChecker_onlyTheMainGateway(t *testing.T) {
	if !runsDeploymentHealthChecker(&Config{RQLiteDSN: "http://10.0.0.1:10100"}) {
		t.Error("the main gateway does not run the deployment health checker")
	}
	if !runsDeploymentHealthChecker(&Config{RQLiteDSN: "http://10.0.0.1:10100", GlobalRQLiteDSN: "http://10.0.0.1:10100"}) {
		t.Error("a gateway whose registry is its own database does not run the checker")
	}
	if runsDeploymentHealthChecker(&Config{RQLiteDSN: "http://10.0.0.1:10035", GlobalRQLiteDSN: "http://10.0.0.1:10100"}) {
		t.Error("a namespace gateway runs the node's deployment health checker")
	}
}

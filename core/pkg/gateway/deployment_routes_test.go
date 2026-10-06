package gateway

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Every deployment route is served by the index gateway, also when it is
// addressed to ns-<name>. A namespace gateway may not drive the node's
// systemd: served there, a delete removed the deployment's rows and freed its
// port while the refused stop left the unit running on it.
func TestDeploymentRoutes_stayOnTheMainGateway(t *testing.T) {
	found := 0
	for _, pattern := range gatewayRoutes.Patterns() {
		if !strings.HasPrefix(pattern, "/v1/deployments") {
			continue
		}
		found++
		r := httptest.NewRequest("POST", pattern, nil)
		if !gatewayRoutes.For(r).MainGateway {
			t.Errorf("%s is not MainGateway: a namespace gateway would serve it", pattern)
		}
	}
	if found == 0 {
		t.Fatal("no /v1/deployments route in the policy table")
	}
}

package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
)

// Every route whose handler asks the operator list stays on the index gateway
// for an ns-<name> host. The list is not in a namespace's RQLite, so a tenant
// gateway answered 503 "the registry did not answer" to a caller who was
// merely not an operator.
func TestRoutePolicy_operatorListRoutesStayOnTheIndexGateway(t *testing.T) {
	for _, path := range []string{
		"/v1/network/status", "/v1/network/peers",
		"/v1/operator/health", "/v1/operator/nodes", "/v1/operator/node/register",
		"/v1/operator/operators", "/v1/operator/operators/0xabc",
		"/v1/operator/rotate-signing-key", "/v1/operator/rotate-secrets",
		"/v1/operator/invite", "/v1/operator/settings", "/v1/operator/creators",
		"/v1/operator/telemetry", "/v1/operator/namespaces/remove",
	} {
		t.Run(path, func(t *testing.T) {
			p := gatewayRoutes.For(httptest.NewRequest(http.MethodGet, path, nil))
			if !p.MainGateway {
				t.Errorf("%s is proxied to a tenant gateway, whose RQLite has no operator list", path)
			}
		})
	}
}

// A node's own discovery is stamped and authenticated by the handler; it is
// not an operator-list request and keeps the route it had.
func TestRoutePolicy_aStampedNetworkRequestIsNotAnOperatorListRoute(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/network/status", nil)
	r.Header.Set(nodeauth.CoordinationMACHeader, "1.00")
	if gatewayRoutes.For(r).MainGateway {
		t.Error("a node's stamped request was pinned to the index gateway")
	}
}

// The routes that need no operator list are unchanged: they act on the node or
// the namespace they are addressed to.
func TestRoutePolicy_nodeLocalRoutesAreNotPinnedToTheIndexGateway(t *testing.T) {
	for _, path := range []string{"/v1/node/status", "/v1/node/logs", "/v1/network/connect", "/v1/node/leave"} {
		if gatewayRoutes.For(httptest.NewRequest(http.MethodGet, path, nil)).MainGateway {
			t.Errorf("%s is pinned to the index gateway", path)
		}
	}
}

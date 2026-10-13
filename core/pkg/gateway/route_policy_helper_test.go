package gateway

import (
	"net/http/httptest"

	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
)

// policyOf is the policy of the route a request for (method, path) matches. It
// is what the middleware reads, resolved the same way.
func policyOf(method, path string) routepolicy.Policy {
	return gatewayRoutes.For(httptest.NewRequest(method, path, nil))
}

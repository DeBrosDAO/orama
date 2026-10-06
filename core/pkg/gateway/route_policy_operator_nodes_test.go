package gateway

import (
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// Listing the operator's nodes is a read: `orama ssh` and every command that
// resolves a node ask for it with a read credential (stagenet e2e, 2026-09-30,
// it was grouped with the write routes and refused one).
//
// The policy table is keyed by path, not method, so the route's policy has to be
// the right one for every method the handler serves; HandleListNodes answers
// GET and refuses every other method with 405 (handlers/operator/nodes.go), so
// there is no write on this path to keep behind operator:write.
func TestRoutePolicy_listingOperatorNodesIsARead(t *testing.T) {
	policy := policyOf(http.MethodGet, "/v1/operator/nodes")
	if policy.Domain != string(auth.DomainOperator) || policy.Action != string(auth.ActionRead) {
		t.Fatalf("GET /v1/operator/nodes asks for %s:%s, want operator:read", policy.Domain, policy.Action)
	}
	if policy.Access.Anonymous() {
		t.Fatal("GET /v1/operator/nodes is reachable without a credential")
	}
}

// What changes a node or the cluster stays a write.
func TestRoutePolicy_operatorMutationsStayWrites(t *testing.T) {
	for _, path := range []string{
		"/v1/operator/node/register", "/v1/operator/operators", "/v1/operator/rotate-secrets",
		"/v1/operator/rotate-signing-key", "/v1/node/command", "/v1/node/leave",
	} {
		policy := policyOf(http.MethodPost, path)
		if policy.Domain != string(auth.DomainOperator) || policy.Action != string(auth.ActionWrite) {
			t.Errorf("%s asks for %s:%s, want operator:write", path, policy.Domain, policy.Action)
		}
	}
}

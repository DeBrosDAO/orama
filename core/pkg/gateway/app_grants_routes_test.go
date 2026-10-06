package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An app that could hold the control plane could deploy over itself, mint keys
// and read the raw database — which is the permanent namespace key this whole
// change exists to end, wearing a different hat.
func TestSetAppGrant_refusesTheControlPlane(t *testing.T) {
	g := chainGateway(t, "acme", &stubKeyDatabase{})

	req := httptest.NewRequest(http.MethodPost, "/v1/deployments/grants",
		strings.NewReader(`{"name":"web","role":"admin"}`))
	req = withNamespace(req, "acme")
	rec := httptest.NewRecorder()

	g.setAppGrant(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: a deployment was granted the control plane", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "runtime") {
		t.Errorf("the refusal does not say what to grant instead: %s", rec.Body.String())
	}
}

// Ownership is not a role either — it is transferred, and a deployment is not
// something a namespace can belong to.
func TestSetAppGrant_refusesOwnership(t *testing.T) {
	g := chainGateway(t, "acme", &stubKeyDatabase{})

	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/deployments/grants",
		strings.NewReader(`{"name":"web","role":"owner"}`)), "acme")
	rec := httptest.NewRecorder()

	g.setAppGrant(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("a deployment was made the owner of its namespace")
	}
}

func TestSetAppGrant_needsADeploymentName(t *testing.T) {
	g := chainGateway(t, "acme", &stubKeyDatabase{})

	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/deployments/grants",
		strings.NewReader(`{"role":"runtime"}`)), "acme")
	rec := httptest.NewRecorder()

	g.setAppGrant(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// withNamespace is a request the auth middleware has already resolved.
func withNamespace(r *http.Request, namespace string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), CtxKeyNamespaceOverride, namespace))
}

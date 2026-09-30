package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A deployment holds the data plane and nothing else: every control-plane role
// is a client error that says what to grant instead, not a server error and
// not a success.
func TestSetAppGrant_onlyTheDataPlaneRoles(t *testing.T) {
	for _, role := range []string{"admin", "owner", "developer"} {
		t.Run(role, func(t *testing.T) {
			g := chainGateway(t, "acme", &stubKeyDatabase{})
			req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/deployments/grants",
				strings.NewReader(`{"name":"web","role":"`+role+`"}`)), "acme")
			rec := httptest.NewRecorder()

			g.setAppGrant(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "runtime") {
				t.Errorf("the refusal does not say what to grant instead: %s", rec.Body.String())
			}
		})
	}
}

func TestSetAppGrant_anUnknownRoleIsA400(t *testing.T) {
	g := chainGateway(t, "acme", &stubKeyDatabase{})
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/deployments/grants",
		strings.NewReader(`{"name":"web","role":"wizard"}`)), "acme")
	rec := httptest.NewRecorder()

	g.setAppGrant(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// ownerRequest is a members request from acme's owner.
func ownerRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := context.WithValue(r.Context(), CtxKeyNamespaceOverride, "acme")
	ctx = context.WithValue(ctx, ctxKeyGrant, &auth.Grant{Role: auth.RoleOwner, Identifier: "0x852ad3dbb4a7da8b1d9c75da2a7d35801a5a987e"})
	return r.WithContext(ctx)
}

// A transfer to a string no wallet can sign in as used to answer 200 and hand
// the namespace to nobody (stagenet e2e, 2026-09-30: "not-a-wallet"). The
// gateway here has no auth service, so reaching the write would panic.
func TestTransferNamespace_refusesWhatIsNotAWallet(t *testing.T) {
	g := &Gateway{}
	for _, bad := range []string{"", "not-a-wallet", "0x123", "0x852ad3dbb4a7da8b1d9c75da2a7d35801a5a987z"} {
		w := httptest.NewRecorder()
		g.transferNamespace(w, ownerRequest(http.MethodPost, "/v1/namespace/members/transfer", `{"wallet":"`+bad+`"}`))
		if w.Code != http.StatusBadRequest {
			t.Errorf("transfer to %q: status %d, want 400", bad, w.Code)
		}
	}
}

func TestAddNamespaceMember_refusesWhatIsNotAWallet(t *testing.T) {
	g := &Gateway{}
	w := httptest.NewRecorder()
	g.addNamespaceMember(w, ownerRequest(http.MethodPost, "/v1/namespace/members", `{"wallet":"not-a-wallet","role":"runtime"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

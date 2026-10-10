package serverless

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// A workload's admin answer is its grant now, like its invoke answer: a token
// minted while the app held admin does not keep the app an admin once the grant
// is narrowed or revoked.
func TestCallerIsAdmin_aWorkloadHoldsWhatItsGrantSaysNow(t *testing.T) {
	h := &ServerlessHandlers{}
	for name, tc := range map[string]struct {
		custom map[string]string
		grant  *auth.Grant
		want   bool
	}{
		"token claims admin, grant reduced to runtime": {map[string]string{"scopes": "admin"}, appGrant(auth.RoleRuntime, ""), false},
		"token claims admin, grant revoked":            {map[string]string{"scopes": "admin"}, nil, false},
		"token claims nothing, grant is admin":         {nil, appGrant(auth.RoleAdmin, ""), true},
		"token claims admin, grant still admin":        {map[string]string{"scopes": "admin"}, appGrant(auth.RoleAdmin, ""), true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := h.getCallerIsAdminFromRequest(workloadInvokeRequest(tc.custom, tc.grant)); got != tc.want {
				t.Errorf("getCallerIsAdminFromRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

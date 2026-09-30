//go:build e2e_fleet

package referenceapps

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// whoami is the gateway's /v1/auth/whoami as the app relays it.
type whoami struct {
	Authenticated bool     `json:"authenticated"`
	Principal     string   `json:"principal"`
	Role          *string  `json:"role"`
	Grants        []string `json:"grants"`
}

// TestReferenceNodeAPI_workloadIdentityRenewsAndGrantsEnforced is the
// workload-identity success path from inside a deployed app: the app is its
// own principal ("app") holding the runtime role it was granted, it renews
// its token at /v1/auth/renew with the token it holds, and the renewed token
// is accepted by the gateway and keeps the app working. Taking the grant down
// to reader reaches the app on redeploy and its data calls are refused;
// granting runtime again restores it (docs/DEPLOYMENT_GUIDE.md "Your app's
// own credential", docs/AUTH.md "A workload's identity").
func TestReferenceNodeAPI_workloadIdentityRenewsAndGrantsEnforced(t *testing.T) {
	t.Parallel()
	tn, u := deployTodoAPI(t)
	app := tn.App(u)
	var self whoami
	if _, err := getJSON(t.Context(), app, "/api/self", "", &self); err != nil {
		t.Fatal(err)
	}
	if !self.Authenticated || self.Principal != "app" || self.Role == nil || *self.Role != roleRuntime {
		t.Fatalf("the app is not its own principal with the runtime role: %+v", self)
	}
	var r renewal
	if _, err := postJSON(t.Context(), app, "/api/renew", "", nil, &r); err != nil {
		t.Fatalf("the app could not renew its workload token: %v", err)
	}
	checkRenewal(t, r, roleRuntime, "invoke", "cache")
	usr := realistic.NewUsers(t, tn, roleRuntime, 1)[0]
	write := func(text string) (int, error) {
		return postJSON(t.Context(), app, "/api/todos", usr.Token(), map[string]string{"text": text}, nil)
	}
	if _, err := write("after renewal"); err != nil {
		t.Fatalf("the app stopped working after renewing its token: %v", err)
	}
	redeployAs(t, tn, u, roleReader, "api-v2")
	for _, node := range tn.F.State.Nodes {
		pinned := app.PinTo(node.PublicIP)
		if status, err := postJSON(t.Context(), pinned, "/api/todos", usr.Token(), map[string]string{"text": "as reader"}, nil); status != http.StatusForbidden {
			t.Errorf("%s: a reader app stored a todo: HTTP %d %v", node.Name, status, err)
		}
	}
	redeployAs(t, tn, u, roleRuntime, "api-v3")
	if _, err := write("granted again"); err != nil {
		t.Errorf("the app did not get its runtime grant back on redeploy: %v", err)
	}
}

// redeployAs grants the API role and redeploys it in place with version,
// waiting until every node serves the new version: a grant reaches a running
// app on its next renewal or at once on a redeploy.
func redeployAs(t *testing.T, tn *realistic.Tenant, u, role, version string) {
	t.Helper()
	tn.Grant(t, todoApp, role)
	tn.Deploy(t, "nodejs", realistic.ServerApp(t, tn.F, realistic.AppNodeAPI, version), todoApp,
		"--env", "STORE_FN="+todoStoreFn, "--env", "APP_VERSION="+version, "--update")
	tn.EveryNodeServes(t, u, "/version", version)
	eventually.Require(t, pollEvery, realistic.StartBudget, "both replicas on "+version, func() (bool, error) {
		n := len(appUnitNodes(t, tn, "node", todoApp))
		if n == replicas {
			return true, nil
		}
		return false, fmt.Errorf("%d nodes run it", n)
	})
}

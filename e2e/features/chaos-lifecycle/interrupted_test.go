//go:build e2e_fleet

package chaoslifecycle

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	deployBudget = 10 * time.Minute
	// uploadingLine is what `orama deploy` prints as it starts the upload
	// (core/cmd/orama/internal/deployments/deploy.go).
	uploadingLine = "Uploading to Orama Network"
	nodeUnit      = realistic.NodeUnit
	// provisionTerminal bounds provisioning reaching ready or failed.
	provisionTerminal = ns.ReadyBudget + 5*time.Minute
	cleanupBudget     = 2 * time.Minute
	// deployWorst: reaching the upload, the interrupted deploy ending, a
	// second deploy, and the convergence after the kill.
	deployWorst = lineBudget + 2*deployBudget + convergeBudget + cleanupBudget
	// provisionWorst: provisioning ending under the kill, the convergence,
	// a fresh namespace becoming ready and the teardowns.
	provisionWorst = provisionTerminal + convergeBudget + ns.ReadyBudget + 2*ns.TeardownBudget
)

// TestChaosLifecycle_gatewayKilledMidDeploy: the namespace gateway of one
// node is SIGKILLed while `orama deploy go` uploads. Wherever the kill
// lands, the CLI ends (it does not hang); the app is then either served or
// the deploy failed with an error and deploying again works; nothing is left
// that blocks the name.
func TestChaosLifecycle_gatewayKilledMidDeploy(t *testing.T) {
	realistic.RequireFaultBudget(t, "the interrupted deploy", deployWorst)
	realistic.Tool(t, "go", "`orama deploy go` builds the app on the deploying machine")
	f := harness.Fleet(t)
	infra.RequireHealthy(t)
	tn := realistic.NewTenant(t)
	dir := realistic.ServerApp(t, f, realistic.AppGo, "chaos-v1")
	t.Cleanup(func() { deleteApp(t, tn, "chaosapp") })
	p := start(t, tn.N.CLI, "deploy", "go", dir, "--name", "chaosapp", "--env", "APP_VERSION=chaos-v1")
	waitLine(t, p, uploadingLine)
	f.Kill(t, f.State.Nodes[0], tenancy.UnitGateway(tn.N.Name))
	res := finish(t, p, deployBudget)
	t.Logf("the interrupted deploy exited %d", res.Exit)
	if res.Exit != 0 {
		if strings.TrimSpace(res.Stderr) == "" {
			t.Errorf("the deploy failed (exit %d) without saying why", res.Exit)
		}
		redeploy(t, tn, dir)
	}
	tn.Grant(t, "chaosapp", "runtime")
	tn.EveryNodeServes(t, appURL(t, tn, "chaosapp"), "/version", "chaos-v1")
	healed(t, "the cluster after the interrupted deploy")
}

// redeploy deploys the app again after a failed attempt: as new, or as an
// update when the failed attempt got as far as recording it.
func redeploy(t *testing.T, tn *realistic.Tenant, dir string) {
	t.Helper()
	res, err := tn.N.CLI.Run(t.Context(), "deploy", "go", dir, "--name", "chaosapp", "--env", "APP_VERSION=chaos-v1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit == 0 {
		return
	}
	upd := tn.N.CLI.MustOK(t, "deploy", "go", dir, "--name", "chaosapp", "--env", "APP_VERSION=chaos-v1", "--update")
	t.Logf("the name was held by the failed attempt; --update completed it:\n%s", upd.Stdout)
}

// appURL reads the app's address from /v1/deployments/get.
func appURL(t *testing.T, tn *realistic.Tenant, name string) string {
	t.Helper()
	var d struct {
		URLs []string `json:"urls"`
	}
	if _, err := tn.C.JSON(t.Context(), http.MethodGet, "/v1/deployments/get?name="+name, tn.Admin.Bearer, nil, &d); err != nil || len(d.URLs) == 0 {
		t.Fatalf("the deployment %s has no URL: %v", name, err)
	}
	return d.URLs[0]
}

func deleteApp(t *testing.T, tn *realistic.Tenant, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	r, err := tn.C.Send(ctx, gw.Req{Method: http.MethodDelete, Path: "/v1/deployments/delete?name=" + name, Bearer: tn.Admin.Bearer})
	if err != nil || (r.Status != http.StatusOK && r.Status != http.StatusNotFound) {
		t.Errorf("cleanup: deleting %s: %v %v", name, err, r)
	}
}

// TestChaosLifecycle_nodeKilledMidProvision: a node's supervisor is
// SIGKILLed right after a namespace creation is accepted. Provisioning ends
// (ready or failed, never stuck provisioning); a ready namespace serves real
// requests, a failed one reports why and deletes cleanly; either way a new
// namespace can be created afterwards.
func TestChaosLifecycle_nodeKilledMidProvision(t *testing.T) {
	realistic.RequireFaultBudget(t, "the interrupted provisioning", provisionWorst)
	f := harness.Fleet(t)
	victim := infra.Followers(t, infra.RequireHealthy(t))[0]
	tenancy.Reserve(t, harness.Fleet(t), 2)
	owner := tenancy.Creator(t, f)
	var created tenancy.Created
	if err := tenancy.Create(t, owner, ns.UniqueName(t.Name())).Decode(&created); err != nil || created.ClusterID == "" {
		t.Fatalf("the creation was not accepted: %v %+v", err, created)
	}
	// Registered before the fault: a failure or timeout anywhere below still
	// deletes the accepted namespace (after the node is restarted, LIFO).
	accepted := namespaceOf(f, owner, created)
	t.Cleanup(func() {
		if err := tenancy.DeleteIfPresent(accepted); err != nil {
			t.Errorf("cleanup: namespace %s: %v", accepted.Name, err)
		}
	})
	f.Kill(t, victim, nodeUnit)
	st := waitTerminal(t, owner, created.ClusterID)
	t.Logf("provisioning under a killed %s ended %s (%s)", victim.Name, st.Status, st.Error)
	if st.Status == ns.StatusReady {
		tenancy.Adopt(t, f, owner, created)
	} else {
		deleteFailed(t, accepted, st.Error)
	}
	healed(t, "the cluster after the interrupted provisioning")
	ns.New(t, f, ns.Options{})
}

// waitTerminal polls the provisioning status until it is ready or failed.
func waitTerminal(t *testing.T, owner *gw.User, clusterID string) *ns.ClusterStatus {
	t.Helper()
	var last *ns.ClusterStatus
	eventually.Require(t, pollEvery, provisionTerminal, "provisioning to finish", func() (bool, error) {
		st, err := tenancy.Status(t.Context(), owner.Client, clusterID)
		if err != nil {
			return false, err
		}
		last = st
		if st.Status == ns.StatusReady || st.Status == ns.StatusFailed {
			return true, nil
		}
		return false, fmt.Errorf("status=%s", st.Status)
	})
	return last
}

// deleteFailed deletes a namespace whose provisioning failed and checks it
// said why.
func deleteFailed(t *testing.T, n *ns.Namespace, reason string) {
	t.Helper()
	if strings.TrimSpace(reason) == "" {
		t.Error("provisioning failed without an error")
	}
	if err := tenancy.DeleteIfPresent(n); err != nil {
		t.Errorf("a namespace whose provisioning failed does not delete: %v", err)
	}
}

// namespaceOf is the namespace an accepted creation names, owned by owner.
func namespaceOf(f *fleet.Fleet, owner *gw.User, c tenancy.Created) *ns.Namespace {
	n := &ns.Namespace{Name: c.Name, ClusterID: c.ClusterID, URL: gw.NamespaceURL(f.State, c.Name)}
	n.Client = owner.Client.WithBase(n.URL)
	n.Owner = &gw.User{Wallet: owner.Wallet, Namespace: c.Name, Client: owner.Client}
	return n
}

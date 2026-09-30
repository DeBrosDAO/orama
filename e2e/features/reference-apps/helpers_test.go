//go:build e2e_fleet

package referenceapps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	// feature names the artifact directory the journeys' numbers go to.
	feature = "reference-apps"
	// nodeBinary is what orama-deploy-node@ and orama-deploy-npm@ execute
	// (core/systemd/orama-deploy-node@.service ExecStart).
	nodeBinary = "/usr/bin/node"
	// replicas is how many nodes run a dynamic deployment
	// (core/pkg/deployments/types.go DefaultReplicaCount).
	replicas = 2
	// visitorP95 bounds a page view of a deployed app from the runner.
	visitorP95 = 3 * time.Second
	// deliverBudget bounds a push or a trigger reaching its destination.
	deliverBudget = 2 * time.Minute
	pollEvery     = realistic.PollEvery
	roleRuntime   = "runtime"
	roleReader    = "reader"
)

// requireNPM skips (not covered) when the runner cannot run npm: `orama
// deploy nodejs` and `orama deploy nextjs` install (and build) there.
func requireNPM(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("npm"); err != nil {
		harness.SkipNotApplicable(t, "npm is not on the runner's PATH; `orama deploy nodejs|nextjs` installs dependencies on the deploying machine")
	}
}

// requireNodeRuntime fails when a core node has no Node.js to run a Node
// deployment with: the platform documents Node.js and Next.js SSR apps, and
// their units execute /usr/bin/node, but install only puts curl, wget, unzip
// and sudo on a node (core/pkg/install/prebuilt.go installMinimalDeps).
func requireNodeRuntime(t testing.TB, f *fleet.Fleet) {
	t.Helper()
	for _, n := range f.State.Nodes {
		if out := f.Exec(t, n, "test -x "+nodeBinary); out.Exit != 0 {
			t.Fatalf("%s has no %s: a Node.js deployment's unit cannot start there (orama-deploy-node@.service ExecStart)", n.Name, nodeBinary)
		}
	}
}

// appUnitNodes are the core nodes where the deployment's unit is active.
func appUnitNodes(t testing.TB, tn *realistic.Tenant, runtime, name string) []fleet.Node {
	t.Helper()
	unit := "orama-deploy-" + runtime + "@" + tn.N.Name + "-" + name + ".service"
	var out []fleet.Node
	for _, n := range tn.F.State.Nodes {
		if tn.F.Unit(t, n, unit) == "active" {
			out = append(out, n)
		}
	}
	return out
}

// requireReplicas waits until exactly the home node and its replica run
// the app: the replica is set up over /v1/internal/deployments/replica/setup.
func requireReplicas(t testing.TB, tn *realistic.Tenant, runtime, name string) {
	t.Helper()
	eventually.Require(t, pollEvery, realistic.StartBudget, name+" running on its home node and replica", func() (bool, error) {
		n := len(appUnitNodes(t, tn, runtime, name))
		return n == replicas, fmt.Errorf("%d nodes run it", n)
	})
}

// visitors sends total page views over paths from workers goroutines and
// fails on any error or a slow p95; the numbers go to the artifact dir.
func visitors(t testing.TB, f *fleet.Fleet, c *gw.Client, name string, workers, total int, paths []string) realistic.Summary {
	t.Helper()
	samples := realistic.Burst(t.Context(), workers, total, func(ctx context.Context, _, i int) error {
		r, err := c.Send(ctx, gw.Req{Path: paths[i%len(paths)]})
		if err != nil {
			return err
		}
		if r.Status != http.StatusOK {
			return fmt.Errorf("%s: HTTP %d", paths[i%len(paths)], r.Status)
		}
		return nil
	})
	sum := realistic.Summarize(name, samples, nil)
	realistic.WriteJSON(t, f, feature, name+".json", sum)
	if sum.Errors > 0 || sum.Count != total {
		t.Errorf("visitors saw errors: %s (first: %s)", sum, sum.FirstError)
	}
	if time.Duration(sum.P95MS*float64(time.Millisecond)) > visitorP95 {
		t.Errorf("visitors are slow: %s, want p95 under %s", sum, visitorP95)
	}
	return sum
}

// getJSON GETs path on c with bearer and decodes a 2xx reply into out; the
// status is returned for refusal checks.
func getJSON(ctx context.Context, c *gw.Client, path, bearer string, out any) (int, error) {
	r, err := c.Send(ctx, gw.Req{Path: path, Bearer: bearer})
	if err != nil {
		return 0, err
	}
	if r.Status/100 != 2 {
		return r.Status, fmt.Errorf("GET %s: HTTP %d %.200s", path, r.Status, r.Body)
	}
	if out == nil {
		return r.Status, nil
	}
	return r.Status, json.Unmarshal(r.Body, out)
}

// postJSON POSTs in to path on c with bearer and decodes a 2xx reply.
func postJSON(ctx context.Context, c *gw.Client, path, bearer string, in, out any) (int, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return 0, err
	}
	r, err := c.Send(ctx, gw.Req{Method: http.MethodPost, Path: path, Bearer: bearer,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: raw})
	if err != nil {
		return 0, err
	}
	if r.Status/100 != 2 {
		return r.Status, fmt.Errorf("POST %s: HTTP %d %.200s", path, r.Status, r.Body)
	}
	if out == nil {
		return r.Status, nil
	}
	return r.Status, json.Unmarshal(r.Body, out)
}

// renewal is what a reference app's renew endpoint reports.
type renewal struct {
	Changed   bool     `json:"changed"`
	ExpiresIn int      `json:"expires_in"`
	Principal string   `json:"principal"`
	Role      string   `json:"role"`
	Grants    []string `json:"grants"`
}

// checkRenewal fails unless the app renewed to a new token that the gateway
// accepts as the app, with role and the grants the role resolves to.
func checkRenewal(t testing.TB, r renewal, role string, grants ...string) {
	t.Helper()
	if !r.Changed || r.ExpiresIn <= 0 {
		t.Errorf("the app's renewal did not yield a new live token: %+v", r)
	}
	if r.Principal != "app" || r.Role != role {
		t.Errorf("the renewed token is %q with role %q, want the app principal with %q", r.Principal, r.Role, role)
	}
	have := strings.Join(r.Grants, ",")
	for _, g := range grants {
		if !strings.Contains(","+have+",", ","+g+",") {
			t.Errorf("the renewed token lacks the %s grant: %v", g, r.Grants)
		}
	}
}

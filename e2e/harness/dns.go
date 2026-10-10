package harness

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/broker"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/provision"
	"github.com/DeBrosOfficial/network/e2e/harness/runctx"
)

// Budgets of the brokered helpers.
const (
	dnsBudget     = 2 * time.Minute
	clusterBudget = 45 * time.Minute
)

// Broker is the run's broker. It fails the test outside a fleet run's
// feature process (E2E_BROKER_SOCK unset).
func Broker(t testing.TB) *broker.Client {
	t.Helper()
	requireNotStagenet(t, "the runner's broker")
	c, err := broker.FromEnv(os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatalf("%s is not set: the runner (e2e-fleet run/test) serves the broker to feature packages", broker.EnvSock)
	}
	return c
}

// CustomDomain is e2e-<run>-<label>.<zone>: a name the run owns that
// Cloudflare serves itself (not delegated to the fleet), for a custom domain
// whose TXT proof the gateway resolves over public DNS. label is 1-12
// lowercase letters or digits, starting with a letter.
func CustomDomain(t testing.TB, label string) string {
	t.Helper()
	requireNotStagenet(t, "CustomDomain (a Cloudflare name of the run)")
	f := Fleet(t)
	zone, ok := strings.CutPrefix(f.State.BaseDomain, "e2e-"+f.State.RunID+".")
	if !ok {
		t.Fatalf("base domain %s is not e2e-%s.<zone>", f.State.BaseDomain, f.State.RunID)
	}
	return "e2e-" + f.State.RunID + "-" + label + "." + zone
}

// DNSTXT creates the TXT record name = value through the broker and deletes
// it (that value only) when the test ends. name must be inside the run's
// subdomain or one of its cluster subdomains (CustomDomain); a record under
// the run subdomain itself is only visible through the fleet's own
// nameservers, which do not serve it.
func DNSTXT(t testing.TB, name, value string) {
	t.Helper()
	b := Broker(t)
	ctx, cancel := context.WithTimeout(t.Context(), dnsBudget)
	defer cancel()
	t.Cleanup(func() {
		cctx, ccancel := fleet.CleanupContext(t)
		defer ccancel()
		if err := b.DeleteTXT(cctx, name, value); err != nil {
			t.Errorf("cleanup: failed to delete TXT %s (teardown deletes the run's records): %v", name, err)
		}
	})
	if err := b.SetTXT(ctx, name, value); err != nil {
		t.Fatal(err)
	}
}

// ExtraCluster installs a single-node eval cluster for this test (see
// provision.AddEvalCluster; about fifteen minutes) and removes it when the
// test ends. name is its subdomain label, unique in the run. The manifest
// declares requires.extra_nodes for its server.
func ExtraCluster(t testing.TB, name string) provision.EvalCluster {
	t.Helper()
	requireNotStagenet(t, "ExtraCluster")
	f := Fleet(t)
	own := *f.State
	run, release := runctx.With(t.Context())
	defer release()
	ctx, cancel := context.WithTimeout(run, clusterBudget)
	defer cancel()
	// A failed install removes what it made itself.
	cl, err := provision.AddEvalCluster(ctx, &own, name)
	if err != nil {
		t.Fatal(errors.Join(errors.New("failed to install eval cluster "+name), err))
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), clusterBudget)
		defer ccancel()
		if err := provision.RemoveEvalCluster(cctx, &own, name); err != nil {
			t.Errorf("cleanup: failed to remove eval cluster %s (teardown removes it by label): %v", name, err)
		}
	})
	return cl
}

// requireNotStagenet skips a test that needs a server, DNS record or broker
// operation of its own: the stagenet target only tests the existing cluster,
// so there is nothing to provision, and the test cannot apply.
func requireNotStagenet(t testing.TB, what string) {
	t.Helper()
	if Fleet(t).State.IsStagenet() {
		SkipNotApplicable(t, what+": the stagenet target has no extra server, probe VM or DNS broker: the existing cluster is only tested, never provisioned, changed or destroyed")
	}
}

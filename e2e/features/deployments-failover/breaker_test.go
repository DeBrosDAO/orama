//go:build e2e_fleet

package deploymentsfailover

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

// The gateway that forwards a request for an app to its nodes keeps one circuit
// breaker per deployment and node, opened after 5 consecutive failures of the
// node itself (core/pkg/gateway/circuit_breaker.go defaultFailureThreshold).
// breakerProbes is how many requests a test sends: enough to open one.
const (
	breakerThreshold = 5
	breakerProbes    = breakerThreshold + 10
	// reportBudget: node reports are collected every 10s and cached 5s.
	reportBudget = 2 * time.Minute
	// deploymentSubsystem is the subsystem of the alert about a deployment's
	// open breaker (core/pkg/telemetry/cluster/alerts_node_services.go).
	deploymentSubsystem = "deployment"
)

// outsider is a node that does not run unit: a request pinned to it is
// forwarded to the app's nodes.
func outsider(t *testing.T, f *fleet.Fleet, unit string) fleet.Node {
	t.Helper()
	running := unitNodes(t, f, unit)
	for _, n := range f.State.Nodes {
		if !nodeIn(n, running) {
			return n
		}
	}
	harness.SkipNotApplicable(t, "needs a node that does not run the app, which a fleet with more nodes than the app's replicas gives")
	return fleet.Node{}
}

func nodeIn(n fleet.Node, list []fleet.Node) bool {
	for _, x := range list {
		if x.Name == n.Name {
			return true
		}
	}
	return false
}

// breakerAlerts are the messages of the deployment-breaker alerts raised about
// node by a report collected after since.
func breakerAlerts(t *testing.T, f *fleet.Fleet, node fleet.Node, since time.Time) ([]string, error) {
	t.Helper()
	r, err := monitor.Get(t.Context(), harness.CLI(t), f.State.Env)
	if err != nil {
		return nil, err
	}
	fresh := false
	for _, n := range r.Nodes {
		if n.Host == node.PublicIP && n.Report != nil && n.Report.Timestamp.After(since) {
			fresh = true
		}
	}
	if !fresh {
		return nil, fmt.Errorf("%s has not reported since %s", node.Name, since.Format(time.TimeOnly))
	}
	var out []string
	for _, a := range r.Alerts {
		if a.Node == node.PublicIP && a.Subsystem == deploymentSubsystem && strings.Contains(a.Message, "Circuit breaker") {
			out = append(out, a.Message)
		}
	}
	return out, nil
}

// TestDeployBreaker_anAppsOwn503DoesNotRefuseAnotherApp: an app that answers 503
// is not a node failing. Through a node that does not run it, its 503 reaches
// the client every time, another app of the same namespace is served through
// the same node throughout, and no breaker opens (the node that runs the app
// marks the app's answer, and the forwarding node does not count it).
func TestDeployBreaker_anAppsOwn503DoesNotRefuseAnotherApp(t *testing.T) {
	infra.HealthyAround(t)
	tn := newTenant(t)
	noisy := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "bn"), "noisy")
	quiet := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "bq"), "quiet")
	serving(t, tn.app(noisy), "/health", "")
	serving(t, tn.app(quiet), "/health", "")
	node := outsider(t, tn.f, "orama-deploy-go@"+tn.instance("noisy")+".service")
	cn, cq := tn.app(noisy).PinTo(node.PublicIP), tn.app(quiet).PinTo(node.PublicIP)

	for i := range breakerProbes {
		if r := cn.MustSend(t, gw.Req{Path: "/unavailable"}); r.Status != http.StatusServiceUnavailable {
			t.Fatalf("request %d for the noisy app through %s: HTTP %d, want its own 503", i, node.Name, r.Status)
		}
		if r := cq.MustSend(t, gw.Req{Path: "/health"}); r.Status != http.StatusOK {
			t.Fatalf("request %d for the quiet app through %s: HTTP %d %.120s; the noisy app's 503 refused another app's request", i, node.Name, r.Status, r.Body)
		}
	}
	// The report that follows the requests must show no breaker for either app.
	last := time.Now()
	eventually.Require(t, edge.PollEvery, reportBudget, "a node report after the requests", func() (bool, error) {
		msgs, err := breakerAlerts(t, tn.f, node, last)
		if err != nil {
			return false, err
		}
		if len(msgs) > 0 {
			t.Fatalf("an app's own 503 opened a circuit breaker: %v", msgs)
		}
		return true, nil
	})
}

// TestDeployBreaker_aStoppedAppOpensOnlyItsOwnCircuit: with one app stopped on
// all its nodes the forwarding node does fail to reach it, and its breakers
// open: the operator's report names that app and not the other one, which is
// served through the same node throughout.
func TestDeployBreaker_aStoppedAppOpensOnlyItsOwnCircuit(t *testing.T) {
	infra.HealthyAround(t)
	tn := newTenant(t)
	down := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "bd"), "down")
	up := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "bu"), "up")
	serving(t, tn.app(down), "/health", "")
	serving(t, tn.app(up), "/health", "")
	unit := "orama-deploy-go@" + tn.instance("down") + ".service"
	node := outsider(t, tn.f, unit)
	for _, n := range unitNodes(t, tn.f, unit) {
		tn.f.HoldDown(t, n, unit)
	}
	cd, cu := tn.app(down).PinTo(node.PublicIP), tn.app(up).PinTo(node.PublicIP)

	for i := range breakerProbes {
		if r := cd.MustSend(t, gw.Req{Path: "/health"}); r.Status != http.StatusServiceUnavailable {
			t.Fatalf("request %d for the stopped app: HTTP %d, want 503", i, r.Status)
		}
		if r := cu.MustSend(t, gw.Req{Path: "/health"}); r.Status != http.StatusOK {
			t.Fatalf("request %d for the other app through %s: HTTP %d %.120s; the stopped app refused it", i, node.Name, r.Status, r.Body)
		}
	}
	downName := tn.n.Name + "/down"
	last := time.Now()
	eventually.Require(t, edge.PollEvery, reportBudget, "the report to name the stopped app's open breaker", func() (bool, error) {
		msgs, err := breakerAlerts(t, tn.f, node, last)
		if err != nil {
			return false, err
		}
		named := false
		for _, m := range msgs {
			if strings.Contains(m, tn.n.Name+"/up") {
				t.Fatalf("the alert names the app that is running: %s", m)
			}
			named = named || strings.Contains(m, downName)
		}
		if !named {
			return false, fmt.Errorf("no breaker alert for %s: %v", downName, msgs)
		}
		return true, nil
	})
}

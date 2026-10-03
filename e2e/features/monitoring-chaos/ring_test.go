//go:build e2e_fleet

package monitoringchaos

import (
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// The ring failure detector (docs/MONITORING.md "In-cluster failure
// detection"): probes every 10s, suspect after 3 misses, but a heartbeat
// younger than 65s outranks the probe, so a node that stops answering is
// suspect after about 65s + 30s.
const (
	heartbeatTrust = 65 * time.Second
	suspectMisses  = 3 * 10 * time.Second
	suspectBudget  = heartbeatTrust + suspectMisses + time.Minute
	recoverBudget  = 2 * time.Minute
	// restartWatch is how long an injected restart is watched for a false
	// positive: longer than the suspect threshold.
	restartWatch = heartbeatTrust + suspectMisses + 30*time.Second
)

// nodeID is n's id in dns_nodes (the ring's target_id).
func nodeID(t *testing.T, f *fleet.Fleet, observer, n fleet.Node) string {
	t.Helper()
	q := infra.IndexQuery(t, f, observer, "SELECT id FROM dns_nodes WHERE internal_ip = ?", n.WGIP)
	if len(q.Values) != 1 {
		t.Fatalf("dns_nodes has %d rows for %s (%s)", len(q.Values), n.Name, n.WGIP)
	}
	id, _ := q.Values[0][0].(string)
	return id
}

// events counts target's node_health_events of status since the SQLite
// timestamp since.
func events(t *testing.T, f *fleet.Fleet, observer fleet.Node, target, status, since string) (int, error) {
	q, err := infra.IndexQueryAt(t, f, observer, "strong",
		"SELECT COUNT(*) FROM node_health_events WHERE target_id = ? AND status = ? AND created_at >= ?", target, status, since)
	if err != nil {
		return 0, err
	}
	n, _ := q.Values[0][0].(float64)
	return int(n), nil
}

// sqliteNow is the registry's clock, in the format created_at is written in.
func sqliteNow(t *testing.T, f *fleet.Fleet, observer fleet.Node) string {
	t.Helper()
	q := infra.IndexQuery(t, f, observer, "SELECT datetime('now')")
	s, _ := q.Values[0][0].(string)
	return s
}

// TestRing_hungGatewaySuspectedThenRecovered: a node whose cluster gateway
// hangs (stopped with SIGSTOP, so the unit stays active and nothing restarts
// it) stops answering the ring's /v1/internal/ping and its heartbeat, and
// its peers record it suspect within the documented bound; resumed, it is
// recorded recovered, and it was never declared dead (docs/MONITORING.md
// "In-cluster failure detection": suspect ~30s after the heartbeat goes
// stale, dead only after 12 misses).
func TestRing_hungGatewaySuspectedThenRecovered(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	victim := infra.Followers(t, r)[0]
	observer := infra.Leader(t, r)
	id := nodeID(t, f, observer, victim)
	since := sqliteNow(t, f, observer)
	t.Cleanup(func() {
		infra.ConvergeInCleanup(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster after the hung gateway")
	})
	tenancy.Freeze(t, f, victim, edge.IndexGatewayUnit)
	start := time.Now()
	eventually.Require(t, edge.PollEvery, suspectBudget, victim.Name+" suspect", func() (bool, error) {
		n, err := events(t, f, observer, id, "suspect", since)
		if err != nil || n == 0 {
			return false, fmt.Errorf("suspect events %d: %v", n, err)
		}
		return true, nil
	})
	t.Logf("%s recorded suspect %s after its gateway hung", victim.Name, time.Since(start).Round(time.Second))
	tenancy.Thaw(t, f, victim, edge.IndexGatewayUnit)
	eventually.Require(t, edge.PollEvery, recoverBudget, victim.Name+" recovered", func() (bool, error) {
		n, err := events(t, f, observer, id, "recovered", since)
		if err != nil || n == 0 {
			return false, fmt.Errorf("recovered events %d: %v", n, err)
		}
		return true, nil
	})
	if n, err := events(t, f, observer, id, "dead", since); err != nil || n != 0 {
		t.Errorf("%s was declared dead %d times (%v) although it hung for less than 12 misses", victim.Name, n, err)
	}
}

// TestRing_injectedGatewayUnitRestartIsNoFalsePositive: restarting a node's
// cluster gateway is not seen as a failure: no suspect and no dead event for
// it over longer than the suspect threshold. This is fault injection, not the
// operator path: the test restarts the one unit the ring probes with a raw
// `systemctl restart` on the node, the gateway restart a rolling upgrade
// causes, without `orama node restart`'s quorum checks and ordering around
// the node's other services, which the ring does not watch.
func TestRing_injectedGatewayUnitRestartIsNoFalsePositive(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	victim := infra.Followers(t, r)[0]
	observer := infra.Leader(t, r)
	id := nodeID(t, f, observer, victim)
	since := sqliteNow(t, f, observer)
	// Injection: a raw unit restart (see above).
	f.MustExec(t, victim, "systemctl restart "+edge.IndexGatewayUnit)
	edge.Hold(t, edge.PollEvery, restartWatch, "no suspect or dead event for "+victim.Name, func() (bool, error) {
		for _, status := range []string{"suspect", "dead"} {
			n, err := events(t, f, observer, id, status, since)
			if err != nil {
				return false, err
			}
			if n > 0 {
				return false, fmt.Errorf("%d %s events after a gateway unit restart", n, status)
			}
		}
		return true, nil
	})
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster after the restart")
}

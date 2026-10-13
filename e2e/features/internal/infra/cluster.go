//go:build e2e_fleet

package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

// Healthy is `orama status --json`'s status of a serving node
// (core/pkg/telemetry/cluster/snapshot.go HealthHealthy).
const Healthy = "healthy"

// WaitConverged polls the operator's monitor report until the cluster has
// settled with want nodes and every node names the same leader, and returns
// that report. It is the lifecycle harness's "the cluster came back".
func WaitConverged(t testing.TB, want int, budget time.Duration, what string) *monitor.Report {
	t.Helper()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	var last *monitor.Report
	eventually.Require(t, PollEvery, budget, what, func() (bool, error) {
		r, err := monitor.Get(t.Context(), cli, f.State.Env)
		if err != nil {
			return false, err
		}
		if err := r.Converged(want); err != nil {
			return false, err
		}
		if err := r.LeaderAgreement(); err != nil {
			return false, err
		}
		last = r
		return true, nil
	})
	return last
}

// RequireHealthy fails at once unless the cluster is converged with its core
// nodes: a destructive test must start from a healthy cluster, or what it
// observes is the previous test's damage.
func RequireHealthy(t testing.TB) *monitor.Report {
	t.Helper()
	f := harness.Fleet(t)
	return WaitConverged(t, len(f.State.Nodes), ConvergeBudget, "the cluster to be healthy before the test")
}

// HealthyAround is RequireHealthy for a fault test, plus a cleanup that
// waits for the cluster to converge again once the test's faults are undone.
// Call it before injecting anything: cleanups run last-in first-out, so the
// wait runs after every restore the faults registered.
func HealthyAround(t testing.TB) *monitor.Report {
	t.Helper()
	f := harness.Fleet(t)
	r := RequireHealthy(t)
	t.Cleanup(func() {
		ConvergeInCleanup(t, len(f.State.Nodes), ConvergeBudget, "the cluster after the test's faults were undone")
	})
	return r
}

// Leader is the fleet node the report names as rqlite leader.
func Leader(t testing.TB, r *monitor.Report) fleet.Node {
	t.Helper()
	if !r.HasLeader() {
		t.Fatalf("the monitor report names no rqlite leader: %+v", r.Summary)
	}
	n, err := NodeByHost(harness.Fleet(t), r.Summary.RQLiteLeader)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Followers are the core nodes that are not the leader.
func Followers(t testing.TB, r *monitor.Report) []fleet.Node {
	t.Helper()
	leader := Leader(t, r)
	var out []fleet.Node
	for _, n := range harness.Fleet(t).State.Nodes {
		if n.Name != leader.Name {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		t.Fatal("the cluster has no follower")
	}
	return out
}

// ReportFor is the report entry of node n, by public or WG address.
func ReportFor(r *monitor.Report, n fleet.Node) (*monitor.Node, error) {
	for i := range r.Nodes {
		h := r.Nodes[i].Host
		if h == n.PublicIP || (n.WGIP != "" && h == n.WGIP) {
			return &r.Nodes[i], nil
		}
	}
	return nil, fmt.Errorf("%s (%s) is not in the monitor report", n.Name, n.PublicIP)
}

// Baseline is what stage 1 found on the fresh cluster, recorded for the
// later stages (the upgrade stage compares against it).
type Baseline struct {
	RunID      string            `json:"run_id"`
	Version    string            `json:"version"`
	Leader     string            `json:"leader"`
	WGIPs      map[string]string `json:"wg_ips"`
	RaftIDs    map[string]string `json:"raft_ids"`
	ChainID    string            `json:"chain_id,omitempty"`
	ChainStart int64             `json:"chain_height,omitempty"`
	RecordedAt time.Time         `json:"recorded_at"`
}

// BaselineFile is the file bootstrap writes in the run's artifact dir.
const BaselineFile = "bootstrap-baseline.json"

// WriteBaseline records b in the run's artifact dir (0600: it names addresses).
func WriteBaseline(st *fleet.State, b Baseline) error {
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode the bootstrap baseline: %w", err)
	}
	path := filepath.Join(st.ArtifactDir, BaselineFile)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("failed to write the bootstrap baseline %s: %w", path, err)
	}
	return nil
}

// ReadBaseline reads what bootstrap recorded. A missing file means stage 1
// did not run in this run (a --stage N run against an existing fleet).
func ReadBaseline(st *fleet.State) (Baseline, error) {
	var b Baseline
	raw, err := os.ReadFile(filepath.Join(st.ArtifactDir, BaselineFile))
	if errors.Is(err, os.ErrNotExist) {
		return b, fmt.Errorf("stage 1 (bootstrap) recorded no %s in %s: %w", BaselineFile, st.ArtifactDir, err)
	}
	if err != nil {
		return b, fmt.Errorf("failed to read the bootstrap baseline: %w", err)
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, fmt.Errorf("failed to parse the bootstrap baseline: %w", err)
	}
	return b, nil
}

// ConvergeInCleanup waits like WaitConverged from a t.Cleanup: it reports a
// cluster that did not come back with Errorf (a Fatal would skip the
// remaining cleanups) under a context of its own.
func ConvergeInCleanup(t testing.TB, want int, budget time.Duration, what string) {
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), budget+time.Minute)
	defer cancel()
	err := eventually.Poll(ctx, PollEvery, budget, what, func() (bool, error) {
		r, err := monitor.Get(ctx, cli, f.State.Env)
		if err != nil {
			return false, err
		}
		if err := r.Converged(want); err != nil {
			return false, err
		}
		return true, r.LeaderAgreement()
	})
	if err != nil {
		t.Errorf("cleanup: %v — later tests will see a disturbed cluster", err)
	}
}

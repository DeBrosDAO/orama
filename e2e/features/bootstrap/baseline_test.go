//go:build e2e_fleet

package bootstrap

import (
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// TestBootstrap_raftIdentitiesAreStable: a fresh install starts every index
// rqlite under its libp2p peer id, not under its raft address, and records
// the id beside raft.db (docs/CLI_REFERENCE.md "orama node migrate-raft-id":
// identity must survive an address change; core/pkg/rqlite/identity.go).
func TestBootstrap_raftIdentitiesAreStable(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	r := infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the fresh cluster to converge")
	seen := map[string]string{}
	for _, entry := range r.Nodes {
		n, err := infra.NodeByHost(f, entry.Host)
		if err != nil {
			t.Fatal(err)
		}
		id := entry.Report.RQLite.NodeID
		if id == "" || strings.Contains(id, ":") {
			t.Errorf("%s: raft id %q is an address, not a stable peer id", n.Name, id)
		}
		if other, dup := seen[id]; dup {
			t.Errorf("%s and %s share raft id %s", n.Name, other, id)
		}
		seen[id] = n.Name
		marker := strings.TrimSpace(string(f.ReadFile(t, n, infra.CoreRQLiteDir+"/raft-node-id")))
		if marker != id {
			t.Errorf("%s: raft-node-id marker %q, rqlite reports %q", n.Name, marker, id)
		}
		if !entry.Report.RQLite.Voter {
			t.Errorf("%s is not a voter: a three-node cluster needs all three", n.Name)
		}
	}
}

// TestBootstrap_recordBaseline writes what the fresh cluster looks like to
// the run's artifact dir, for the upgrade stage to compare against: the
// release, the leader, every node's WireGuard address and raft id, and the
// chain height.
func TestBootstrap_recordBaseline(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	r := infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the fresh cluster to converge")
	b := infra.Baseline{
		RunID: f.State.RunID, Leader: infra.Leader(t, r).Name, ChainID: f.State.ChainID,
		WGIPs: map[string]string{}, RaftIDs: map[string]string{}, RecordedAt: time.Now().UTC(),
	}
	for _, entry := range r.Nodes {
		n, err := infra.NodeByHost(f, entry.Host)
		if err != nil {
			t.Fatal(err)
		}
		b.WGIPs[n.Name] = entry.Report.WGIP
		b.RaftIDs[n.Name] = entry.Report.RQLite.NodeID
		if b.Version == "" {
			b.Version = entry.Report.Version
		}
		if c := entry.Report.Chain; c != nil && c.LatestHeight > b.ChainStart {
			b.ChainStart = c.LatestHeight
		}
	}
	if err := infra.WriteBaseline(f.State, b); err != nil {
		t.Fatal(err)
	}
	got, err := infra.ReadBaseline(f.State)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunID != f.State.RunID || len(got.WGIPs) != len(f.State.Nodes) {
		t.Fatalf("the baseline read back is %+v", got)
	}
}

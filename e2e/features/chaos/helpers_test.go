//go:build e2e_fleet

package chaos

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	pollEvery = 5 * time.Second
	// serveBudget bounds the survivors answering again after a fault starts:
	// a raft election and the gateways noticing the lost peer.
	serveBudget = 2 * time.Minute
	// recoverBudget bounds a killed unit coming back by itself: systemd's
	// Restart= or the node's supervisor, before the harness would start it.
	recoverBudget  = 2 * time.Minute
	convergeBudget = infra.ConvergeBudget
	// faultWorst is the most one fault of this package can take with its
	// recovery: the nodes serving through and after it (serveBudget each,
	// three nodes), its own wait (the disk alert raising and clearing, or a
	// unit's restart), and the convergence after it. No fault starts when
	// less of the stage budget is left (realistic.RequireFaultBudget).
	faultWorst = 3*serveBudget + 2*alertBudget + recoverBudget + convergeBudget
	tableDDL   = "CREATE TABLE IF NOT EXISTS chaos (key TEXT PRIMARY KEY, phase TEXT NOT NULL)"
)

// others are the core nodes other than victim.
func others(f *fleet.Fleet, victim fleet.Node) []fleet.Node {
	var out []fleet.Node
	for _, n := range f.State.Nodes {
		if n.Name != victim.Name {
			out = append(out, n)
		}
	}
	return out
}

// requireServing waits until every node in nodes answers /v1/health through
// its own Caddy and gateway.
func requireServing(t testing.TB, nodes []fleet.Node, what string) {
	t.Helper()
	c := harness.GW(t)
	for _, n := range nodes {
		pinned := c.PinTo(n.PublicIP)
		eventually.Require(t, pollEvery, serveBudget, n.Name+" serving "+what, func() (bool, error) {
			r, err := pinned.Send(t.Context(), gw.Req{Path: "/v1/health"})
			if err != nil {
				return false, err
			}
			return r.Status == http.StatusOK, fmt.Errorf("HTTP %d", r.Status)
		})
	}
}

// store is a namespace's database, written and read through chosen nodes.
type store struct {
	n *ns.Namespace
}

func newStore(t testing.TB) *store {
	t.Helper()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	s := &store{n: n}
	if _, err := n.Client.JSON(t.Context(), http.MethodPost, "/v1/rqlite/create-table", n.Owner.Token(), map[string]string{"schema": tableDDL}, nil); err != nil {
		t.Fatal(err)
	}
	return s
}

// write inserts row key tagged phase through node, waiting out an
// election. The insert is idempotent, so an attempt whose answer was lost
// cannot count twice.
func (s *store) write(t testing.TB, node fleet.Node, phase, key string) {
	t.Helper()
	c := s.n.Client.PinTo(node.PublicIP)
	eventually.Require(t, pollEvery, serveBudget, "a write through "+node.Name+" ("+phase+")", func() (bool, error) {
		_, err := c.JSON(t.Context(), http.MethodPost, "/v1/rqlite/exec", s.n.Owner.Token(),
			map[string]any{"sql": "INSERT OR IGNORE INTO chaos (key, phase) VALUES (?, ?)", "args": []any{key, phase}}, nil)
		return err == nil, err
	})
}

// writeN writes n rows of phase through node.
func (s *store) writeN(t testing.TB, node fleet.Node, phase string, n int) {
	t.Helper()
	for i := range n {
		s.write(t, node, phase, fmt.Sprintf("%s-%d", phase, i))
	}
}

// count reads how many rows carry phase through node.
func (s *store) count(ctx context.Context, node fleet.Node, phase string) (int, error) {
	var out struct {
		Items []map[string]any `json:"items"`
	}
	_, err := s.n.Client.PinTo(node.PublicIP).JSON(ctx, http.MethodPost, "/v1/rqlite/query", s.n.Owner.Token(),
		map[string]any{"sql": "SELECT key FROM chaos WHERE phase = ?", "args": []any{phase}}, &out)
	return len(out.Items), err
}

// requireRows waits until node reads want rows of phase: the healed node
// has caught up on what was written while it was cut off.
func (s *store) requireRows(t testing.TB, node fleet.Node, phase string, want int) {
	t.Helper()
	eventually.Require(t, pollEvery, convergeBudget, node.Name+" reading the "+phase+" rows", func() (bool, error) {
		n, err := s.count(t.Context(), node, phase)
		if err != nil {
			return false, err
		}
		return n == want, fmt.Errorf("%d rows, want %d", n, want)
	})
}

// healed waits until the whole cluster is converged with every node.
func healed(t testing.TB, what string) {
	t.Helper()
	infra.WaitConverged(t, len(harness.Fleet(t).State.Nodes), convergeBudget, what)
}

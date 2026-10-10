//go:build e2e_fleet

package rqliteraftdestructive

import (
	"context"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// unitActive is systemctl's state of a running unit.
const unitActive = "active"

// stopped tracks the nodes a test stopped; the cleanup starts them all at
// once (a node's start waits until it serves, which may need the others back)
// and then waits for the whole cluster to converge.
type stopped struct {
	mu    sync.Mutex
	nodes []fleet.Node
}

func newStopped(t testing.TB) *stopped {
	t.Helper()
	s := &stopped{}
	t.Cleanup(func() { s.restore(t) })
	return s
}

// stop runs `orama node stop` on n (with --force when asked) and records it
// when the node really stopped.
func (s *stopped) stop(t testing.TB, n fleet.Node, force bool) fleet.Output {
	t.Helper()
	f := harness.Fleet(t)
	args := []string{"node", "stop"}
	if force {
		args = append(args, "--force")
	}
	s.mu.Lock()
	s.nodes = append(s.nodes, n)
	s.mu.Unlock()
	return infra.OnNode(t, f, n, args...)
}

func (s *stopped) restore(t testing.TB) {
	f := harness.Fleet(t)
	var wg sync.WaitGroup
	for _, n := range s.nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), infra.ConvergeBudget)
			defer cancel()
			out, err := f.SSH(ctx, n).Run(ctx, infra.OramaCommand("node", "start"))
			if err != nil || out.Exit != 0 {
				t.Errorf("cleanup: orama node start on %s failed (exit %d): %v\n%s", n.Name, out.Exit, err, f.Redact(out.Stdout+out.Stderr))
			}
		}()
	}
	wg.Wait()
	if len(s.nodes) > 0 {
		infra.ConvergeInCleanup(t, len(f.State.Nodes), infra.ColdStartBudget, "the cluster after restarting the stopped nodes")
	}
}

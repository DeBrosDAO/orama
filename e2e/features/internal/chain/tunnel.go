//go:build e2e_fleet

package chain

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Tunnel forwards a local loopback port on the runner to the chain's port on
// node n (Host, 127.0.0.1 in a fleet run) through SSH (harness fleet.Tunnel:
// the run's key and its pinned known_hosts), so the CLI under test can reach
// the chain's node-local REST API (31003) or RPC (31001) the way an operator
// on the node would. It
// returns "127.0.0.1:<local port>" and closes at cleanup.
func (c *Chain) Tunnel(t testing.TB, n fleet.Node, port int) string {
	t.Helper()
	return c.F.Tunnel(t, n, fmt.Sprintf("%s:%d", c.Host(), port))
}

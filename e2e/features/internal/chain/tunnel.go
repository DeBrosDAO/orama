//go:build e2e_fleet

package chain

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Tunnel forwards a local loopback port on the runner to 127.0.0.1:port on
// node n through SSH (harness fleet.Tunnel: the run's key and its pinned
// known_hosts), so the CLI under test can reach the chain's loopback-only
// REST API (31003) or RPC (31001) the way an operator on the node would. It
// returns "127.0.0.1:<local port>" and closes at cleanup.
func (c *Chain) Tunnel(t testing.TB, n fleet.Node, port int) string {
	t.Helper()
	return c.F.Tunnel(t, n, fmt.Sprintf("127.0.0.1:%d", port))
}

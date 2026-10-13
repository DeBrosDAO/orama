//go:build e2e_fleet

package tenancy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// dialBudget bounds the TCP connect of a node-pinned client.
const dialBudget = 15 * time.Second

// PinnedDial dials ip:443 whatever address it is asked for.
func PinnedDial(ip string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	target, d := net.JoinHostPort(ip, "443"), &net.Dialer{Timeout: dialBudget}
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return d.DialContext(ctx, network, target)
	}
}

// NodeClient is a client pinned to one core node.
type NodeClient struct {
	Node   fleet.Node
	Client *gw.Client
}

// PerNode returns c pinned to each core node in turn (gw.Client.PinTo: the
// evidence of every request names the node it was pinned to).
func PerNode(t testing.TB, f *fleet.Fleet, c *gw.Client) []NodeClient {
	t.Helper()
	out := make([]NodeClient, 0, len(f.State.Nodes))
	for _, n := range f.State.Nodes {
		out = append(out, NodeClient{Node: n, Client: c.PinTo(n.PublicIP)})
	}
	return out
}

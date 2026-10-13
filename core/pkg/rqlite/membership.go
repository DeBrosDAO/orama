package rqlite

import (
	"context"
	"fmt"
	"strings"
)

// VerifyJoined confirms the node at ep is in the join target's raft cluster:
// its configuration lists a member at joinAddr. A node whose join failed and
// that bootstrapped instead is ready — leader of a cluster of one — and passes
// every readiness check; this is the one that tells it apart.
func VerifyJoined(ctx context.Context, ep Endpoint, joinAddr string) error {
	nodes, err := ep.Admin().Nodes(ctx)
	if err != nil {
		return fmt.Errorf("read the raft configuration at %s: %w", ep.HostPort(), err)
	}
	addrs := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.Addr == joinAddr {
			return nil
		}
		addrs = append(addrs, n.Addr)
	}
	return fmt.Errorf("the raft cluster at %s is [%s] and does not include the join target %s: this node started a cluster of its own instead of joining; wipe it and join again",
		ep.HostPort(), strings.Join(addrs, ", "), joinAddr)
}

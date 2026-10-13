package updateagent

import (
	"context"
	"fmt"
	"net"

	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// raftView reads the raft configuration from this node's index RQLite.
type raftView struct {
	admin *rqlite.AdminClient
}

var _ autoupdate.Raft = raftView{}

// View is the leader's overlay address and the voters, counting those that
// answer as healthy. A configuration with no leader has an empty LeaderHost,
// which the rollout plan refuses.
func (r raftView) View(ctx context.Context) (autoupdate.RaftView, error) {
	status, err := r.admin.Status(ctx)
	if err != nil {
		return autoupdate.RaftView{}, fmt.Errorf("read the raft status: %w", err)
	}
	nodes, err := r.admin.Nodes(ctx)
	if err != nil {
		return autoupdate.RaftView{}, fmt.Errorf("read the raft nodes: %w", err)
	}
	view := autoupdate.RaftView{LeaderHost: leaderHost(status)}
	for _, n := range nodes {
		if !n.Voter {
			continue
		}
		view.Voters++
		if n.Reachable {
			view.HealthyVoters++
		}
	}
	return view, nil
}

// leaderHost is the host of the leader's raft address, "" when there is none.
func leaderHost(s *rqlite.RQLiteStatus) string {
	addr := s.Store.Leader.Addr
	if addr == "" {
		addr = s.Store.Raft.LeaderAddr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return host
}

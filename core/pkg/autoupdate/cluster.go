package autoupdate

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rollout"
)

// Install states recorded in release_installs.
const (
	// StateInstalled: the node runs the release and passed its health gate.
	StateInstalled = "installed"
	// StateFailed: the node installed the release and rolled back. Any failed
	// row makes the release bad for every node.
	StateFailed = "failed"
	// StateSkipped: the node does not install releases by itself (a validator),
	// and says so, so that the rollout does not wait for it.
	StateSkipped = "skipped"
)

// Member is a node of the cluster as the registry (dns_nodes) knows it.
type Member struct {
	ID         string
	InternalIP string
	Role       string
	// Live is a node whose status is active and that heartbeated recently.
	Live bool
}

// RaftView is what the node's index RQLite says of the raft configuration.
type RaftView struct {
	// LeaderHost is the overlay address of the leader, "" when there is none.
	LeaderHost string
	// Voters and HealthyVoters count the voters and those that are reachable.
	Voters, HealthyVoters int
}

// Store is the cluster's shared state: the registry, the stored policy, the
// install record and the lock. The production Store is SQLStore, over the
// index RQLite.
type Store interface {
	// Stored is the cluster's auto-update settings by key.
	Stored(ctx context.Context) (map[string]string, error)
	// Members is the registered, not retired, nodes.
	Members(ctx context.Context) ([]Member, error)
	// Installs is the state each node recorded for version, by node id.
	Installs(ctx context.Context, version string) (map[string]string, error)
	// Record writes a node's state for version.
	Record(ctx context.Context, version, nodeID, state, detail string) error
	// Lock takes the cluster-wide rollout lock for holder, and fails at once
	// when another node holds it. The release function frees it.
	Lock(ctx context.Context, holder string) (release func(context.Context) error, err error)
}

// Raft reads the raft configuration from this node's RQLite.
type Raft interface {
	View(ctx context.Context) (RaftView, error)
}

// ClusterHealth is what Decide needs to know, from the registry and raft: the
// cluster is degraded when any member is not live.
func ClusterHealth(members []Member, raft RaftView) Health {
	degraded := len(members) == 0
	for _, m := range members {
		if !m.Live {
			degraded = true
		}
	}
	return Health{Degraded: degraded, Voters: raft.Voters, HealthyVoters: raft.HealthyVoters}
}

// NextNode is the member whose turn it is to install a release: the first in
// the rollout plan (followers before the leader, nameservers spaced) that has
// not recorded the release as installed or skipped. ok is false when every
// member has. A skipped member (a validator, upgraded by hand) is done for the
// order: waiting for it would stall every node after it.
func NextNode(members []Member, raft RaftView, installs map[string]string) (next Member, ok bool, err error) {
	byHost := make(map[string]Member, len(members))
	nodes := make([]inspector.Node, 0, len(members))
	roles := make(map[string]rollout.RaftRole, len(members))
	for _, m := range members {
		if m.InternalIP == "" {
			return Member{}, false, fmt.Errorf("node %s has no overlay address in the registry", m.ID)
		}
		byHost[m.InternalIP] = m
		nodes = append(nodes, inspector.Node{Host: m.InternalIP, Role: m.Role})
		roles[m.InternalIP] = rollout.RoleFollower
		if m.InternalIP == raft.LeaderHost {
			roles[m.InternalIP] = rollout.RoleLeader
		}
	}
	plan, err := rollout.Build(nodes, roles)
	if err != nil {
		return Member{}, false, fmt.Errorf("plan the rollout: %w", err)
	}
	for _, step := range plan.Steps {
		m := byHost[step.Node.Host]
		if state := installs[m.ID]; state != StateInstalled && state != StateSkipped {
			return m, true, nil
		}
	}
	return Member{}, false, nil
}

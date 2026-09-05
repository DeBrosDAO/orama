package monitor

import (
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/production/report"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// CollectionStatus tracks the SSH collection result for a single node.
type CollectionStatus struct {
	Node     inspector.Node
	Report   *report.NodeReport
	Error    error
	Duration time.Duration
	Retries  int
}

// ClusterSnapshot is the aggregated state of the entire cluster at a point in time.
type ClusterSnapshot struct {
	Environment string
	CollectedAt time.Time
	Duration    time.Duration
	Nodes       []CollectionStatus
	Alerts      []Alert
}

// Healthy returns only nodes that reported successfully.
func (cs *ClusterSnapshot) Healthy() []*report.NodeReport {
	var out []*report.NodeReport
	for _, n := range cs.Nodes {
		if n.Report != nil {
			out = append(out, n.Report)
		}
	}
	return out
}

// Failed returns nodes where SSH or parsing failed.
func (cs *ClusterSnapshot) Failed() []CollectionStatus {
	var out []CollectionStatus
	for _, n := range cs.Nodes {
		if n.Error != nil {
			out = append(out, n)
		}
	}
	return out
}

// ByHost returns a map of host -> NodeReport for quick lookup.
func (cs *ClusterSnapshot) ByHost() map[string]*report.NodeReport {
	m := make(map[string]*report.NodeReport, len(cs.Nodes))
	for _, n := range cs.Nodes {
		if n.Report != nil {
			m[n.Node.Host] = n.Report
		}
	}
	return m
}

// HealthyCount returns the number of nodes that reported successfully.
func (cs *ClusterSnapshot) HealthyCount() int {
	count := 0
	for _, n := range cs.Nodes {
		if n.Report != nil {
			count++
		}
	}
	return count
}

// TotalCount returns the total number of nodes attempted.
func (cs *ClusterSnapshot) TotalCount() int {
	return len(cs.Nodes)
}

// NodeHealth is the one-word verdict for a node, the summary `orama status`
// shows. It answers "can this node serve traffic and is it part of the raft
// cluster", which is coarser than the per-subsystem detail in the full report.
type NodeHealth string

const (
	// HealthUnreachable means the SSH collection itself failed.
	HealthUnreachable NodeHealth = "unreachable"
	// HealthHealthy means the gateway answers and raft has a settled role.
	HealthHealthy NodeHealth = "healthy"
	// HealthDegraded means the node answered but is not fully serving.
	HealthDegraded NodeHealth = "degraded"
)

// Health classifies one node's collection result.
func (c CollectionStatus) Health() NodeHealth {
	if c.Error != nil || c.Report == nil {
		return HealthUnreachable
	}
	gatewayUp := c.Report.Gateway != nil && c.Report.Gateway.Responsive
	raftSettled := c.Report.RQLite != nil &&
		(c.Report.RQLite.RaftState == "Leader" || c.Report.RQLite.RaftState == "Follower")
	if gatewayUp && raftSettled {
		return HealthHealthy
	}
	return HealthDegraded
}

// Detail is the human-readable reason a node is not healthy, empty when it is.
func (c CollectionStatus) Detail() string {
	switch c.Health() {
	case HealthHealthy:
		return ""
	case HealthUnreachable:
		if c.Error != nil {
			return c.Error.Error()
		}
		return "no report returned"
	}

	var missing []string
	if c.Report.Gateway == nil || !c.Report.Gateway.Responsive {
		missing = append(missing, "gateway down")
	}
	if c.Report.RQLite == nil {
		missing = append(missing, "no rqlite report")
	} else if c.Report.RQLite.RaftState != "Leader" && c.Report.RQLite.RaftState != "Follower" {
		state := c.Report.RQLite.RaftState
		if state == "" {
			state = "unknown"
		}
		missing = append(missing, "raft "+state)
	}
	return strings.Join(missing, ", ")
}

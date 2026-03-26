package monitor

import (
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

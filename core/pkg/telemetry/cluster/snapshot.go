// Package cluster turns per-node health reports into a view of the whole
// cluster: which nodes answered, what each reported, and the alerts derived
// from comparing them. The same types are built by the cluster gateway from
// its peers' telemetry and by `orama monitor --ssh` from reports collected over
// SSH, and they travel between the two as JSON.
package cluster

import (
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// NodeRef identifies a node in a snapshot.
type NodeRef struct {
	// Host is the node's public address, the name operators know it by.
	Host string `json:"host"`
	// Role is "node" or "nameserver-<n>".
	Role string `json:"role,omitempty"`
	// WGIP is the node's WireGuard address.
	WGIP string `json:"wg_ip,omitempty"`
}

// CollectionStatus is the result of collecting one node's report.
type CollectionStatus struct {
	Node   NodeRef            `json:"node"`
	Report *report.NodeReport `json:"report,omitempty"`
	// Err says why there is no report. Empty when Report is set.
	Err        string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Retries    int    `json:"retries,omitempty"`
	// ReportAgeSec is how old the report was when it was collected: a node's
	// telemetry is gathered on a timer, so this is the staleness a viewer sees.
	ReportAgeSec int `json:"report_age_sec,omitempty"`
	// ClockOffsetMS is the node's clock minus the collector's, measured when
	// the report was served (to within the request's round trip). It is what
	// the clock-skew alert compares: nodes collect on their own timers, so
	// report timestamps differ by up to an interval on synchronised clocks.
	ClockOffsetMS int64 `json:"clock_offset_ms,omitempty"`
	// ClockMeasured says ClockOffsetMS was measured.
	ClockMeasured bool `json:"clock_measured,omitempty"`
	// Unknown means the node answered but serves no telemetry — it runs an
	// older release, as mid-way through a rolling upgrade — so its state is
	// not known. It counts neither for nor against any service.
	Unknown bool `json:"unknown,omitempty"`
}

// ClusterSnapshot is the aggregated state of the entire cluster at a point in time.
type ClusterSnapshot struct {
	Environment string             `json:"environment,omitempty"`
	CollectedAt time.Time          `json:"collected_at"`
	DurationMS  int64              `json:"duration_ms"`
	Nodes       []CollectionStatus `json:"nodes"`
	Alerts      []Alert            `json:"alerts"`
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

// Failed returns nodes whose collection failed.
func (cs *ClusterSnapshot) Failed() []CollectionStatus {
	var out []CollectionStatus
	for _, n := range cs.Nodes {
		if n.Err != "" {
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
	return len(cs.Healthy())
}

// UnknownCount returns the number of nodes on a release without telemetry.
func (cs *ClusterSnapshot) UnknownCount() int {
	n := 0
	for _, c := range cs.Nodes {
		if c.Unknown {
			n++
		}
	}
	return n
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
	// HealthUnreachable means the collection itself failed.
	HealthUnreachable NodeHealth = "unreachable"
	// HealthHealthy means the gateway answers and raft has a settled role.
	HealthHealthy NodeHealth = "healthy"
	// HealthDegraded means the node answered but is not fully serving.
	HealthDegraded NodeHealth = "degraded"
	// HealthUnknown means the node runs a release without telemetry.
	HealthUnknown NodeHealth = "unknown"
)

// Health classifies one node's collection result.
func (c CollectionStatus) Health() NodeHealth {
	if c.Unknown {
		return HealthUnknown
	}
	if c.Err != "" || c.Report == nil {
		return HealthUnreachable
	}
	gatewayUp := c.Report.Gateway != nil && c.Report.Gateway.Responsive
	raftSettled := c.Report.RQLite.Settled()
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
	case HealthUnknown, HealthUnreachable:
		if c.Err != "" {
			return c.Err
		}
		return "no report returned"
	}

	var missing []string
	if c.Report.Gateway == nil || !c.Report.Gateway.Responsive {
		missing = append(missing, "gateway down")
	}
	if c.Report.RQLite == nil {
		missing = append(missing, "no rqlite report")
	} else if !c.Report.RQLite.Settled() {
		state := c.Report.RQLite.RaftState
		if state == "" {
			state = "unknown"
		}
		missing = append(missing, "raft "+state)
	}
	return strings.Join(missing, ", ")
}

// IsNameserver reports whether the node runs one of the cluster's nameservers.
func (n NodeRef) IsNameserver() bool {
	return strings.HasPrefix(n.Role, "nameserver")
}

// Package monitor reads `orama monitor report --json`, the operator's view of
// the whole cluster, and decides whether the cluster has settled.
//
// The shapes are a copy of the fields tests need from the CLI's report
// (core/cmd/orama/internal/monitor/display's envelope, and the node report of
// core/pkg/telemetry/report). They are copied, not imported: the e2e module
// does not build core's telemetry dependencies. drift_test.go decodes a
// report the real code wrote (testdata/monitor-report.json) and fails when a
// field here no longer exists there.
//
// The predicates (Converged, LeaderAgreement, Forgotten, Serving) are the
// ones core/e2e/lifecycle uses, so a fleet test and a lifecycle scenario mean
// the same thing by "the cluster came back".
package monitor

import "time"

// Values `orama monitor report` writes.
const (
	// NoLeader is summary.rqlite_leader when no node is the leader.
	NoLeader = "none"
	// StatusOK is a node's status, and a summary field's value, when all is well.
	StatusOK = "ok"
	// RaftLeader and RaftFollower are the settled raft states.
	RaftLeader   = "Leader"
	RaftFollower = "Follower"
	// AlertCritical is the severity that keeps a cluster from converging.
	AlertCritical = "critical"
	// nameserverRolePrefix starts the role of a node that runs a nameserver.
	nameserverRolePrefix = "nameserver"
)

// MaxReportAgeSec is the oldest a node's report may be for the cluster to
// count as converged: the age after which the gateway itself stops counting a
// report (pkg/gateway/telemetry.go telemetryStaleAfter: three missed 10s
// collections plus the 60s collection timeout). A node under load takes a
// while to collect, so its report's age legitimately swings between 0 and the
// collection time plus the 10s interval; a tighter bound failed a healthy
// node whose collection took 25s.
const MaxReportAgeSec = 90

// Report is the whole `orama monitor report --json` document.
type Report struct {
	Meta    Meta    `json:"meta"`
	Summary Summary `json:"summary"`
	Alerts  []Alert `json:"alerts"`
	Nodes   []Node  `json:"nodes"`
}

// Meta says what the report covers.
type Meta struct {
	Environment  string    `json:"environment"`
	CollectedAt  time.Time `json:"collected_at"`
	DurationSec  float64   `json:"duration_seconds"`
	NodeCount    int       `json:"node_count"`
	HealthyCount int       `json:"healthy_count"`
	FailedCount  int       `json:"failed_count"`
}

// Summary is the cluster-wide verdict.
type Summary struct {
	RQLiteLeader   string `json:"rqlite_leader"`
	RQLiteQuorum   string `json:"rqlite_quorum"`
	WGMeshStatus   string `json:"wg_mesh_status"`
	ServiceHealth  string `json:"service_health"`
	CriticalAlerts int    `json:"critical_alerts"`
	WarningAlerts  int    `json:"warning_alerts"`
}

// Alert is one detected issue.
type Alert struct {
	Severity  string `json:"severity"`
	Subsystem string `json:"subsystem"`
	Node      string `json:"node"`
	Message   string `json:"message"`
}

// Node is one node's entry. Report is nil when it could not be collected;
// Error says why.
type Node struct {
	Host         string      `json:"host"`
	Role         string      `json:"role"`
	Status       string      `json:"status"`
	Report       *NodeReport `json:"report,omitempty"`
	Error        string      `json:"error,omitempty"`
	ReportAgeSec int         `json:"report_age_sec"`
}

// NodeReport is the subset of `orama node report --json` tests read.
type NodeReport struct {
	Timestamp time.Time `json:"timestamp"`
	Hostname  string    `json:"hostname"`
	PublicIP  string    `json:"public_ip,omitempty"`
	WGIP      string    `json:"wireguard_ip,omitempty"`
	Version   string    `json:"version"`

	System    *System    `json:"system"`
	Services  *Services  `json:"services"`
	RQLite    *RQLite    `json:"rqlite,omitempty"`
	Gateway   *Gateway   `json:"gateway,omitempty"`
	WireGuard *WireGuard `json:"wireguard,omitempty"`
	DNS       *DNS       `json:"dns,omitempty"`
	Network   *Network   `json:"network"`
	Chain     *Chain     `json:"chain,omitempty"`
}

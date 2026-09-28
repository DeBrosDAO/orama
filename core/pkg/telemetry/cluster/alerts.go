package cluster

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// AlertSeverity represents the severity of an alert.
type AlertSeverity string

const (
	AlertCritical AlertSeverity = "critical"
	AlertWarning  AlertSeverity = "warning"
	AlertInfo     AlertSeverity = "info"
)

// SubsystemCollection is the subsystem of an alert raised because a node's
// report could not be collected at all.
const SubsystemCollection = "collection"

// Alert represents a detected issue.
type Alert struct {
	Severity  AlertSeverity `json:"severity"`
	Subsystem string        `json:"subsystem"`
	Node      string        `json:"node"`
	Message   string        `json:"message"`
}

// joiningGraceSec is the grace period (in seconds) after a node starts during
// which unreachability alerts from other nodes are downgraded to info.
const joiningGraceSec = 300

// nodeContext carries per-node metadata needed for context-aware alerting.
type nodeContext struct {
	host         string
	role         string // "node", "nameserver-ns1", etc.
	isNameserver bool
	isJoining    bool // orama-node active_since_sec < joiningGraceSec
	uptimeSec    int  // orama-node active_since_sec
}

// buildNodeContexts builds a map of WG IP -> nodeContext for all healthy nodes.
func buildNodeContexts(snap *ClusterSnapshot) map[string]*nodeContext {
	ctxMap := make(map[string]*nodeContext)
	for _, cs := range snap.Nodes {
		if cs.Report == nil {
			continue
		}
		r := cs.Report
		host := nodeHost(r)

		nc := &nodeContext{
			host:         host,
			role:         cs.Node.Role,
			isNameserver: strings.HasPrefix(cs.Node.Role, "nameserver"),
		}

		// Determine uptime from orama-node service
		if r.Services != nil {
			for _, svc := range r.Services.Services {
				if svc.Name == "orama-node" && svc.ActiveState == "active" {
					nc.uptimeSec = int(svc.ActiveSinceSec)
					nc.isJoining = svc.ActiveSinceSec < joiningGraceSec
					break
				}
			}
		}

		ctxMap[host] = nc
		// Also index by WG IP for cross-node RQLite unreachability lookups
		if r.WireGuard != nil && r.WireGuard.WgIP != "" {
			ctxMap[r.WireGuard.WgIP] = nc
		}
	}
	return ctxMap
}

// DeriveAlerts scans a ClusterSnapshot and produces alerts.
func DeriveAlerts(snap *ClusterSnapshot) []Alert {
	var alerts []Alert

	// Collection failures
	for _, cs := range snap.Nodes {
		if cs.Unknown {
			alerts = append(alerts, Alert{
				Severity:  AlertInfo,
				Subsystem: SubsystemCollection,
				Node:      cs.Node.Host,
				Message:   "Runs a release without telemetry; its health is not known until it is upgraded",
			})
			continue
		}
		if cs.Err != "" {
			alerts = append(alerts, Alert{
				Severity:  AlertCritical,
				Subsystem: SubsystemCollection,
				Node:      cs.Node.Host,
				Message:   fmt.Sprintf("Collection failed: %s", cs.Err),
			})
		}
	}

	reports := snap.Healthy()
	if len(reports) == 0 {
		return alerts
	}

	// Build context map for role/uptime-aware alerting
	nodeCtxMap := buildNodeContexts(snap)

	// Cross-node checks
	alerts = append(alerts, checkRQLiteLeader(reports)...)
	alerts = append(alerts, checkRQLiteQuorum(reports)...)
	alerts = append(alerts, checkRaftTermConsistency(reports)...)
	alerts = append(alerts, checkAppliedIndexLag(reports)...)
	alerts = append(alerts, checkWGPeerSymmetry(reports)...)
	alerts = append(alerts, checkClockSkew(snap)...)
	alerts = append(alerts, checkBinaryVersion(reports)...)
	alerts = append(alerts, checkOlricMemberConsistency(reports)...)
	alerts = append(alerts, checkIPFSSwarmConsistency(reports)...)
	alerts = append(alerts, checkIPFSClusterConsistency(reports)...)

	// Per-node checks
	for _, r := range reports {
		host := nodeHost(r)
		nc := nodeCtxMap[host]
		alerts = append(alerts, checkNodeRQLite(r, host, nodeCtxMap)...)
		alerts = append(alerts, checkNodeWireGuard(r, host)...)
		alerts = append(alerts, checkNodeSystem(r, host)...)
		alerts = append(alerts, checkNodeServices(r, host, nc)...)
		alerts = append(alerts, checkNodeDNS(r, host, nc)...)
		alerts = append(alerts, checkNodeTor(r, host)...)
		alerts = append(alerts, checkNodeProcesses(r, host)...)
		alerts = append(alerts, checkNodeNamespaces(r, host)...)
		alerts = append(alerts, checkNodeNetwork(r, host)...)
		alerts = append(alerts, checkNodeOlric(r, host)...)
		alerts = append(alerts, checkNodeIPFS(r, host)...)
		alerts = append(alerts, checkNodeVault(r, host)...)
		alerts = append(alerts, checkNodeGateway(r, host)...)
	}

	alerts = append(alerts, checkGlobalHealth(reports)...)

	return alerts
}

func nodeHost(r *report.NodeReport) string {
	if r.PublicIP != "" {
		return r.PublicIP
	}
	return r.Hostname
}

// ---------------------------------------------------------------------------
// Cross-node checks
// ---------------------------------------------------------------------------

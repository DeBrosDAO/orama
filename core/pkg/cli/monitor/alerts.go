package monitor

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/production/report"
)

// AlertSeverity represents the severity of an alert.
type AlertSeverity string

const (
	AlertCritical AlertSeverity = "critical"
	AlertWarning  AlertSeverity = "warning"
	AlertInfo     AlertSeverity = "info"
)

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
		if cs.Error != nil {
			alerts = append(alerts, Alert{
				Severity:  AlertCritical,
				Subsystem: "ssh",
				Node:      cs.Node.Host,
				Message:   fmt.Sprintf("Collection failed: %v", cs.Error),
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
	alerts = append(alerts, checkClockSkew(reports)...)
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
		alerts = append(alerts, checkNodeAnyone(r, host)...)
		alerts = append(alerts, checkNodeProcesses(r, host)...)
		alerts = append(alerts, checkNodeNamespaces(r, host)...)
		alerts = append(alerts, checkNodeNetwork(r, host)...)
		alerts = append(alerts, checkNodeOlric(r, host)...)
		alerts = append(alerts, checkNodeIPFS(r, host)...)
		alerts = append(alerts, checkNodeVault(r, host)...)
		alerts = append(alerts, checkNodeGateway(r, host)...)
	}

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

func checkRQLiteLeader(reports []*report.NodeReport) []Alert {
	var alerts []Alert
	leaders := 0
	leaderAddrs := map[string]bool{}
	for _, r := range reports {
		if r.RQLite != nil && r.RQLite.RaftState == "Leader" {
			leaders++
		}
		if r.RQLite != nil && r.RQLite.LeaderAddr != "" {
			leaderAddrs[r.RQLite.LeaderAddr] = true
		}
	}

	if leaders == 0 {
		alerts = append(alerts, Alert{AlertCritical, "rqlite", "cluster", "No RQLite leader found"})
	} else if leaders > 1 {
		alerts = append(alerts, Alert{AlertCritical, "rqlite", "cluster",
			fmt.Sprintf("Split brain: %d leaders detected", leaders)})
	}

	if len(leaderAddrs) > 1 {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", "cluster",
			fmt.Sprintf("Leader disagreement: nodes report %d different leader addresses", len(leaderAddrs))})
	}

	return alerts
}

func checkRQLiteQuorum(reports []*report.NodeReport) []Alert {
	var voters, responsive int
	for _, r := range reports {
		if r.RQLite == nil {
			continue
		}
		if r.RQLite.Responsive {
			responsive++
			if r.RQLite.Voter {
				voters++
			}
		}
	}

	if responsive == 0 {
		return nil // no rqlite data at all
	}

	// Total voters = responsive voters + unresponsive nodes that should be voters.
	// For quorum calculation, use the total voter count (responsive + unreachable).
	totalVoters := voters
	for _, r := range reports {
		if r.RQLite != nil && !r.RQLite.Responsive {
			// Assume unresponsive nodes were voters (conservative estimate).
			totalVoters++
		}
	}

	if totalVoters < 2 {
		return nil // single-node cluster, no quorum concept
	}

	quorum := totalVoters/2 + 1
	if voters < quorum {
		return []Alert{{AlertCritical, "rqlite", "cluster",
			fmt.Sprintf("Quorum lost: only %d/%d voters reachable (need %d)", voters, totalVoters, quorum)}}
	}
	if voters == quorum {
		return []Alert{{AlertWarning, "rqlite", "cluster",
			fmt.Sprintf("Quorum fragile: exactly %d/%d voters reachable (one more failure = quorum loss)", voters, totalVoters)}}
	}

	return nil
}

func checkRaftTermConsistency(reports []*report.NodeReport) []Alert {
	var minTerm, maxTerm uint64
	first := true
	for _, r := range reports {
		if r.RQLite == nil || !r.RQLite.Responsive {
			continue
		}
		if first {
			minTerm = r.RQLite.Term
			maxTerm = r.RQLite.Term
			first = false
		}
		if r.RQLite.Term < minTerm {
			minTerm = r.RQLite.Term
		}
		if r.RQLite.Term > maxTerm {
			maxTerm = r.RQLite.Term
		}
	}
	if maxTerm-minTerm > 1 {
		return []Alert{{AlertWarning, "rqlite", "cluster",
			fmt.Sprintf("Raft term inconsistency: min=%d, max=%d (delta=%d)", minTerm, maxTerm, maxTerm-minTerm)}}
	}
	return nil
}

func checkAppliedIndexLag(reports []*report.NodeReport) []Alert {
	var maxApplied uint64
	for _, r := range reports {
		if r.RQLite != nil && r.RQLite.Applied > maxApplied {
			maxApplied = r.RQLite.Applied
		}
	}

	var alerts []Alert
	for _, r := range reports {
		if r.RQLite == nil || !r.RQLite.Responsive {
			continue
		}
		lag := maxApplied - r.RQLite.Applied
		if lag > 100 {
			alerts = append(alerts, Alert{AlertWarning, "rqlite", nodeHost(r),
				fmt.Sprintf("Applied index lag: %d behind leader (local=%d, max=%d)", lag, r.RQLite.Applied, maxApplied)})
		}
	}
	return alerts
}

func checkWGPeerSymmetry(reports []*report.NodeReport) []Alert {
	type nodeInfo struct {
		host     string
		peerKeys map[string]bool
	}
	var nodes []nodeInfo
	for _, r := range reports {
		if r.WireGuard == nil || !r.WireGuard.InterfaceUp {
			continue
		}
		ni := nodeInfo{host: nodeHost(r), peerKeys: map[string]bool{}}
		for _, p := range r.WireGuard.Peers {
			ni.peerKeys[p.PublicKey] = true
		}
		nodes = append(nodes, ni)
	}

	var alerts []Alert
	expectedPeers := len(nodes) - 1
	for _, ni := range nodes {
		if len(ni.peerKeys) < expectedPeers {
			alerts = append(alerts, Alert{AlertCritical, "wireguard", ni.host,
				fmt.Sprintf("WG peer count mismatch: has %d peers, expected %d", len(ni.peerKeys), expectedPeers)})
		}
	}

	return alerts
}

func checkClockSkew(reports []*report.NodeReport) []Alert {
	var times []struct {
		host string
		t    int64
	}
	for _, r := range reports {
		if r.System != nil && r.System.TimeUnix > 0 {
			times = append(times, struct {
				host string
				t    int64
			}{nodeHost(r), r.System.TimeUnix})
		}
	}
	if len(times) < 2 {
		return nil
	}

	var minT, maxT int64 = times[0].t, times[0].t
	var minHost, maxHost string = times[0].host, times[0].host
	for _, t := range times[1:] {
		if t.t < minT {
			minT = t.t
			minHost = t.host
		}
		if t.t > maxT {
			maxT = t.t
			maxHost = t.host
		}
	}

	delta := maxT - minT
	if delta > 5 {
		return []Alert{{AlertWarning, "system", "cluster",
			fmt.Sprintf("Clock skew: %ds between %s and %s", delta, minHost, maxHost)}}
	}
	return nil
}

func checkBinaryVersion(reports []*report.NodeReport) []Alert {
	versions := map[string][]string{} // version -> list of hosts
	for _, r := range reports {
		v := r.Version
		if v == "" {
			v = "unknown"
		}
		versions[v] = append(versions[v], nodeHost(r))
	}
	if len(versions) > 1 {
		msg := "Binary version mismatch:"
		for v, hosts := range versions {
			msg += fmt.Sprintf(" %s=%v", v, hosts)
		}
		return []Alert{{AlertWarning, "system", "cluster", msg}}
	}
	return nil
}

func checkOlricMemberConsistency(reports []*report.NodeReport) []Alert {
	// Count nodes where Olric is active to determine expected member count.
	activeCount := 0
	for _, r := range reports {
		if r.Olric != nil && r.Olric.ServiceActive {
			activeCount++
		}
	}
	if activeCount < 2 {
		return nil
	}

	var alerts []Alert
	for _, r := range reports {
		if r.Olric == nil || !r.Olric.ServiceActive || r.Olric.MemberCount == 0 {
			continue
		}
		if r.Olric.MemberCount < activeCount {
			alerts = append(alerts, Alert{AlertWarning, "olric", nodeHost(r),
				fmt.Sprintf("Olric member count: %d (expected %d active nodes)", r.Olric.MemberCount, activeCount)})
		}
	}
	return alerts
}

func checkIPFSSwarmConsistency(reports []*report.NodeReport) []Alert {
	// Count IPFS-active nodes to determine expected peer count.
	activeCount := 0
	for _, r := range reports {
		if r.IPFS != nil && r.IPFS.DaemonActive {
			activeCount++
		}
	}
	if activeCount < 2 {
		return nil
	}

	expectedPeers := activeCount - 1
	var alerts []Alert
	for _, r := range reports {
		if r.IPFS == nil || !r.IPFS.DaemonActive {
			continue
		}
		if r.IPFS.SwarmPeerCount == 0 {
			alerts = append(alerts, Alert{AlertCritical, "ipfs", nodeHost(r),
				"IPFS node isolated: 0 swarm peers"})
		} else if r.IPFS.SwarmPeerCount < expectedPeers {
			alerts = append(alerts, Alert{AlertWarning, "ipfs", nodeHost(r),
				fmt.Sprintf("IPFS swarm peers: %d (expected %d)", r.IPFS.SwarmPeerCount, expectedPeers)})
		}
	}
	return alerts
}

func checkIPFSClusterConsistency(reports []*report.NodeReport) []Alert {
	activeCount := 0
	for _, r := range reports {
		if r.IPFS != nil && r.IPFS.ClusterActive {
			activeCount++
		}
	}
	if activeCount < 2 {
		return nil
	}

	var alerts []Alert
	for _, r := range reports {
		if r.IPFS == nil || !r.IPFS.ClusterActive {
			continue
		}
		if r.IPFS.ClusterPeerCount < activeCount {
			alerts = append(alerts, Alert{AlertWarning, "ipfs", nodeHost(r),
				fmt.Sprintf("IPFS cluster peers: %d (expected %d)", r.IPFS.ClusterPeerCount, activeCount)})
		}
	}
	return alerts
}

// ---------------------------------------------------------------------------
// Per-node checks
// ---------------------------------------------------------------------------

func checkNodeRQLite(r *report.NodeReport, host string, nodeCtxMap map[string]*nodeContext) []Alert {
	if r.RQLite == nil {
		return nil
	}
	var alerts []Alert

	if !r.RQLite.Responsive {
		alerts = append(alerts, Alert{AlertCritical, "rqlite", host, "RQLite not responding"})
		return alerts // no point checking further
	}

	if !r.RQLite.Ready {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", host, "RQLite not ready (/readyz failed)"})
	}
	if !r.RQLite.StrongRead {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", host, "Strong read failed"})
	}

	// Raft state anomalies
	if r.RQLite.RaftState == "Candidate" {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", host, "RQLite in election (Candidate state)"})
	}
	if r.RQLite.RaftState == "Shutdown" {
		alerts = append(alerts, Alert{AlertCritical, "rqlite", host, "RQLite in Shutdown state"})
	}

	// FSM backlog
	if r.RQLite.FsmPending > 10 {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", host,
			fmt.Sprintf("RQLite FSM backlog: %d entries pending", r.RQLite.FsmPending)})
	}

	// Commit-applied gap (per-node, distinct from cross-node applied index lag)
	if r.RQLite.Commit > 0 && r.RQLite.Applied > 0 && r.RQLite.Commit > r.RQLite.Applied {
		gap := r.RQLite.Commit - r.RQLite.Applied
		if gap > 100 {
			alerts = append(alerts, Alert{AlertWarning, "rqlite", host,
				fmt.Sprintf("RQLite commit-applied gap: %d (commit=%d, applied=%d)", gap, r.RQLite.Commit, r.RQLite.Applied)})
		}
	}

	// Resource pressure
	if r.RQLite.Goroutines > 1000 {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", host,
			fmt.Sprintf("RQLite goroutine count high: %d", r.RQLite.Goroutines)})
	}
	if r.RQLite.HeapMB > 1000 {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", host,
			fmt.Sprintf("RQLite heap memory high: %dMB", r.RQLite.HeapMB)})
	}

	// Cluster partition detection: check if this node reports other nodes as unreachable.
	// If the unreachable node recently joined (< 5 min), downgrade to info — probes
	// may not have succeeded yet and this is expected transient behavior.
	for nodeAddr, info := range r.RQLite.Nodes {
		if !info.Reachable {
			// nodeAddr is like "10.0.0.4:7001" — extract the IP to look up context
			targetIP := strings.Split(nodeAddr, ":")[0]
			if targetCtx, ok := nodeCtxMap[targetIP]; ok && targetCtx.isJoining {
				alerts = append(alerts, Alert{AlertInfo, "rqlite", host,
					fmt.Sprintf("Node %s recently joined (%ds ago), probe pending for %s",
						targetCtx.host, targetCtx.uptimeSec, nodeAddr)})
			} else {
				alerts = append(alerts, Alert{AlertCritical, "rqlite", host,
					fmt.Sprintf("RQLite reports node %s unreachable (cluster partition)", nodeAddr)})
			}
		}
	}

	// Debug vars
	if dv := r.RQLite.DebugVars; dv != nil {
		if dv.LeaderNotFound > 0 {
			alerts = append(alerts, Alert{AlertWarning, "rqlite", host,
				fmt.Sprintf("RQLite leader_not_found errors: %d", dv.LeaderNotFound)})
		}
		if dv.SnapshotErrors > 0 {
			alerts = append(alerts, Alert{AlertWarning, "rqlite", host,
				fmt.Sprintf("RQLite snapshot errors: %d", dv.SnapshotErrors)})
		}
		totalQueryErrors := dv.QueryErrors + dv.ExecuteErrors
		if totalQueryErrors > 0 {
			alerts = append(alerts, Alert{AlertInfo, "rqlite", host,
				fmt.Sprintf("RQLite query/execute errors: %d", totalQueryErrors)})
		}
	}

	return alerts
}

func checkNodeWireGuard(r *report.NodeReport, host string) []Alert {
	if r.WireGuard == nil {
		return nil
	}
	var alerts []Alert
	if !r.WireGuard.InterfaceUp {
		alerts = append(alerts, Alert{AlertCritical, "wireguard", host, "WireGuard interface down"})
		return alerts
	}
	for _, p := range r.WireGuard.Peers {
		if p.HandshakeAgeSec > 180 && p.LatestHandshake > 0 {
			alerts = append(alerts, Alert{AlertWarning, "wireguard", host,
				fmt.Sprintf("Stale WG handshake with peer %s: %ds ago", truncateKey(p.PublicKey), p.HandshakeAgeSec)})
		}
		if p.LatestHandshake == 0 {
			alerts = append(alerts, Alert{AlertCritical, "wireguard", host,
				fmt.Sprintf("WG peer %s has never handshaked", truncateKey(p.PublicKey))})
		}
	}
	return alerts
}

func checkNodeSystem(r *report.NodeReport, host string) []Alert {
	if r.System == nil {
		return nil
	}
	var alerts []Alert
	if r.System.MemUsePct > 90 {
		alerts = append(alerts, Alert{AlertWarning, "system", host,
			fmt.Sprintf("Memory at %d%%", r.System.MemUsePct)})
	}
	if r.System.DiskUsePct > 85 {
		alerts = append(alerts, Alert{AlertWarning, "system", host,
			fmt.Sprintf("Disk at %d%%", r.System.DiskUsePct)})
	}
	if r.System.OOMKills > 0 {
		alerts = append(alerts, Alert{AlertCritical, "system", host,
			fmt.Sprintf("%d OOM kills detected", r.System.OOMKills)})
	}
	if r.System.SwapUsedMB > 0 && r.System.SwapTotalMB > 0 {
		pct := r.System.SwapUsedMB * 100 / r.System.SwapTotalMB
		if pct > 30 {
			alerts = append(alerts, Alert{AlertInfo, "system", host,
				fmt.Sprintf("Swap usage at %d%%", pct)})
		}
	}
	// High load
	if r.System.CPUCount > 0 {
		loadRatio := r.System.LoadAvg1 / float64(r.System.CPUCount)
		if loadRatio > 2.0 {
			alerts = append(alerts, Alert{AlertWarning, "system", host,
				fmt.Sprintf("High load: %.1f (%.1fx CPU count)", r.System.LoadAvg1, loadRatio)})
		}
	}
	// Inode exhaustion
	if r.System.InodePct > 95 {
		alerts = append(alerts, Alert{AlertCritical, "system", host,
			fmt.Sprintf("Inode exhaustion imminent: %d%%", r.System.InodePct)})
	} else if r.System.InodePct > 90 {
		alerts = append(alerts, Alert{AlertWarning, "system", host,
			fmt.Sprintf("Inode usage at %d%%", r.System.InodePct)})
	}
	return alerts
}

func checkNodeServices(r *report.NodeReport, host string, nc *nodeContext) []Alert {
	if r.Services == nil {
		return nil
	}
	var alerts []Alert
	for _, svc := range r.Services.Services {
		// Skip services that are expected to be inactive based on node role/mode
		if shouldSkipServiceAlert(svc.Name, svc.ActiveState, r, nc) {
			continue
		}

		if svc.ActiveState == "failed" {
			alerts = append(alerts, Alert{AlertCritical, "service", host,
				fmt.Sprintf("Service %s is FAILED", svc.Name)})
		} else if svc.ActiveState != "active" && svc.ActiveState != "" && svc.ActiveState != "unknown" {
			alerts = append(alerts, Alert{AlertWarning, "service", host,
				fmt.Sprintf("Service %s is %s", svc.Name, svc.ActiveState)})
		}
		if svc.RestartLoopRisk {
			alerts = append(alerts, Alert{AlertCritical, "service", host,
				fmt.Sprintf("Service %s restart loop: %d restarts, active for %ds", svc.Name, svc.NRestarts, svc.ActiveSinceSec)})
		}
	}
	for _, unit := range r.Services.FailedUnits {
		alerts = append(alerts, Alert{AlertWarning, "service", host,
			fmt.Sprintf("Failed systemd unit: %s", unit)})
	}
	return alerts
}

// shouldSkipServiceAlert returns true if this service being inactive is expected
// given the node's role and anyone mode.
func shouldSkipServiceAlert(svcName, state string, r *report.NodeReport, nc *nodeContext) bool {
	if state == "active" || state == "failed" {
		return false // always report active (no alert) and failed (always alert)
	}

	// CoreDNS: only expected on nameserver nodes
	if svcName == "coredns" && (nc == nil || !nc.isNameserver) {
		return true
	}

	// Anyone services: only alert for the mode the node is configured for
	if r.Anyone != nil {
		mode := r.Anyone.Mode
		if svcName == "orama-anyone-client" && mode == "relay" {
			return true // relay node doesn't run client
		}
		if svcName == "orama-anyone-relay" && mode == "client" {
			return true // client node doesn't run relay
		}
	}
	// If anyone section is nil (no anyone configured), skip both anyone services
	if r.Anyone == nil && (svcName == "orama-anyone-client" || svcName == "orama-anyone-relay") {
		return true
	}

	return false
}

func checkNodeDNS(r *report.NodeReport, host string, nc *nodeContext) []Alert {
	if r.DNS == nil {
		return nil
	}

	isNameserver := nc != nil && nc.isNameserver

	var alerts []Alert

	// CoreDNS: only check on nameserver nodes
	if isNameserver && !r.DNS.CoreDNSActive {
		alerts = append(alerts, Alert{AlertCritical, "dns", host, "CoreDNS is down"})
	}

	// Caddy: check on all nodes (any node can host namespaces)
	if !r.DNS.CaddyActive {
		alerts = append(alerts, Alert{AlertCritical, "dns", host, "Caddy is down"})
	}

	// TLS cert expiry: only meaningful on nameserver nodes that have public domains
	if isNameserver {
		if r.DNS.BaseTLSDaysLeft >= 0 && r.DNS.BaseTLSDaysLeft < 14 {
			alerts = append(alerts, Alert{AlertWarning, "dns", host,
				fmt.Sprintf("Base TLS cert expires in %d days", r.DNS.BaseTLSDaysLeft)})
		}
		if r.DNS.WildTLSDaysLeft >= 0 && r.DNS.WildTLSDaysLeft < 14 {
			alerts = append(alerts, Alert{AlertWarning, "dns", host,
				fmt.Sprintf("Wildcard TLS cert expires in %d days", r.DNS.WildTLSDaysLeft)})
		}
	}

	// DNS resolution checks: only on nameserver nodes with CoreDNS running
	if isNameserver && r.DNS.CoreDNSActive {
		if !r.DNS.SOAResolves {
			alerts = append(alerts, Alert{AlertWarning, "dns", host, "SOA record not resolving"})
		}
		if !r.DNS.WildcardResolves {
			alerts = append(alerts, Alert{AlertWarning, "dns", host, "Wildcard DNS not resolving"})
		}
		if !r.DNS.BaseAResolves {
			alerts = append(alerts, Alert{AlertWarning, "dns", host, "Base domain A record not resolving"})
		}
		if !r.DNS.NSResolves {
			alerts = append(alerts, Alert{AlertWarning, "dns", host, "NS records not resolving"})
		}
		if !r.DNS.Port53Bound {
			alerts = append(alerts, Alert{AlertCritical, "dns", host, "CoreDNS active but port 53 not bound"})
		}
	}

	if r.DNS.CaddyActive && !r.DNS.Port443Bound {
		alerts = append(alerts, Alert{AlertCritical, "dns", host, "Caddy active but port 443 not bound"})
	}
	return alerts
}

func checkNodeAnyone(r *report.NodeReport, host string) []Alert {
	if r.Anyone == nil {
		return nil
	}
	var alerts []Alert
	if (r.Anyone.RelayActive || r.Anyone.ClientActive) && !r.Anyone.Bootstrapped {
		alerts = append(alerts, Alert{AlertWarning, "anyone", host,
			fmt.Sprintf("Anyone bootstrap at %d%%", r.Anyone.BootstrapPct)})
	}
	return alerts
}

func checkNodeProcesses(r *report.NodeReport, host string) []Alert {
	if r.Processes == nil {
		return nil
	}
	var alerts []Alert
	if r.Processes.ZombieCount > 0 {
		alerts = append(alerts, Alert{AlertInfo, "system", host,
			fmt.Sprintf("%d zombie processes", r.Processes.ZombieCount)})
	}
	if r.Processes.OrphanCount > 0 {
		alerts = append(alerts, Alert{AlertInfo, "system", host,
			fmt.Sprintf("%d orphan orama processes", r.Processes.OrphanCount)})
	}
	if r.Processes.PanicCount > 0 {
		alerts = append(alerts, Alert{AlertCritical, "system", host,
			fmt.Sprintf("%d panic/fatal in orama-node logs (1h)", r.Processes.PanicCount)})
	}
	return alerts
}

func checkNodeNamespaces(r *report.NodeReport, host string) []Alert {
	var alerts []Alert
	for _, ns := range r.Namespaces {
		if !ns.GatewayUp {
			alerts = append(alerts, Alert{AlertWarning, "namespace", host,
				fmt.Sprintf("Namespace %s gateway down", ns.Name)})
		}
		if !ns.RQLiteUp {
			alerts = append(alerts, Alert{AlertWarning, "namespace", host,
				fmt.Sprintf("Namespace %s RQLite down", ns.Name)})
		}
	}
	return alerts
}

func checkNodeNetwork(r *report.NodeReport, host string) []Alert {
	if r.Network == nil {
		return nil
	}
	var alerts []Alert
	if !r.Network.UFWActive {
		alerts = append(alerts, Alert{AlertCritical, "network", host, "UFW firewall is inactive"})
	}
	if !r.Network.InternetReachable {
		alerts = append(alerts, Alert{AlertWarning, "network", host, "Internet not reachable (ping 8.8.8.8 failed)"})
	}
	if r.Network.TCPRetransRate > 5.0 {
		alerts = append(alerts, Alert{AlertWarning, "network", host,
			fmt.Sprintf("High TCP retransmission rate: %.1f%%", r.Network.TCPRetransRate)})
	}

	// Check for internal ports exposed in UFW rules.
	// Ports 5001 (RQLite), 6001 (Gateway), 3320 (Olric), 4501 (IPFS API) should be internal only.
	internalPorts := []string{"5001", "6001", "3320", "4501"}
	for _, rule := range r.Network.UFWRules {
		ruleLower := strings.ToLower(rule)
		// Only flag ALLOW rules (not deny/reject).
		if !strings.Contains(ruleLower, "allow") {
			continue
		}
		for _, port := range internalPorts {
			// Match rules like "5001 ALLOW Anywhere" or "5001/tcp ALLOW IN"
			// but not rules restricted to 10.0.0.0/24 (WG subnet).
			if strings.Contains(rule, port) && !strings.Contains(rule, "10.0.0.") {
				alerts = append(alerts, Alert{AlertCritical, "network", host,
					fmt.Sprintf("Internal port %s exposed in UFW: %s", port, strings.TrimSpace(rule))})
			}
		}
	}

	return alerts
}

func checkNodeOlric(r *report.NodeReport, host string) []Alert {
	if r.Olric == nil {
		return nil
	}
	var alerts []Alert

	if !r.Olric.ServiceActive {
		alerts = append(alerts, Alert{AlertCritical, "olric", host, "Olric service down"})
		return alerts
	}
	if !r.Olric.MemberlistUp {
		alerts = append(alerts, Alert{AlertCritical, "olric", host, "Olric memberlist port down"})
	}
	if r.Olric.LogSuspects > 0 {
		alerts = append(alerts, Alert{AlertWarning, "olric", host,
			fmt.Sprintf("Olric member suspects: %d in last hour", r.Olric.LogSuspects)})
	}
	if r.Olric.LogFlapping > 5 {
		alerts = append(alerts, Alert{AlertWarning, "olric", host,
			fmt.Sprintf("Olric members flapping: %d join/leave events in last hour", r.Olric.LogFlapping)})
	}
	if r.Olric.LogErrors > 20 {
		alerts = append(alerts, Alert{AlertWarning, "olric", host,
			fmt.Sprintf("High Olric error rate: %d errors in last hour", r.Olric.LogErrors)})
	}
	if r.Olric.RestartCount > 3 {
		alerts = append(alerts, Alert{AlertWarning, "olric", host,
			fmt.Sprintf("Olric excessive restarts: %d", r.Olric.RestartCount)})
	}
	if r.Olric.ProcessMemMB > 500 {
		alerts = append(alerts, Alert{AlertWarning, "olric", host,
			fmt.Sprintf("Olric high memory: %dMB", r.Olric.ProcessMemMB)})
	}

	return alerts
}

func checkNodeIPFS(r *report.NodeReport, host string) []Alert {
	if r.IPFS == nil {
		return nil
	}
	var alerts []Alert

	if !r.IPFS.DaemonActive {
		alerts = append(alerts, Alert{AlertCritical, "ipfs", host, "IPFS daemon down"})
	}
	if !r.IPFS.ClusterActive {
		alerts = append(alerts, Alert{AlertCritical, "ipfs", host, "IPFS cluster down"})
	}

	// Only check these if daemon is running (otherwise data is meaningless).
	if r.IPFS.DaemonActive {
		if r.IPFS.SwarmPeerCount == 0 {
			alerts = append(alerts, Alert{AlertCritical, "ipfs", host, "IPFS isolated: no swarm peers"})
		}
		if !r.IPFS.HasSwarmKey {
			alerts = append(alerts, Alert{AlertCritical, "ipfs", host,
				"IPFS swarm key missing (private network compromised)"})
		}
		if !r.IPFS.BootstrapEmpty {
			alerts = append(alerts, Alert{AlertWarning, "ipfs", host,
				"IPFS bootstrap list not empty (should be empty for private swarm)"})
		}
	}

	if r.IPFS.RepoUsePct > 95 {
		alerts = append(alerts, Alert{AlertCritical, "ipfs", host,
			fmt.Sprintf("IPFS repo nearly full: %d%%", r.IPFS.RepoUsePct)})
	} else if r.IPFS.RepoUsePct > 90 {
		alerts = append(alerts, Alert{AlertWarning, "ipfs", host,
			fmt.Sprintf("IPFS repo at %d%%", r.IPFS.RepoUsePct)})
	}

	if r.IPFS.ClusterErrors > 0 {
		alerts = append(alerts, Alert{AlertWarning, "ipfs", host,
			fmt.Sprintf("IPFS cluster peer errors: %d", r.IPFS.ClusterErrors)})
	}

	return alerts
}

func checkNodeVault(r *report.NodeReport, host string) []Alert {
	if r.Vault == nil {
		return nil
	}
	var alerts []Alert

	if !r.Vault.ServiceActive {
		alerts = append(alerts, Alert{AlertCritical, "vault", host, "Vault service not running"})
		return alerts
	}

	if !r.Vault.Responsive {
		alerts = append(alerts, Alert{AlertWarning, "vault", host, "Vault not responding to health queries"})
		return alerts
	}

	switch r.Vault.Status {
	case "unavailable":
		alerts = append(alerts, Alert{AlertCritical, "vault", host,
			fmt.Sprintf("Vault unavailable: %d/%d guardians healthy (need %d for reads)",
				r.Vault.Healthy, r.Vault.Guardians, r.Vault.Threshold)})
	case "degraded":
		alerts = append(alerts, Alert{AlertWarning, "vault", host,
			fmt.Sprintf("Vault degraded: %d/%d guardians healthy (need %d for writes)",
				r.Vault.Healthy, r.Vault.Guardians, r.Vault.WriteQuorum)})
	}

	if r.Vault.RestartCount > 3 {
		alerts = append(alerts, Alert{AlertWarning, "vault", host,
			fmt.Sprintf("Vault restarted %d times", r.Vault.RestartCount)})
	}

	return alerts
}

func checkNodeGateway(r *report.NodeReport, host string) []Alert {
	if r.Gateway == nil {
		return nil
	}
	var alerts []Alert

	if !r.Gateway.Responsive {
		alerts = append(alerts, Alert{AlertCritical, "gateway", host, "Gateway not responding"})
		return alerts
	}

	if r.Gateway.HTTPStatus != 200 {
		alerts = append(alerts, Alert{AlertWarning, "gateway", host,
			fmt.Sprintf("Gateway health check returned HTTP %d", r.Gateway.HTTPStatus)})
	}

	for name, sub := range r.Gateway.Subsystems {
		if sub.Status != "ok" && sub.Status != "" {
			msg := fmt.Sprintf("Gateway subsystem %s: status=%s", name, sub.Status)
			if sub.Error != "" {
				msg += fmt.Sprintf(" error=%s", sub.Error)
			}
			alerts = append(alerts, Alert{AlertWarning, "gateway", host, msg})
		}
	}

	return alerts
}

func truncateKey(key string) string {
	if len(key) > 8 {
		return key[:8] + "..."
	}
	return key
}

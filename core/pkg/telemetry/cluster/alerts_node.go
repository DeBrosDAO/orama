package cluster

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

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
	if r.RQLite.RaftState == report.RaftCandidate {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", host, "RQLite in election (Candidate state)"})
	}
	if r.RQLite.RaftState == report.RaftShutdown {
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
			// nodeAddr is like "10.0.0.4:10101" — extract the IP to look up context
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
		if shouldSkipServiceAlert(svc.Name, svc.ActiveState, nc) {
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
			detail := fmt.Sprintf("active for %ds", svc.ActiveSinceSec)
			if svc.ActiveSinceSec == 0 {
				detail = "never reached active"
			}
			alerts = append(alerts, Alert{AlertCritical, "service", host,
				fmt.Sprintf("Service %s restart loop: %d restarts, %s", svc.Name, svc.NRestarts, detail)})
		}
	}
	for _, unit := range r.Services.FailedUnits {
		alerts = append(alerts, Alert{AlertWarning, "service", host,
			fmt.Sprintf("Failed systemd unit: %s", unit)})
	}
	return alerts
}

// shouldSkipServiceAlert returns true if this service being inactive is expected
// given the node's role.
func shouldSkipServiceAlert(svcName, state string, nc *nodeContext) bool {
	if state == "active" || state == "failed" {
		return false // always report active (no alert) and failed (always alert)
	}

	// CoreDNS: only expected on nameserver nodes
	if svcName == "coredns" && (nc == nil || !nc.isNameserver) {
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

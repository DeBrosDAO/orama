package monitor

import (
	"fmt"

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

	// Cross-node: RQLite leader
	alerts = append(alerts, checkRQLiteLeader(reports)...)

	// Cross-node: Raft term consistency
	alerts = append(alerts, checkRaftTermConsistency(reports)...)

	// Cross-node: Applied index lag
	alerts = append(alerts, checkAppliedIndexLag(reports)...)

	// Cross-node: WireGuard peer symmetry
	alerts = append(alerts, checkWGPeerSymmetry(reports)...)

	// Cross-node: Clock skew
	alerts = append(alerts, checkClockSkew(reports)...)

	// Cross-node: Binary version
	alerts = append(alerts, checkBinaryVersion(reports)...)

	// Per-node checks
	for _, r := range reports {
		host := nodeHost(r)
		alerts = append(alerts, checkNodeRQLite(r, host)...)
		alerts = append(alerts, checkNodeWireGuard(r, host)...)
		alerts = append(alerts, checkNodeSystem(r, host)...)
		alerts = append(alerts, checkNodeServices(r, host)...)
		alerts = append(alerts, checkNodeDNS(r, host)...)
		alerts = append(alerts, checkNodeAnyone(r, host)...)
		alerts = append(alerts, checkNodeProcesses(r, host)...)
		alerts = append(alerts, checkNodeNamespaces(r, host)...)
		alerts = append(alerts, checkNodeNetwork(r, host)...)
	}

	return alerts
}

func nodeHost(r *report.NodeReport) string {
	if r.PublicIP != "" {
		return r.PublicIP
	}
	return r.Hostname
}

// --- Cross-node checks ---

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
			first = true
		}
		if r.RQLite.Term < minTerm {
			minTerm = r.RQLite.Term
		}
		if r.RQLite.Term > maxTerm {
			maxTerm = r.RQLite.Term
		}
		first = false
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
	// Build map: wg_ip -> set of peer public keys
	type nodeInfo struct {
		host     string
		wgIP     string
		peerKeys map[string]bool
	}
	var nodes []nodeInfo
	for _, r := range reports {
		if r.WireGuard == nil || !r.WireGuard.InterfaceUp {
			continue
		}
		ni := nodeInfo{host: nodeHost(r), wgIP: r.WireGuard.WgIP, peerKeys: map[string]bool{}}
		for _, p := range r.WireGuard.Peers {
			ni.peerKeys[p.PublicKey] = true
		}
		nodes = append(nodes, ni)
	}

	// For WG peer symmetry, we check peer counts match (N-1 peers expected)
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

// --- Per-node checks ---

func checkNodeRQLite(r *report.NodeReport, host string) []Alert {
	if r.RQLite == nil {
		return nil
	}
	var alerts []Alert
	if !r.RQLite.Responsive {
		alerts = append(alerts, Alert{AlertCritical, "rqlite", host, "RQLite not responding"})
	}
	if r.RQLite.Responsive && !r.RQLite.Ready {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", host, "RQLite not ready (/readyz failed)"})
	}
	if r.RQLite.Responsive && !r.RQLite.StrongRead {
		alerts = append(alerts, Alert{AlertWarning, "rqlite", host, "Strong read failed"})
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
	return alerts
}

func checkNodeServices(r *report.NodeReport, host string) []Alert {
	if r.Services == nil {
		return nil
	}
	var alerts []Alert
	for _, svc := range r.Services.Services {
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

func checkNodeDNS(r *report.NodeReport, host string) []Alert {
	if r.DNS == nil {
		return nil
	}
	var alerts []Alert
	if !r.DNS.CoreDNSActive {
		alerts = append(alerts, Alert{AlertCritical, "dns", host, "CoreDNS is down"})
	}
	if !r.DNS.CaddyActive {
		alerts = append(alerts, Alert{AlertCritical, "dns", host, "Caddy is down"})
	}
	if r.DNS.BaseTLSDaysLeft >= 0 && r.DNS.BaseTLSDaysLeft < 14 {
		alerts = append(alerts, Alert{AlertWarning, "dns", host,
			fmt.Sprintf("Base TLS cert expires in %d days", r.DNS.BaseTLSDaysLeft)})
	}
	if r.DNS.WildTLSDaysLeft >= 0 && r.DNS.WildTLSDaysLeft < 14 {
		alerts = append(alerts, Alert{AlertWarning, "dns", host,
			fmt.Sprintf("Wildcard TLS cert expires in %d days", r.DNS.WildTLSDaysLeft)})
	}
	if r.DNS.CoreDNSActive && !r.DNS.SOAResolves {
		alerts = append(alerts, Alert{AlertWarning, "dns", host, "SOA record not resolving"})
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
	return alerts
}

func truncateKey(key string) string {
	if len(key) > 8 {
		return key[:8] + "..."
	}
	return key
}


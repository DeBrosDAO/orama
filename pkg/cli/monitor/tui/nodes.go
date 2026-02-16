package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// renderNodes renders the Nodes tab with detailed per-node information.
func renderNodes(snap *monitor.ClusterSnapshot, width int) string {
	if snap == nil {
		return styleMuted.Render("Collecting cluster data...")
	}

	var b strings.Builder

	for i, cs := range snap.Nodes {
		if i > 0 {
			b.WriteString("\n")
		}

		host := cs.Node.Host
		role := cs.Node.Role
		if role == "" {
			role = "node"
		}

		if cs.Error != nil {
			b.WriteString(styleBold.Render(fmt.Sprintf("Node: %s", host)))
			b.WriteString(fmt.Sprintf(" (%s)", role))
			b.WriteString("\n")
			b.WriteString(separator(width))
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("  Status:  %s\n", styleCritical.Render("UNREACHABLE")))
			b.WriteString(fmt.Sprintf("  Error:   %s\n", styleCritical.Render(cs.Error.Error())))
			b.WriteString(fmt.Sprintf("  Took:    %s\n", styleMuted.Render(cs.Duration.Truncate(time.Millisecond).String())))
			if cs.Retries > 0 {
				b.WriteString(fmt.Sprintf("  Retries: %d\n", cs.Retries))
			}
			continue
		}

		r := cs.Report
		if r == nil {
			continue
		}

		b.WriteString(styleBold.Render(fmt.Sprintf("Node: %s", host)))
		b.WriteString(fmt.Sprintf(" (%s)  ", role))
		b.WriteString(styleHealthy.Render("ONLINE"))
		if r.Version != "" {
			b.WriteString(fmt.Sprintf("  v%s", r.Version))
		}
		b.WriteString("\n")
		b.WriteString(separator(width))
		b.WriteString("\n")

		// System Resources
		if r.System != nil {
			sys := r.System
			b.WriteString(styleBold.Render("  System"))
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("    CPU:      %d cores, load %.1f / %.1f / %.1f\n",
				sys.CPUCount, sys.LoadAvg1, sys.LoadAvg5, sys.LoadAvg15))
			b.WriteString(fmt.Sprintf("    Memory:   %s  (%d / %d MB, %d MB avail)\n",
				colorPct(sys.MemUsePct), sys.MemUsedMB, sys.MemTotalMB, sys.MemAvailMB))
			b.WriteString(fmt.Sprintf("    Disk:     %s  (%s / %s, %s avail)\n",
				colorPct(sys.DiskUsePct), sys.DiskUsedGB, sys.DiskTotalGB, sys.DiskAvailGB))
			if sys.SwapTotalMB > 0 {
				b.WriteString(fmt.Sprintf("    Swap:     %d / %d MB\n", sys.SwapUsedMB, sys.SwapTotalMB))
			}
			b.WriteString(fmt.Sprintf("    Uptime:   %s\n", sys.UptimeSince))
			if sys.OOMKills > 0 {
				b.WriteString(fmt.Sprintf("    OOM:      %s\n", styleCritical.Render(fmt.Sprintf("%d kills", sys.OOMKills))))
			}
		}

		// Services
		if r.Services != nil && len(r.Services.Services) > 0 {
			b.WriteString(styleBold.Render("  Services"))
			b.WriteString("\n")
			for _, svc := range r.Services.Services {
				stateStr := styleHealthy.Render(svc.ActiveState)
				if svc.ActiveState == "failed" {
					stateStr = styleCritical.Render("FAILED")
				} else if svc.ActiveState != "active" {
					stateStr = styleWarning.Render(svc.ActiveState)
				}
				extra := ""
				if svc.MemoryCurrentMB > 0 {
					extra += fmt.Sprintf(" mem=%dMB", svc.MemoryCurrentMB)
				}
				if svc.NRestarts > 0 {
					extra += fmt.Sprintf(" restarts=%d", svc.NRestarts)
				}
				if svc.RestartLoopRisk {
					extra += styleCritical.Render(" RESTART-LOOP")
				}
				b.WriteString(fmt.Sprintf("    %-28s %s%s\n", svc.Name, stateStr, extra))
			}
			if len(r.Services.FailedUnits) > 0 {
				b.WriteString(fmt.Sprintf("    Failed units: %s\n",
					styleCritical.Render(strings.Join(r.Services.FailedUnits, ", "))))
			}
		}

		// RQLite
		if r.RQLite != nil {
			rq := r.RQLite
			b.WriteString(styleBold.Render("  RQLite"))
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("    Responsive: %s   Ready: %s   Strong Read: %s\n",
				statusStr(rq.Responsive), statusStr(rq.Ready), statusStr(rq.StrongRead)))
			if rq.Responsive {
				b.WriteString(fmt.Sprintf("    Raft: %s   Leader: %s   Term: %d   Applied: %d\n",
					styleBold.Render(rq.RaftState), rq.LeaderAddr, rq.Term, rq.Applied))
				if rq.DBSize != "" {
					b.WriteString(fmt.Sprintf("    DB size: %s   Peers: %d   Goroutines: %d   Heap: %dMB\n",
						rq.DBSize, rq.NumPeers, rq.Goroutines, rq.HeapMB))
				}
			}
		}

		// WireGuard
		if r.WireGuard != nil {
			wg := r.WireGuard
			b.WriteString(styleBold.Render("  WireGuard"))
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("    Interface: %s   IP: %s   Peers: %d\n",
				statusStr(wg.InterfaceUp), wg.WgIP, wg.PeerCount))
		}

		// Network
		if r.Network != nil {
			net := r.Network
			b.WriteString(styleBold.Render("  Network"))
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("    Internet: %s   UFW: %s   TCP est: %d  retrans: %.1f%%\n",
				statusStr(net.InternetReachable), statusStr(net.UFWActive),
				net.TCPEstablished, net.TCPRetransRate))
		}
	}

	return b.String()
}

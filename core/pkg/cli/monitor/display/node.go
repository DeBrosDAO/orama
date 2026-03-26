package display

import (
	"fmt"
	"io"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// NodeTable prints detailed per-node information to w.
func NodeTable(snap *monitor.ClusterSnapshot, w io.Writer) error {
	for i, cs := range snap.Nodes {
		if i > 0 {
			fmt.Fprintln(w)
		}

		host := cs.Node.Host
		role := cs.Node.Role

		if cs.Error != nil {
			fmt.Fprintf(w, "%s (%s)\n", styleRed.Render("Node: "+host), role)
			fmt.Fprintf(w, "  %s\n", styleRed.Render(fmt.Sprintf("UNREACHABLE: %v", cs.Error)))
			continue
		}

		r := cs.Report
		if r == nil {
			fmt.Fprintf(w, "%s (%s)\n", styleRed.Render("Node: "+host), role)
			fmt.Fprintf(w, "  %s\n", styleRed.Render("No report available"))
			continue
		}

		fmt.Fprintf(w, "%s\n", styleBold.Render(fmt.Sprintf("Node: %s (%s)", host, role)))

		// System
		if r.System != nil {
			sys := r.System
			fmt.Fprintf(w, "  System:    CPU %d | Load %.2f | Mem %d%% (%d/%d MB) | Disk %d%%\n",
				sys.CPUCount, sys.LoadAvg1, sys.MemUsePct, sys.MemUsedMB, sys.MemTotalMB, sys.DiskUsePct)
		} else {
			fmt.Fprintln(w, "  System:    "+styleMuted.Render("no data"))
		}

		// RQLite
		if r.RQLite != nil {
			rq := r.RQLite
			readyStr := styleRed.Render("Not Ready")
			if rq.Ready {
				readyStr = styleGreen.Render("Ready")
			}
			if rq.Responsive {
				fmt.Fprintf(w, "  RQLite:    %s | Term %d | Applied %d | Peers %d | %s\n",
					rq.RaftState, rq.Term, rq.Applied, rq.NumPeers, readyStr)
			} else {
				fmt.Fprintf(w, "  RQLite:    %s\n", styleRed.Render("NOT RESPONDING"))
			}
		} else {
			fmt.Fprintln(w, "  RQLite:    "+styleMuted.Render("not configured"))
		}

		// WireGuard
		if r.WireGuard != nil {
			wg := r.WireGuard
			if wg.InterfaceUp {
				// Check handshakes
				hsOK := true
				for _, p := range wg.Peers {
					if p.LatestHandshake == 0 || p.HandshakeAgeSec > 180 {
						hsOK = false
						break
					}
				}
				hsStr := statusIcon(hsOK)
				fmt.Fprintf(w, "  WireGuard: UP | %s | %d peers | handshakes %s\n",
					wg.WgIP, wg.PeerCount, hsStr)
			} else {
				fmt.Fprintf(w, "  WireGuard: %s\n", styleRed.Render("DOWN"))
			}
		} else {
			fmt.Fprintln(w, "  WireGuard: "+styleMuted.Render("not configured"))
		}

		// Olric
		if r.Olric != nil {
			ol := r.Olric
			stateStr := styleRed.Render("inactive")
			if ol.ServiceActive {
				stateStr = styleGreen.Render("active")
			}
			fmt.Fprintf(w, "  Olric:     %s | %d members\n", stateStr, ol.MemberCount)
		} else {
			fmt.Fprintln(w, "  Olric:     "+styleMuted.Render("not configured"))
		}

		// IPFS
		if r.IPFS != nil {
			ipfs := r.IPFS
			daemonStr := styleRed.Render("inactive")
			if ipfs.DaemonActive {
				daemonStr = styleGreen.Render("active")
			}
			clusterStr := styleRed.Render("DOWN")
			if ipfs.ClusterActive {
				clusterStr = styleGreen.Render("OK")
			}
			fmt.Fprintf(w, "  IPFS:      %s | %d swarm peers | cluster %s\n",
				daemonStr, ipfs.SwarmPeerCount, clusterStr)
		} else {
			fmt.Fprintln(w, "  IPFS:      "+styleMuted.Render("not configured"))
		}

		// Anyone
		if r.Anyone != nil {
			an := r.Anyone
			mode := an.Mode
			if mode == "" {
				if an.RelayActive {
					mode = "relay"
				} else if an.ClientActive {
					mode = "client"
				} else {
					mode = "inactive"
				}
			}
			bootStr := styleRed.Render("not bootstrapped")
			if an.Bootstrapped {
				bootStr = styleGreen.Render("bootstrapped")
			}
			fmt.Fprintf(w, "  Anyone:    %s | %s\n", mode, bootStr)
		} else {
			fmt.Fprintln(w, "  Anyone:    "+styleMuted.Render("not configured"))
		}
	}

	return nil
}

// NodeJSON writes the node details as JSON.
func NodeJSON(snap *monitor.ClusterSnapshot, w io.Writer) error {
	type nodeDetail struct {
		Host   string      `json:"host"`
		Role   string      `json:"role"`
		Status string      `json:"status"`
		Error  string      `json:"error,omitempty"`
		Report interface{} `json:"report,omitempty"`
	}

	var entries []nodeDetail
	for _, cs := range snap.Nodes {
		e := nodeDetail{
			Host: cs.Node.Host,
			Role: cs.Node.Role,
		}
		if cs.Error != nil {
			e.Status = "unreachable"
			e.Error = cs.Error.Error()
		} else if cs.Report != nil {
			e.Status = "ok"
			e.Report = cs.Report
		} else {
			e.Status = "unknown"
		}
		entries = append(entries, e)
	}

	return writeJSON(w, entries)
}

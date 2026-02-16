package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// MeshTable prints WireGuard mesh status to w.
func MeshTable(snap *monitor.ClusterSnapshot, w io.Writer) error {
	fmt.Fprintf(w, "%s\n", styleBold.Render(
		fmt.Sprintf("WireGuard Mesh \u2014 %s", snap.Environment)))
	fmt.Fprintln(w, strings.Repeat("\u2550", 28))
	fmt.Fprintln(w)

	// Header
	fmt.Fprintf(w, "%-18s %-12s %-7s %-7s %s\n",
		styleHeader.Render("NODE"),
		styleHeader.Render("WG IP"),
		styleHeader.Render("PORT"),
		styleHeader.Render("PEERS"),
		styleHeader.Render("STATUS"))
	fmt.Fprintln(w, separator(54))

	// Collect mesh info for peer details
	type meshNode struct {
		host    string
		wgIP    string
		port    int
		peers   int
		total   int
		healthy bool
	}
	var meshNodes []meshNode

	expectedPeers := snap.HealthyCount() - 1

	for _, cs := range snap.Nodes {
		if cs.Error != nil || cs.Report == nil {
			continue
		}
		r := cs.Report
		if r.WireGuard == nil {
			fmt.Fprintf(w, "%-18s %s\n", cs.Node.Host, styleMuted.Render("no WireGuard"))
			continue
		}

		wg := r.WireGuard
		peerCount := wg.PeerCount
		allOK := wg.InterfaceUp
		if allOK {
			for _, p := range wg.Peers {
				if p.LatestHandshake == 0 || p.HandshakeAgeSec > 180 {
					allOK = false
					break
				}
			}
		}

		mn := meshNode{
			host:    cs.Node.Host,
			wgIP:    wg.WgIP,
			port:    wg.ListenPort,
			peers:   peerCount,
			total:   expectedPeers,
			healthy: allOK,
		}
		meshNodes = append(meshNodes, mn)

		peerStr := fmt.Sprintf("%d/%d", peerCount, expectedPeers)
		statusStr := statusIcon(allOK)
		if !wg.InterfaceUp {
			statusStr = styleRed.Render("DOWN")
		}

		fmt.Fprintf(w, "%-18s %-12s %-7d %-7s %s\n",
			cs.Node.Host, wg.WgIP, wg.ListenPort, peerStr, statusStr)
	}

	// Peer details
	fmt.Fprintln(w)
	fmt.Fprintln(w, styleBold.Render("Peer Details:"))

	for _, cs := range snap.Nodes {
		if cs.Error != nil || cs.Report == nil || cs.Report.WireGuard == nil {
			continue
		}
		wg := cs.Report.WireGuard
		if !wg.InterfaceUp {
			continue
		}
		localIP := wg.WgIP
		for _, p := range wg.Peers {
			hsAge := formatDuration(p.HandshakeAgeSec)
			rx := formatBytes(p.TransferRx)
			tx := formatBytes(p.TransferTx)

			peerIP := p.AllowedIPs
			// Strip CIDR if present
			if idx := strings.Index(peerIP, "/"); idx > 0 {
				peerIP = peerIP[:idx]
			}

			hsColor := styleGreen
			if p.LatestHandshake == 0 {
				hsAge = "never"
				hsColor = styleRed
			} else if p.HandshakeAgeSec > 180 {
				hsColor = styleYellow
			}

			fmt.Fprintf(w, "  %s \u2194 %s: handshake %s, rx: %s, tx: %s\n",
				localIP, peerIP, hsColor.Render(hsAge), rx, tx)
		}
	}

	return nil
}

// MeshJSON writes the WireGuard mesh as JSON.
func MeshJSON(snap *monitor.ClusterSnapshot, w io.Writer) error {
	type peerEntry struct {
		AllowedIPs      string `json:"allowed_ips"`
		HandshakeAgeSec int64  `json:"handshake_age_sec"`
		TransferRxBytes int64  `json:"transfer_rx_bytes"`
		TransferTxBytes int64  `json:"transfer_tx_bytes"`
	}
	type meshEntry struct {
		Host       string      `json:"host"`
		WgIP       string      `json:"wg_ip"`
		ListenPort int         `json:"listen_port"`
		PeerCount  int         `json:"peer_count"`
		Up         bool        `json:"up"`
		Peers      []peerEntry `json:"peers,omitempty"`
	}

	var entries []meshEntry
	for _, cs := range snap.Nodes {
		if cs.Error != nil || cs.Report == nil || cs.Report.WireGuard == nil {
			continue
		}
		wg := cs.Report.WireGuard
		e := meshEntry{
			Host:       cs.Node.Host,
			WgIP:       wg.WgIP,
			ListenPort: wg.ListenPort,
			PeerCount:  wg.PeerCount,
			Up:         wg.InterfaceUp,
		}
		for _, p := range wg.Peers {
			e.Peers = append(e.Peers, peerEntry{
				AllowedIPs:      p.AllowedIPs,
				HandshakeAgeSec: p.HandshakeAgeSec,
				TransferRxBytes: p.TransferRx,
				TransferTxBytes: p.TransferTx,
			})
		}
		entries = append(entries, e)
	}

	return writeJSON(w, entries)
}

// formatDuration formats seconds into a human-readable string.
func formatDuration(sec int64) string {
	if sec < 60 {
		return fmt.Sprintf("%ds ago", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm ago", sec/60)
	}
	return fmt.Sprintf("%dh ago", sec/3600)
}

// formatBytes formats bytes into a human-readable string.
func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1fGB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1fMB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1fKB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

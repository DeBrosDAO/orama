package tui

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
	"github.com/DeBrosOfficial/network/pkg/cli/production/report"
)

// renderWGMesh renders the WireGuard mesh status tab with peer details.
func renderWGMesh(snap *monitor.ClusterSnapshot, width int) string {
	if snap == nil {
		return styleMuted.Render("Collecting cluster data...")
	}

	reports := snap.Healthy()
	if len(reports) == 0 {
		return styleMuted.Render("No healthy nodes to display.")
	}

	var b strings.Builder

	// Mesh overview
	b.WriteString(styleBold.Render("WireGuard Mesh Overview"))
	b.WriteString("\n")
	b.WriteString(separator(width))
	b.WriteString("\n\n")

	// Summary header
	b.WriteString(fmt.Sprintf("  %-18s %-10s %-18s %-6s %-8s\n",
		headerStyle.Render("HOST"),
		headerStyle.Render("IFACE"),
		headerStyle.Render("WG IP"),
		headerStyle.Render("PEERS"),
		headerStyle.Render("PORT"),
	))

	wgNodes := 0
	for _, r := range reports {
		if r.WireGuard == nil {
			continue
		}
		wgNodes++
		wg := r.WireGuard
		ifaceStr := statusStr(wg.InterfaceUp)
		b.WriteString(fmt.Sprintf("  %-18s %-10s %-18s %-6d %-8d\n",
			nodeHost(r), ifaceStr, wg.WgIP, wg.PeerCount, wg.ListenPort))
	}

	if wgNodes == 0 {
		return styleMuted.Render("No nodes have WireGuard configured.")
	}

	expectedPeers := wgNodes - 1

	// Per-node peer details
	b.WriteString("\n")
	b.WriteString(styleBold.Render("Peer Details"))
	b.WriteString("\n")
	b.WriteString(separator(width))
	b.WriteString("\n")

	for _, r := range reports {
		if r.WireGuard == nil || len(r.WireGuard.Peers) == 0 {
			continue
		}

		b.WriteString("\n")
		host := nodeHost(r)
		peerCountStr := fmt.Sprintf("%d/%d peers", len(r.WireGuard.Peers), expectedPeers)
		if len(r.WireGuard.Peers) < expectedPeers {
			peerCountStr = styleCritical.Render(peerCountStr)
		} else {
			peerCountStr = styleHealthy.Render(peerCountStr)
		}
		b.WriteString(fmt.Sprintf("  %s  %s\n", styleBold.Render(host), peerCountStr))

		for _, p := range r.WireGuard.Peers {
			b.WriteString(renderPeerLine(p))
		}
	}

	return b.String()
}

// renderPeerLine formats a single WG peer.
func renderPeerLine(p report.WGPeerInfo) string {
	keyShort := p.PublicKey
	if len(keyShort) > 12 {
		keyShort = keyShort[:12] + "..."
	}

	// Handshake status
	var hsStr string
	if p.LatestHandshake == 0 {
		hsStr = styleCritical.Render("never")
	} else if p.HandshakeAgeSec > 180 {
		hsStr = styleWarning.Render(fmt.Sprintf("%ds ago", p.HandshakeAgeSec))
	} else {
		hsStr = styleHealthy.Render(fmt.Sprintf("%ds ago", p.HandshakeAgeSec))
	}

	// Transfer
	rx := formatBytes(p.TransferRx)
	tx := formatBytes(p.TransferTx)

	return fmt.Sprintf("    key=%s  endpoint=%-22s  hs=%s  rx=%s tx=%s  ips=%s\n",
		styleMuted.Render(keyShort),
		p.Endpoint,
		hsStr,
		rx, tx,
		p.AllowedIPs,
	)
}

// formatBytes formats bytes into a human-readable string.
func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

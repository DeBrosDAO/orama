package node

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// activeOverlayPeersSQL selects the registered, active nodes that have a
// WireGuard overlay address. Only those can be reached for inter-node queries:
// the gateway port is firewalled off the public interface.
const activeOverlayPeersSQL = `SELECT id, internal_ip FROM dns_nodes WHERE status = 'active' AND internal_ip IS NOT NULL AND internal_ip != ''`

// activeOverlayPeers lists the cluster's active nodes from the registry, each
// with its own node peer id. A row whose address is not inside the WireGuard
// overlay is not a target — requests signed for a node go only over the mesh —
// and is counted in skipped for the caller to report.
func activeOverlayPeers(ctx context.Context, db *sql.DB) (targets []ipfs.PeerTarget, skipped int, err error) {
	rows, err := rqlite.SafeQueryContext(db, ctx, activeOverlayPeersSQL)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read dns_nodes: %w", err)
	}
	defer rows.Close()

	overlay := constants.WireGuardOverlay()
	for rows.Next() {
		var t ipfs.PeerTarget
		if err := rows.Scan(&t.ID, &t.IP); err != nil {
			return nil, 0, fmt.Errorf("failed to scan dns_nodes: %w", err)
		}
		if ip, perr := netip.ParseAddr(t.IP); perr != nil || !overlay.Contains(ip) {
			skipped++
			continue
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("failed to iterate dns_nodes: %w", err)
	}
	return targets, skipped, nil
}

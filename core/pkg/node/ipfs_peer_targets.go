package node

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// activeOverlayPeersSQL selects the registered, active nodes that have a
// WireGuard overlay address. Only those can be reached for inter-node queries:
// the gateway port is firewalled off the public interface.
const activeOverlayPeersSQL = `SELECT id, internal_ip FROM dns_nodes WHERE status = 'active' AND internal_ip IS NOT NULL AND internal_ip != ''`

// activeOverlayPeers lists the cluster's active nodes from the registry, each
// with its own node peer id.
func activeOverlayPeers(ctx context.Context, db *sql.DB) ([]ipfs.PeerTarget, error) {
	rows, err := rqlite.SafeQueryContext(db, ctx, activeOverlayPeersSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to read dns_nodes: %w", err)
	}
	defer rows.Close()

	var out []ipfs.PeerTarget
	for rows.Next() {
		var t ipfs.PeerTarget
		if err := rows.Scan(&t.ID, &t.IP); err != nil {
			return nil, fmt.Errorf("failed to scan dns_nodes: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate dns_nodes: %w", err)
	}
	return out, nil
}

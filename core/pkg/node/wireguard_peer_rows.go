package node

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// scanWGPeers runs the membership query against db and returns the peer set
// keyed by public key, excluding this node (localPubKey, whose overlay address
// is localWGIP).
//
// A row that fails to scan — the driver could not decode a column — is a hard
// error. The query itself failing is too. A row that scans but is not a peer
// this node may apply is logged and skipped: Register refuses such a row on
// the way in, and one legacy row that still fails the check must not stop
// every node applying the rest of the mesh. Skipping it leaves that one peer
// out of the desired set; the rows that scanned are still applied.
func scanWGPeers(ctx context.Context, db *sql.DB, localPubKey, localWGIP string, logger *zap.Logger) (map[string]install.WireGuardPeer, error) {
	rows, err := rqlite.SafeQueryContext(db, ctx, wgPeerQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	peers := make(map[string]install.WireGuardPeer)
	holders := make(map[string]string) // allowed IP -> node id
	for rows.Next() {
		var nodeID, wgIP, pubKey, pubIP string
		var wgPort int
		if err := rows.Scan(&nodeID, &wgIP, &pubKey, &pubIP, &wgPort); err != nil {
			return nil, fmt.Errorf("scan wireguard_peers row: %w", err)
		}
		if pubKey == localPubKey {
			continue // skip self
		}
		if pubKey == "" || wgIP == "" {
			logger.Warn("skipping a wireguard_peers row that cannot be applied",
				zap.String("node_id", nodeID),
				zap.String("reason", "empty public_key or wg_ip"))
			continue
		}
		peer, err := meshPeerFromRow(wgIP, pubKey, pubIP, wgPort, localWGIP)
		if err != nil {
			logger.Warn("skipping a wireguard_peers row that cannot be applied",
				zap.String("node_id", nodeID), zap.Error(err))
			continue
		}
		if other, taken := holders[peer.AllowedIP]; taken {
			return nil, fmt.Errorf("wireguard_peers rows for nodes %q and %q both claim %s; "+
				"wg routes an address to one peer only", other, nodeID, peer.AllowedIP)
		}
		holders[peer.AllowedIP] = nodeID
		peers[pubKey] = peer
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate wireguard_peers: %w", err)
	}
	return peers, nil
}

// meshPeerFromRow builds the peer a wireguard_peers row describes and checks it
// exactly as orama-privhelper checks a peer before writing it to wg0.conf
// (privhelper.ValidatePeer): a canonical 32-byte key, an ip:port endpoint and
// a single /32 inside the overlay — plus that the address is not this node's
// own, which would route this node's overlay address away from itself.
//
// A row with no public_ip yields a peer with no endpoint rather than the
// endpoint ":51820", which is not an address: the peer is still applied and
// the handshake waits for it to reach this node.
func meshPeerFromRow(wgIP, pubKey, pubIP string, wgPort int, localWGIP string) (install.WireGuardPeer, error) {
	if wgPort == 0 {
		wgPort = defaultWireGuardPort
	}
	peer := install.WireGuardPeer{PublicKey: pubKey, AllowedIP: wgIP + "/32"}
	if pubIP != "" {
		peer.Endpoint = net.JoinHostPort(pubIP, strconv.Itoa(wgPort))
	}
	if err := privhelper.ValidatePeer(peer); err != nil {
		return install.WireGuardPeer{}, err
	}
	if wgIP == localWGIP {
		return install.WireGuardPeer{}, fmt.Errorf("its wg_ip %s is this node's own overlay address", wgIP)
	}
	return peer, nil
}

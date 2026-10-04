package nodeapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/overlay"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// errNotAdmitted refuses a node that no join admitted.
var errNotAdmitted = errors.New("this node was never admitted to the cluster: " +
	"join it with an operator-minted invite (orama node install --join), which records its peer before it registers")

// admissionRow is what admitted reads about a node in one query.
type admissionRow struct {
	Known       int `db:"known"`
	Peer        int `db:"peer"`
	Placeholder int `db:"placeholder"`
	Taken       int `db:"taken"`
	Registry    int `db:"registry"`
}

// registration is who is registering: the node id the stamp proves, the
// overlay address it claims, and the overlay address the request is
// attributable to ("" when it is not attributable to one).
type registration struct {
	nodeID, claimedIP, sourceIP string
}

// admitted reports whether a node may write itself into the node registry. A
// key a caller generated itself proves only that the caller holds it; this is
// what says the cluster let that node in. It is admitted when:
//   - it is registered and not retired: every node that ran before this
//     check, and a restart or rolling upgrade, carries on, and a retired
//     machine does not bring itself back;
//   - a WireGuard peer row exists under its id: the join (under an invite),
//     the enrolment (under a token) and the peer endpoint (over the mesh with
//     the cluster secret) are the only writers of that row for a new node;
//   - an OramaOS node's peer row exists at the overlay address it claims,
//     under the placeholder id the enrolment recorded it with (it had no
//     libp2p identity yet), the request comes from that address, and no other
//     live node holds the address;
//   - the registry is empty: the genesis node of a new cluster, which nothing
//     can have admitted.
//
// Without it any caller that reached /v1/internal/node/* could make up an
// identity, enrol a key for it and register a dns_nodes row, entering
// namespace placement and DNS as a node nobody admitted.
func admitted(ctx context.Context, db rqlite.Client, reg registration) error {
	var rows []admissionRow
	if err := db.Query(ctx, &rows,
		`SELECT
		   (SELECT COUNT(*) FROM dns_nodes WHERE id = ? AND last_seen != ?) AS known,
		   (SELECT COUNT(*) FROM wireguard_peers WHERE node_id = ?) AS peer,
		   (SELECT COUNT(*) FROM wireguard_peers WHERE wg_ip = ? AND node_id = ?) AS placeholder,
		   (SELECT COUNT(*) FROM dns_nodes WHERE internal_ip = ? AND id != ? AND last_seen != ?) AS taken,
		   (SELECT COUNT(*) FROM dns_nodes) AS registry`,
		reg.nodeID, constants.RetiredNodeLastSeen,
		reg.nodeID,
		reg.claimedIP, overlay.PlaceholderNodeID(reg.claimedIP),
		reg.claimedIP, reg.nodeID, constants.RetiredNodeLastSeen); err != nil {
		return fmt.Errorf("could not read whether node %s was admitted: %w", reg.nodeID, err)
	}
	if len(rows) != 1 {
		return fmt.Errorf("could not read whether node %s was admitted: %d rows", reg.nodeID, len(rows))
	}
	r := rows[0]
	placeholder := r.Placeholder > 0 && r.Taken == 0 && reg.sourceIP != "" && reg.sourceIP == reg.claimedIP
	if r.Known > 0 || r.Peer > 0 || placeholder || r.Registry == 0 {
		return nil
	}
	return errNotAdmitted
}

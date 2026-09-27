// Package hub is the cluster gateway's side of monitoring: it keeps this
// node's own report fresh, gathers its peers' reports over the WireGuard mesh,
// assembles them into a cluster snapshot, and records service uptime.
//
// It replaces SSHing into every node on every refresh. Each node collects its
// own report on a timer; a viewer's request is answered from the reports the
// nodes already hold, so what it costs does not grow with the number of
// viewers, and a node that stops answering shows up as unreachable within one
// collection interval.
package hub

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Peer is a node the cluster registry knows.
type Peer struct {
	ID       string
	PublicIP string
	WGIP     string
	Role     string
	Status   string
}

// PeerLister lists the nodes a snapshot covers.
type PeerLister interface {
	Peers(ctx context.Context) ([]Peer, error)
}

// membershipWindow is how long after its last heartbeat a node stays in the
// snapshot. A node that dies must show as unreachable rather than vanish; one
// silent for a day has been removed or replaced, and keeping it would leave a
// permanent false outage.
const membershipWindow = 24 * time.Hour

// rqliteTimeLayout is how dns_nodes.last_seen is stored (UTC).
const rqliteTimeLayout = "2006-01-02 15:04:05"

// DBPeerLister reads nodes from the cluster registry's dns_nodes table.
type DBPeerLister struct {
	DB  *sql.DB
	Now func() time.Time
}

// Peers returns every node heard from within the membership window, ordered by id.
func (l DBPeerLister) Peers(ctx context.Context) ([]Peer, error) {
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	cutoff := now().UTC().Add(-membershipWindow).Format(rqliteTimeLayout)
	rows, err := rqlite.SafeQueryContext(l.DB, ctx, `
		SELECT id, COALESCE(ip_address, ''), COALESCE(internal_ip, ''),
		       COALESCE(role, 'node'), COALESCE(status, '')
		  FROM dns_nodes
		 WHERE COALESCE(last_seen, '') > ?
		 ORDER BY id`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list nodes from dns_nodes in the cluster registry: %w", err)
	}
	defer rows.Close()

	var peers []Peer
	for rows.Next() {
		var p Peer
		if err := rows.Scan(&p.ID, &p.PublicIP, &p.WGIP, &p.Role, &p.Status); err != nil {
			return nil, fmt.Errorf("read a dns_nodes row: %w", err)
		}
		peers = append(peers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list nodes from dns_nodes: %w", err)
	}
	return peers, nil
}

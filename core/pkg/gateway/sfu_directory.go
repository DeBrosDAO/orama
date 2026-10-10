package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	webrtchandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/webrtc"
)

// sfuDirectoryTTL bounds how long a namespace's SFU list is reused. The list
// changes only when a role is reallocated (a node replaced or gone), so a short
// cache keeps the join path off the registry while a change is seen within
// seconds.
const sfuDirectoryTTL = 10 * time.Second

// sfuNodesQuery lists the SFU roles of a namespace on nodes the fleet still
// counts as active and that have a WireGuard address: the SFU binds only to it,
// and a public IP is never a fallback for inter-node traffic.
// webrtc_port_allocations is the authority for which node
// runs an SFU (website/src/docs/operator/webrtc-operations.mdx#role-reconciliation); the health probe decides
// whether it is serving right now.
const sfuNodesQuery = `
	SELECT dn.id, dn.internal_ip, wpa.sfu_signaling_port
	FROM webrtc_port_allocations wpa
	JOIN namespace_clusters nc ON wpa.namespace_cluster_id = nc.id
	JOIN dns_nodes dn ON wpa.node_id = dn.id
	WHERE nc.namespace_name = ?
	  AND wpa.service_type = 'sfu'
	  AND wpa.sfu_signaling_port > 0
	  AND dn.status = 'active'
	  AND dn.internal_ip IS NOT NULL AND dn.internal_ip <> ''
	ORDER BY dn.id`

type sfuDirectoryEntry struct {
	nodes []webrtchandlers.SFUNode
	at    time.Time
}

// registrySFUDirectory reads SFU placement from the cluster registry (the main
// rqlite, not the tenant's own) and caches it per namespace.
type registrySFUDirectory struct {
	db  *sql.DB
	ttl time.Duration

	mu    sync.Mutex
	cache map[string]sfuDirectoryEntry
}

func newRegistrySFUDirectory(db *sql.DB, ttl time.Duration) *registrySFUDirectory {
	return &registrySFUDirectory{db: db, ttl: ttl, cache: make(map[string]sfuDirectoryEntry)}
}

// SFUNodes returns the namespace's SFU nodes. A failed refresh returns the
// error rather than an old list: placing a room from a stale set would put it
// on a node the registry no longer lists.
func (d *registrySFUDirectory) SFUNodes(ctx context.Context, namespace string) ([]webrtchandlers.SFUNode, error) {
	d.mu.Lock()
	e, ok := d.cache[namespace]
	d.mu.Unlock()
	if ok && time.Since(e.at) < d.ttl {
		return e.nodes, nil
	}

	// The registry read runs outside the lock so one slow read does not stall
	// every namespace's joins; concurrent misses each read and the last wins.
	rows, err := d.db.QueryContext(ctx, sfuNodesQuery, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to query SFU nodes: %w", err)
	}
	defer rows.Close()

	var nodes []webrtchandlers.SFUNode
	for rows.Next() {
		var n webrtchandlers.SFUNode
		if err := rows.Scan(&n.NodeID, &n.Host, &n.Port); err != nil {
			return nil, fmt.Errorf("failed to read an SFU node row: %w", err)
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read SFU nodes: %w", err)
	}

	d.mu.Lock()
	d.cache[namespace] = sfuDirectoryEntry{nodes: nodes, at: time.Now()}
	d.mu.Unlock()
	return nodes, nil
}

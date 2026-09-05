package node

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/nodeapi"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/wireguard"
	"go.uber.org/zap"
)

// registerDNSNode records this node in the dns_nodes table for deployment
// routing.
//
// The row is written by the index gateway on this host rather than by this
// process: it is a promise every consumer trusts — `status = 'active' AND
// last_seen > ?` is what routes real traffic — and a request can be
// authenticated where a direct INSERT could not be. Which node the row is about
// comes from the stamp on that request, so this node can only ever register
// itself.
func (n *Node) registerDNSNode(ctx context.Context) error {
	client, err := n.coreAPIClient(ctx)
	if err != nil {
		return fmt.Errorf("cannot record this node: %w", err)
	}

	// A node that cannot work out its own address does not register. This used
	// to fall back to 127.0.0.1, which every consumer of `status = 'active'`
	// then handed out as the address to reach this node on — a node that could
	// not answer the question published a wrong answer instead of none.
	ipAddress, err := n.getNodeIPAddress()
	if err != nil {
		return fmt.Errorf("cannot determine this node's address, so it must not advertise itself: %w", err)
	}

	// The overlay address is what other nodes dial for raft, namespace cluster
	// membership and eviction. The gateway checks it against the address the
	// cluster allocated, so a node that is not on the mesh yet says so rather
	// than offering its public address in that column.
	internalIP, err := n.getWireGuardIP()
	if err != nil {
		return fmt.Errorf("cannot determine this node's overlay address: %w", err)
	}
	if internalIP == "" {
		return fmt.Errorf("this node has no overlay address yet, so it cannot be reached by other nodes")
	}

	// Determine region (defaulting to "local" for now, could be from cloud metadata in future)
	region := "local"

	if err := client.Register(ctx, nodeapi.RegisterRequest{
		IPAddress:      ipAddress,
		InternalIP:     internalIP,
		Region:         region,
		SSHUser:        n.config.Node.SSHUser,
		Environment:    n.config.Node.Environment,
		OperatorWallet: n.config.Node.OperatorWallet,
	}); err != nil {
		return fmt.Errorf("failed to register DNS node: %w", err)
	}

	n.logger.ComponentInfo(logging.ComponentNode, "Registered DNS node",
		zap.String("node_id", n.GetPeerID()),
		zap.String("ip_address", ipAddress),
		zap.String("region", region),
	)

	return nil
}

// startDNSHeartbeat starts a goroutine that periodically updates the node's last_seen timestamp
func (n *Node) startDNSHeartbeat(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				n.logger.ComponentInfo(logging.ComponentNode, "DNS heartbeat stopped")
				return
			case <-ticker.C:
				if err := n.updateDNSHeartbeat(ctx); err != nil {
					n.logger.ComponentWarn(logging.ComponentNode, "Failed to update DNS heartbeat", zap.Error(err))
				}
				// Self-healing: ensure this node's DNS records exist on every heartbeat
				if err := n.ensureBaseDNSRecords(ctx); err != nil {
					n.logger.ComponentWarn(logging.ComponentNode, "Failed to ensure DNS records on heartbeat", zap.Error(err))
				}
				// Re-advertise this node in the namespace gateway round-robin.
				// MUST run after the heartbeat above (which re-asserts 'active')
				// and before the purge below, so a just-recovered node is no
				// longer purge-eligible by the time it re-adds itself.
				n.ensureNamespaceHostRecords(ctx)
				// Retract this node's own TURN records for namespaces it is no
				// longer a TURN node for. The inactive-node purge cannot catch
				// these: the node is alive, it just does not serve that namespace.
				n.retractForeignTURNRecords(ctx)
				// Remove DNS records for nodes that stopped heartbeating
				n.cleanupStaleNodeRecords(ctx)
				// Purge per-namespace records (gateway host, TURN, stealth) that
				// still point at inactive nodes (bugboard #158) — neither an RPC/
				// WebSocket client nor a relay-only call must resolve a removed node.
				n.purgeInactiveNodeRecords(ctx)
			}
		}
	}()

	n.logger.ComponentInfo(logging.ComponentNode, "Started DNS heartbeat (30s interval)")
}

// updateDNSHeartbeat refreshes this node's last_seen timestamp in dns_nodes.
//
// The gateway re-asserts 'active' as well as refreshing last_seen: a live,
// heartbeating node must count as active, which heals a node that was reaped to
// 'inactive' during a restart window without a fresh registration.
func (n *Node) updateDNSHeartbeat(ctx context.Context) error {
	client, err := n.coreAPIClient(ctx)
	if err != nil {
		return fmt.Errorf("cannot record this heartbeat: %w", err)
	}

	registered, err := client.Heartbeat(ctx)
	if err != nil {
		return fmt.Errorf("failed to update DNS heartbeat: %w", err)
	}
	// The row is missing entirely — the initial registration never landed, or
	// it was purged while this node was away. Register now.
	if !registered {
		return n.registerDNSNode(ctx)
	}
	return nil
}

// ensureBaseDNSRecords ensures this node's IP is present in the base DNS records.
// This provides self-healing: if records are missing (fresh install, DB reset),
// the node recreates them on startup. Each node only manages its own IP entries.
//
// Records are created for BOTH the base domain (dbrs.space) and the node domain
// (node1.dbrs.space). The base domain records enable round-robin load balancing
// across all nodes. The node domain records enable direct node access.
func (n *Node) ensureBaseDNSRecords(ctx context.Context) error {
	baseDomain := n.config.HTTPGateway.BaseDomain
	nodeDomain := n.config.Node.Domain

	if baseDomain == "" && nodeDomain == "" {
		return nil // No domain configured, skip
	}

	// The DNS record loop writes zone data directly, unlike the two writes
	// above it that record this node's identity — see the note on
	// registerDNSNode. It is the caller of this handle, so it is the one that
	// has to have it: registerDNSNode used to hold the only guard, and it no
	// longer touches the database at all.
	if n.getRQLiteAdapter() == nil {
		return fmt.Errorf("rqlite adapter not initialized")
	}

	ipAddress, err := n.getNodeIPAddress()
	if err != nil {
		return fmt.Errorf("failed to determine node IP: %w", err)
	}

	db := n.getRQLiteAdapter().GetSQLDB()

	// Clean up any private IP A records left by old code versions.
	// Old code could insert WireGuard IPs (10.0.0.x) into dns_records.
	// This self-heals on every heartbeat cycle.
	cleanupPrivateIPRecords(ctx, db, n.logger)

	// Build list of A records to ensure
	var records []struct {
		fqdn  string
		value string
	}

	// Base domain records (e.g., dbrs.space, *.dbrs.space) — only for nameserver nodes.
	// Apex/wildcard NS identity stays on nameserver nodes. Tenant TLS is
	// orama-namespace-caddy@index on every node that hosts a gateway.
	if baseDomain != "" && n.isNameserverNode(ctx) {
		records = append(records,
			struct{ fqdn, value string }{baseDomain + ".", ipAddress},
			struct{ fqdn, value string }{"*." + baseDomain + ".", ipAddress},
		)
	}

	// Node-specific records (e.g., node1.dbrs.space, *.node1.dbrs.space) — for direct node access
	if nodeDomain != "" && nodeDomain != baseDomain {
		records = append(records,
			struct{ fqdn, value string }{nodeDomain + ".", ipAddress},
			struct{ fqdn, value string }{"*." + nodeDomain + ".", ipAddress},
		)
	}

	// Insert root A record and wildcard A record for this node's IP
	// ON CONFLICT DO NOTHING avoids duplicates (UNIQUE on fqdn, record_type, value)
	for _, r := range records {
		query := `INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
			VALUES (?, 'A', ?, 300, 'system', 'system', TRUE, datetime('now'), datetime('now'))
			ON CONFLICT(fqdn, record_type, value) DO NOTHING`
		if _, err := rqlite.SafeExecContext(db, ctx, query, r.fqdn, r.value); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to ensure DNS record",
				zap.String("fqdn", r.fqdn), zap.Error(err))
		}
	}

	// Ensure SOA and NS records exist for the base domain (self-healing)
	if baseDomain != "" {
		n.ensureSOAAndNSRecords(ctx, baseDomain)
	}

	// Pin push.<baseDomain> to a single healthy nameserver (bugboard #858) so the
	// shared ntfy tier — which keeps no cross-node state — converges every
	// publisher AND subscriber onto one instance. Nameserver-only: they run Caddy
	// and already serve push.<baseDomain> on :443.
	if baseDomain != "" && n.isNameserverNode(ctx) {
		n.ensurePushDesignatedRecord(ctx, baseDomain)
	}

	// Claim an NS slot for the base domain (ns1/ns2/ns3) — only if this node
	// was installed with --nameserver (i.e. runs Caddy + CoreDNS).
	if baseDomain != "" && n.isNameserverPreference() {
		n.claimNameserverSlot(ctx, baseDomain, ipAddress)
	}

	return nil
}

// ensureSOAAndNSRecords creates SOA and NS records for the base domain if they don't exist.
// These are normally seeded during install Phase 7, but if that fails (e.g. migrations
// not yet run), the heartbeat self-heals them here.
func (n *Node) ensureSOAAndNSRecords(ctx context.Context, baseDomain string) {
	db := n.getRQLiteAdapter().GetSQLDB()
	fqdn := baseDomain + "."

	// Check if SOA exists
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dns_records WHERE fqdn = ? AND record_type = 'SOA'`, fqdn,
	).Scan(&count)
	if err != nil || count > 0 {
		return // SOA exists or query failed, skip
	}

	n.logger.ComponentInfo(logging.ComponentNode, "SOA/NS records missing, self-healing",
		zap.String("domain", baseDomain))

	// Create SOA record
	soaValue := fmt.Sprintf("ns1.%s. admin.%s. %d 3600 1800 604800 300",
		baseDomain, baseDomain, time.Now().Unix())
	if _, err := rqlite.SafeExecContext(db, ctx,
		`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
		VALUES (?, 'SOA', ?, 300, 'system', 'system', TRUE, datetime('now'), datetime('now'))
		ON CONFLICT(fqdn, record_type, value) DO NOTHING`,
		fqdn, soaValue,
	); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to create SOA record", zap.Error(err))
	}

	// Create NS records (ns1, ns2, ns3)
	for i := 1; i <= 3; i++ {
		nsValue := fmt.Sprintf("ns%d.%s.", i, baseDomain)
		if _, err := rqlite.SafeExecContext(db, ctx,
			`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
			VALUES (?, 'NS', ?, 300, 'system', 'system', TRUE, datetime('now'), datetime('now'))
			ON CONFLICT(fqdn, record_type, value) DO NOTHING`,
			fqdn, nsValue,
		); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to create NS record", zap.Error(err))
		}
	}
}

// ensurePushDesignatedRecord pins push.<baseDomain> to a single healthy
// nameserver node (bugboard #858), with failover. Thin wrapper over
// pinPushDesignated that logs failures.
func (n *Node) ensurePushDesignatedRecord(ctx context.Context, baseDomain string) {
	if _, err := pinPushDesignated(ctx, n.getRQLiteAdapter().GetSQLDB(), baseDomain); err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to pin push DNS record (bugboard #858)", zap.Error(err))
	}
}

// pinPushDesignated makes push.<baseDomain> resolve to exactly ONE healthy
// nameserver node — the lowest-IP active one (deterministic ordering, so every
// nameserver computes the same value with no leader election; it fails over when
// that node stops heartbeating).
//
// Why: the shared ntfy push tier keeps no cross-node shared state, so a publish
// and a long-lived subscriber that round-robin DNS sends to different instances
// never meet (bugboard #858 — measured 0/5 cross-node delivery). Concentrating
// push.<baseDomain> on one instance makes every publisher AND subscriber
// converge there. The specific A-record overrides the round-robin wildcard
// (exact match wins in DNS); the gateway fan-out still reaches that node, so
// there are no duplicates. Returns the designated IP, or "" if no healthy
// nameserver is resolvable (in which case the wildcard round-robin is left
// untouched — push still works via the gateway fan-out). Split out for testing.
func pinPushDesignated(ctx context.Context, db *sql.DB, baseDomain string) (string, error) {
	var designated string
	err := db.QueryRowContext(ctx, `
		SELECT ns.ip_address
		FROM dns_nameservers ns
		JOIN dns_nodes dn ON dn.ip_address = ns.ip_address
		WHERE ns.domain = ?
		  AND dn.status = 'active'
		  AND dn.last_seen > datetime('now', '-90 seconds')
		ORDER BY ns.ip_address ASC
		LIMIT 1`, baseDomain).Scan(&designated)
	if err == sql.ErrNoRows {
		return "", nil // no healthy nameserver; leave the wildcard round-robin
	}
	if err != nil {
		return "", fmt.Errorf("select designated push node: %w", err)
	}
	if designated == "" {
		return "", nil
	}

	fqdn := "push." + baseDomain + "."
	if _, err := rqlite.SafeExecContext(db, ctx,
		`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
		 VALUES (?, 'A', ?, 60, 'system', 'system', TRUE, datetime('now'), datetime('now'))
		 ON CONFLICT(fqdn, record_type, value) DO UPDATE SET is_active = TRUE, updated_at = datetime('now')`,
		fqdn, designated,
	); err != nil {
		return "", fmt.Errorf("pin push record: %w", err)
	}
	// Exactly one instance serves push: prune any stale push.<baseDomain> A-records
	// pointing elsewhere (this is also the failover step).
	if _, err := rqlite.SafeExecContext(db, ctx,
		`DELETE FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND value != ?`,
		fqdn, designated,
	); err != nil {
		return designated, fmt.Errorf("prune stale push records: %w", err)
	}
	return designated, nil
}

// claimNameserverSlot attempts to claim an available NS hostname (ns1/ns2/ns3) for this node.
// If the node already has a slot, it updates the IP. If no slot is available, it does nothing.
func (n *Node) claimNameserverSlot(ctx context.Context, domain, ipAddress string) {
	nodeID := n.GetPeerID()
	db := n.getRQLiteAdapter().GetSQLDB()

	// Check if this node already has a slot
	var existingHostname string
	err := db.QueryRowContext(ctx,
		`SELECT hostname FROM dns_nameservers WHERE node_id = ? AND domain = ?`,
		nodeID, domain,
	).Scan(&existingHostname)

	if err == nil {
		// Already claimed — update IP if changed
		if _, err := rqlite.SafeExecContext(db, ctx,
			`UPDATE dns_nameservers SET ip_address = ?, updated_at = datetime('now') WHERE hostname = ? AND domain = ?`,
			ipAddress, existingHostname, domain,
		); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to update NS slot IP", zap.Error(err))
		}
		// Ensure the glue A record matches
		nsFQDN := existingHostname + "." + domain + "."
		if _, err := rqlite.SafeExecContext(db, ctx,
			`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
			VALUES (?, 'A', ?, 300, 'system', 'system', TRUE, datetime('now'), datetime('now'))
			ON CONFLICT(fqdn, record_type, value) DO NOTHING`,
			nsFQDN, ipAddress,
		); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to ensure NS glue record", zap.Error(err))
		}
		return
	}

	// Try to claim an available slot
	for _, hostname := range []string{"ns1", "ns2", "ns3"} {
		result, err := rqlite.SafeExecContext(db, ctx,
			`INSERT INTO dns_nameservers (hostname, node_id, ip_address, domain) VALUES (?, ?, ?, ?)
			ON CONFLICT(hostname) DO NOTHING`,
			hostname, nodeID, ipAddress, domain,
		)
		if err != nil {
			continue
		}
		rows, _ := result.RowsAffected()
		if rows > 0 {
			// Successfully claimed this slot — create glue record
			nsFQDN := hostname + "." + domain + "."
			if _, err := rqlite.SafeExecContext(db, ctx,
				`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
				VALUES (?, 'A', ?, 300, 'system', 'system', TRUE, datetime('now'), datetime('now'))
				ON CONFLICT(fqdn, record_type, value) DO NOTHING`,
				nsFQDN, ipAddress,
			); err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "Failed to create NS glue record", zap.Error(err))
			}
			n.logger.ComponentInfo(logging.ComponentNode, "Claimed NS slot",
				zap.String("hostname", hostname),
				zap.String("ip", ipAddress),
			)
			return
		}
	}
}

// cleanupStaleNodeRecords removes A records for nodes that have stopped heartbeating.
// This ensures DNS only returns IPs for healthy, active nodes.
func (n *Node) cleanupStaleNodeRecords(ctx context.Context) {
	if n.getRQLiteAdapter() == nil {
		return
	}

	baseDomain := n.config.HTTPGateway.BaseDomain
	if baseDomain == "" {
		baseDomain = n.config.Node.Domain
	}
	if baseDomain == "" {
		return
	}

	db := n.getRQLiteAdapter().GetSQLDB()

	// Find nodes that haven't sent a heartbeat in over 2 minutes
	staleQuery := `SELECT id, ip_address FROM dns_nodes WHERE status = 'active' AND last_seen < datetime('now', '-120 seconds')`
	rows, err := db.QueryContext(ctx, staleQuery)
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "Failed to query stale nodes", zap.Error(err))
		return
	}
	defer rows.Close()

	// Build all FQDNs to clean: base domain + node domain
	var fqdnsToClean []string
	fqdnsToClean = append(fqdnsToClean, baseDomain+".", "*."+baseDomain+".")
	if n.config.Node.Domain != "" && n.config.Node.Domain != baseDomain {
		fqdnsToClean = append(fqdnsToClean, n.config.Node.Domain+".", "*."+n.config.Node.Domain+".")
	}

	for rows.Next() {
		var nodeID, ip string
		if err := rows.Scan(&nodeID, &ip); err != nil {
			continue
		}

		// Mark node as inactive
		if _, err := rqlite.SafeExecContext(db, ctx, `UPDATE dns_nodes SET status = 'inactive', updated_at = datetime('now') WHERE id = ?`, nodeID); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to mark node inactive", zap.String("node_id", nodeID), zap.Error(err))
		}

		// Remove the dead node's A records from round-robin
		for _, f := range fqdnsToClean {
			if _, err := rqlite.SafeExecContext(db, ctx, `DELETE FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND value = ? AND namespace = 'system'`, f, ip); err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "Failed to remove stale DNS record",
					zap.String("fqdn", f), zap.String("ip", ip), zap.Error(err))
			}
		}

		// Release any NS slot held by this dead node
		if _, err := rqlite.SafeExecContext(db, ctx, `DELETE FROM dns_nameservers WHERE node_id = ?`, nodeID); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to release NS slot", zap.String("node_id", nodeID), zap.Error(err))
		}

		// Remove glue records for this node's IP (ns1.domain., ns2.domain., ns3.domain.)
		for _, ns := range []string{"ns1", "ns2", "ns3"} {
			nsFQDN := ns + "." + baseDomain + "."
			if _, err := rqlite.SafeExecContext(db, ctx,
				`DELETE FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND value = ? AND namespace = 'system'`,
				nsFQDN, ip,
			); err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "Failed to remove NS glue record", zap.Error(err))
			}
		}

		n.logger.ComponentInfo(logging.ComponentNode, "Removed stale node from DNS",
			zap.String("node_id", nodeID),
			zap.String("ip", ip),
		)

		// Check if the dead node hosted any namespace services
		var nsCount int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT nc.namespace_name) FROM namespace_cluster_nodes ncn
			 JOIN namespace_clusters nc ON ncn.namespace_cluster_id = nc.id
			 WHERE ncn.node_id = ? AND ncn.status = 'running'`, nodeID,
		).Scan(&nsCount); err == nil && nsCount > 0 {
			n.logger.ComponentWarn(logging.ComponentNode,
				"Dead node hosted namespace services — reconciliation loop will repair",
				zap.String("node_id", nodeID),
				zap.String("ip", ip),
				zap.Int("affected_namespaces", nsCount),
			)
		}
	}
}

// inactiveNodeIPsSQL selects the public IPs of nodes that are no longer active.
// The NOT IN guard excludes any IP still held by an ACTIVE node, so if a departed
// node's public IP is later reused by a live node, that live node's records are
// preserved (bugboard #158 review).
// purgeStaleAfter is how long a node must have been silent before its DNS records
// are eligible for deletion. It is deliberately much longer than the 120s
// active→inactive threshold used by cleanupStaleNodeRecords: that flag flips on a
// transient blip (a rolling restart, a leaderless rqlite window), and deleting
// records is not something a blip should trigger. Only a node that has been quiet
// this long is treated as genuinely gone.
const purgeStaleAfter = 15 * time.Minute

// staleCutoff renders the "silent since" instant that the purge queries bind.
//
// Extracted so tests exercise the real computation against a real SQLite clock:
// every part of it is load-bearing and silently fails open if wrong. The format
// must match what the heartbeat writes into last_seen (datetime('now') /
// CURRENT_TIMESTAMP → 'YYYY-MM-DD HH:MM:SS', fixed width, so lexicographic
// comparison equals chronological). .UTC() is required because those writers store
// UTC — on a UTC+N host a local-time cutoff would sit N hours in the future, making
// `last_seen < cutoff` true for every node and defeating the gate entirely.
func staleCutoff() string {
	return time.Now().UTC().Add(-purgeStaleAfter).Format("2006-01-02 15:04:05")
}

// inactiveNodeIPsSQL selects the public IPs of nodes that are genuinely gone:
// non-active AND silent since the caller-supplied cutoff (one `?`).
//
// The cutoff is computed in Go and bound as a parameter rather than written as
// datetime('now', …). rqlite replicates the statement and each node applies it
// locally, so a non-deterministic expression in a DELETE *predicate* could match
// different rows on different nodes and diverge the cluster. A bound constant
// travels identically to every replica.
//
// The IS NOT NULL / != ” filter is explicit rather than incidental: a single NULL
// ip_address would make the outer `value NOT IN (…)` evaluate to NULL for every
// row (SQL three-valued logic), silently turning the whole purge into a no-op.
const inactiveNodeIPsSQL = `SELECT ip_address FROM dns_nodes WHERE status != 'active'
	           AND ip_address IS NOT NULL AND ip_address != ''
	           AND last_seen < ?
	           AND ip_address NOT IN (SELECT ip_address FROM dns_nodes
	               WHERE status = 'active' AND ip_address IS NOT NULL AND ip_address != '')`

// purgeInactiveTURNRecordsSQL removes namespace TURN/stealth A records whose
// value is the IP of a non-active node (bugboard #158).
//
// Deliberately NOT guarded against emptying the fqdn, unlike the namespace-host
// purge below. The rationale is recoverability, not resolution behavior:
// EnsureTURNRecordForNode re-adds each live TURN node's own record on every boot,
// so an over-purged turn host repopulates itself, and a relay that resolves
// nowhere is no worse for the client than one that resolves to a dead node —
// either way the ICE agent falls through to the next server. This is the exact
// query already verified in production; the namespace host gets the extra guard
// because its blast radius (all RPC + the signaling WebSocket) is far larger.
const purgeInactiveTURNRecordsSQL = `DELETE FROM dns_records
	 WHERE record_type = 'A'
	   AND (namespace LIKE 'namespace-turn:%' OR namespace LIKE 'namespace-turn-stealth:%')
	   AND value IN (
	       ` + inactiveNodeIPsSQL + `
	   )`

// purgeInactiveNamespaceHostRecordsSQL removes namespace GATEWAY-host A records
// (`ns-<ns>` and its `*.ns-<ns>` wildcard, tag `namespace:<ns>`) whose value is
// the IP of a non-active node.
//
// This host is the origin for the persistent signaling WebSocket and every RPC,
// and the resolver round-robins across its A records — so one dead IP among three
// meant roughly a third of connections landing on a node that refuses :443
// (devnet: 109.123.239.61 on ns-anchat-test). The original #158 purge covered only
// the `namespace-turn*` tags, leaving this host stale.
//
// SAFETY — the EXISTS guard: a dead-IP row is deleted only when the SAME fqdn
// still has at least one RESOLVABLE A record that is NOT a dead IP. Without it, a
// cluster-wide heartbeat failure that flipped every node to non-active would
// delete every A record for the namespace host.
//
// Note what emptying it would actually cause — it is NOT a clean NXDOMAIN. The
// resolver rewrites a 3-label miss to the base wildcard (getWildcardName in
// pkg/coredns/rqlite/plugin.go), and `*.<base>` DOES exist (ensureBaseDNSRecords),
// so `ns-<ns>.<base>` would silently fall through to the nameserver nodes — which
// may not host this namespace at all. Clients would connect, pass TLS on the
// wildcard cert, and hit the wrong backend: a silent misroute is harder to
// diagnose than an outright failure, which makes this guard more important, not
// less.
//
// "Resolvable" means is_active = TRUE, matching the resolver's own predicate
// (pkg/coredns/rqlite/backend.go). A soft-disabled row must NOT count as a
// survivor: recovery's suspect-node path (DisableNamespaceRecord) sets
// is_active = FALSE on exactly these fqdns, so counting dark rows would let the
// purge delete the last row that actually answers queries. For the same reason a
// dark row is never itself deleted — removing it changes nothing observable, and
// keeping it preserves recovery's ability to re-enable it.
//
// Survivors are matched by fqdn, not by tag: any surviving resolvable A record
// for the name means the name still resolves.
//
// Recoverability: paired with EnsureNamespaceHostRecordForNode, which re-adds
// each live gateway node's own record on every boot. That counterpart is what
// makes deleting these rows safe at all — a node flapped non-active transiently
// re-advertises itself instead of being erased permanently.
const purgeInactiveNamespaceHostRecordsSQL = `DELETE FROM dns_records
	 WHERE record_type = 'A'
	   AND namespace LIKE 'namespace:%'
	   AND is_active = TRUE
	   AND value IN (
	       ` + inactiveNodeIPsSQL + `
	   )
	   AND EXISTS (
	       SELECT 1 FROM dns_records AS survivor
	        WHERE survivor.fqdn = dns_records.fqdn
	          AND survivor.record_type = 'A'
	          AND survivor.is_active = TRUE
	          AND survivor.value NOT IN (
	              ` + inactiveNodeIPsSQL + `
	          )
	   )`

// ensureNamespaceHostRecordsSQL re-advertises THIS node in the gateway
// round-robin for every namespace it currently serves as a running gateway.
//
// One statement covers all such namespaces: it derives the fqdn from
// namespace_clusters.namespace_name and inserts only when an identical
// (fqdn, A, value, tag) row is absent. `?` order:
//
//	1 prefix, 2 baseDomain, 3 nodeIP, 4 now, 5 now,      -- projection
//	6 nodeID,                                            -- which namespaces
//	7 prefix, 8 baseDomain, 9 nodeIP                     -- NOT EXISTS guard
//
// is_active is left to its TRUE default and never forced: a row recovery
// deliberately disabled (DisableNamespaceRecord) blocks the insert via NOT EXISTS
// and stays dark, so this can never resurrect traffic to a drained node.
//
// ON CONFLICT DO NOTHING is a required backstop, not decoration. The NOT EXISTS
// guard matches on four columns INCLUDING the namespace tag, but the table's
// constraint is UNIQUE(fqdn, record_type, value) with no tag — so a row carrying
// the same (fqdn, A, ip) under a different tag slips past the guard and hits the
// constraint. Since this is ONE statement covering EVERY namespace this node
// gateways, that would abort the whole insert and silently stop re-advertisement
// for all of them, every 30s — exactly the failure this function exists to fix.
// Same clause as the sibling self-heal in ensureBaseDNSRecords.
const ensureNamespaceHostRecordsSQL = `INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, created_at, updated_at)
	SELECT ?||'ns-'||nc.namespace_name||'.'||?||'.', 'A', ?, 60,
	       'namespace:'||nc.namespace_name, 'namespace-dns-reconcile', ?, ?
	  FROM namespace_cluster_nodes ncn
	  JOIN namespace_clusters nc ON ncn.namespace_cluster_id = nc.id
	 WHERE ncn.node_id = ? AND ncn.role = 'gateway' AND ncn.status = 'running'
	   AND NOT EXISTS (
	       SELECT 1 FROM dns_records r
	        WHERE r.fqdn = ?||'ns-'||nc.namespace_name||'.'||?||'.'
	          AND r.record_type = 'A' AND r.value = ?
	          AND r.namespace = 'namespace:'||nc.namespace_name
	   )
	ON CONFLICT(fqdn, record_type, value) DO NOTHING`

// ensureNamespaceHostRecords re-advertises this node in the `ns-<ns>` (and
// `*.ns-<ns>`) round-robin for every namespace it gateways.
//
// Runs on the 30s DNS sweep, immediately AFTER updateDNSHeartbeat has re-asserted
// status='active' and refreshed last_seen — the ordering matters, because it means
// a node that just recovered is no longer purge-eligible by the time it
// re-advertises. Making this periodic rather than boot-only is what keeps the
// purge safe: purge and re-add now run at the same cadence, so a node whose record
// was removed while it was genuinely gone rejoins the round-robin ~30s after it
// comes back, instead of staying absent until its next process restart (which is
// how a healthy node silently sat out of rotation on devnet).
//
// Additive and per-node: inserts only THIS node's own value, only when absent.
func (n *Node) ensureNamespaceHostRecords(ctx context.Context) {
	if n.getRQLiteAdapter() == nil {
		return
	}
	nodeID := n.GetPeerID()
	if nodeID == "" {
		return
	}
	// Deliberately NO fallback to Node.Domain. Provisioning derives the namespace
	// host solely from HTTPGateway.BaseDomain (CreateNamespaceRecords), so falling
	// back to this node's own per-node domain would fabricate `ns-<ns>.<node
	// domain>.` records — a different name on every node, resolving nothing and
	// round-robining nothing. Better to advertise nothing than to invent a name.
	baseDomain := n.config.HTTPGateway.BaseDomain
	if baseDomain == "" {
		return
	}
	ip, err := n.getNodeIPAddress()
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode,
			"Cannot re-advertise namespace host DNS records: this node's public IP is unavailable",
			zap.Error(err))
		return
	}

	db := n.getRQLiteAdapter().GetSQLDB()
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	// The apex host and the deployment wildcard are always advertised together.
	for _, prefix := range []string{"", "*."} {
		res, err := rqlite.SafeExecContext(db, ctx, ensureNamespaceHostRecordsSQL,
			prefix, baseDomain, ip, now, now, nodeID, prefix, baseDomain, ip)
		if err != nil {
			n.logger.ComponentWarn(logging.ComponentNode,
				"Failed to ensure namespace host DNS records",
				zap.String("prefix", prefix), zap.Error(err))
			continue
		}
		if res != nil {
			if affected, _ := res.RowsAffected(); affected > 0 {
				n.logger.ComponentInfo(logging.ComponentNode,
					"Re-advertised this node in the namespace gateway round-robin",
					zap.String("prefix", prefix), zap.String("ip", ip),
					zap.Int64("records_added", affected))
			}
		}
	}
}

// retractForeignTURNRecordsSQL removes THIS node's own TURN/stealth A records for
// any namespace it does not actually serve as a TURN node.
//
// `webrtc_port_allocations` (service_type='turn') is the authoritative record of
// which nodes run TURN for a namespace. The DNS A records, by contrast, are written
// once at webrtc-enable time (CreateTURNRecords) and never reconciled against it —
// so when a node is removed from a namespace's cluster, its TURN records linger and
// clients keep round-robining onto a host that no longer answers. That is NOT
// something the inactive-node purge can catch: the node is alive and heartbeating,
// it simply is not a TURN node for that namespace (observed on devnet: 57.129.7.232
// advertised on turn-anchat-test with no TURN allocation, so ~50% of relay
// connections hit a dead endpoint).
//
// Per-node and self-limited: `value = ?` is this node's own IP, so a node can only
// ever retract ITSELF. It can never delete a peer's record, which makes it safe to
// run unguarded on every node — the same model as the ensure. `?` order:
// 1 nodeIP, 2 nodeID.
const retractForeignTURNRecordsSQL = `DELETE FROM dns_records
	 WHERE record_type = 'A'
	   AND value = ?
	   AND (namespace LIKE 'namespace-turn:%' OR namespace LIKE 'namespace-turn-stealth:%')
	   AND NOT EXISTS (
	       SELECT 1 FROM webrtc_port_allocations wpa
	         JOIN namespace_clusters nc ON wpa.namespace_cluster_id = nc.id
	        WHERE wpa.service_type = 'turn'
	          AND wpa.node_id = ?
	          AND dns_records.namespace IN ('namespace-turn:'||nc.namespace_name,
	                                        'namespace-turn-stealth:'||nc.namespace_name)
	   )`

// retractForeignTURNRecords drops this node's TURN/stealth A records for any
// namespace it holds no TURN allocation for. See retractForeignTURNRecordsSQL.
func (n *Node) retractForeignTURNRecords(ctx context.Context) {
	if n.getRQLiteAdapter() == nil {
		return
	}
	nodeID := n.GetPeerID()
	if nodeID == "" {
		return
	}
	ip, err := n.getNodeIPAddress()
	if err != nil {
		return // already logged by the ensure on the same sweep
	}
	res, err := rqlite.SafeExecContext(n.getRQLiteAdapter().GetSQLDB(), ctx,
		retractForeignTURNRecordsSQL, ip, nodeID)
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode,
			"Failed to retract stale TURN DNS records for this node", zap.Error(err))
		return
	}
	if res != nil {
		if affected, _ := res.RowsAffected(); affected > 0 {
			n.logger.ComponentInfo(logging.ComponentNode,
				"Retracted own TURN DNS records for namespaces this node no longer serves as a TURN node",
				zap.String("ip", ip), zap.Int64("records_removed", affected))
		}
	}
}

// purgeInactiveNodeRecords deletes per-namespace A records (namespace gateway
// host, TURN, stealth-TURN) whose value is the IP of a node that is no longer
// active (bugboard #158).
//
// These records are written once at provision / WebRTC-enable time
// (dns_manager.go: CreateNamespaceRecords / CreateTURNRecords /
// CreateStealthTURNRecords) and are never reconciled on topology change, unlike
// the system round-robin records that cleanupStaleNodeRecords handles. So when a
// node is removed/replaced its A records linger, and the resolver keeps handing
// clients a dead IP:
//   - `turn.ns-<ns>` → zero relay candidates, the call hangs on "connecting"
//   - `ns-<ns>` → the signaling WebSocket and every RPC hit a node that refuses
//     :443, so roughly 1-in-N connections fail and get retried
//
// cleanupStaleNodeRecords only fires at the active→inactive transition and only
// touches `namespace='system'` rows, so an already-inactive node's per-namespace
// records are never cleaned. This purge closes that gap for ALL currently-inactive
// nodes, while the EXISTS guard in the SQL guarantees it can never empty an fqdn.
// Idempotent DELETE-by-value — safe to run from every node on every sweep (same
// model as cleanupStaleNodeRecords, no leader gating needed).
func (n *Node) purgeInactiveNodeRecords(ctx context.Context) {
	if n.getRQLiteAdapter() == nil {
		return
	}
	db := n.getRQLiteAdapter().GetSQLDB()

	cutoff := staleCutoff()

	// Two independent statements. The TURN purge is unguarded (for a relay-only
	// client NXDOMAIN fails instantly whereas a dead A record burns a timeout);
	// the namespace-host purge never empties an fqdn. `args` repeats the cutoff
	// once per occurrence of inactiveNodeIPsSQL in each statement.
	for _, p := range []struct {
		what string
		sql  string
		args []interface{}
	}{
		{"TURN/stealth", purgeInactiveTURNRecordsSQL, []interface{}{cutoff}},
		{"namespace host", purgeInactiveNamespaceHostRecordsSQL, []interface{}{cutoff, cutoff}},
	} {
		res, err := rqlite.SafeExecContext(db, ctx, p.sql, p.args...)
		if err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to purge inactive-node DNS records",
				zap.String("record_set", p.what), zap.Error(err))
			continue
		}
		if res != nil {
			if affected, _ := res.RowsAffected(); affected > 0 {
				n.logger.ComponentInfo(logging.ComponentNode, "Purged DNS records pointing at inactive nodes",
					zap.String("record_set", p.what), zap.Int64("records_removed", affected))
			}
		}
	}
}

// isNameserverPreference checks if this node was installed with --nameserver flag
// by reading the preferences.yaml file. Only nameserver nodes should claim NS slots.
func (n *Node) isNameserverPreference() bool {
	oramaDir := filepath.Join(os.ExpandEnv(n.config.Node.DataDir), "..")
	prefsPath := filepath.Join(oramaDir, "preferences.yaml")
	data, err := os.ReadFile(prefsPath)
	if err != nil {
		return false
	}
	// Simple check: look for "nameserver: true" in the YAML
	return strings.Contains(string(data), "nameserver: true")
}

// isNameserverNode checks if this node has claimed a nameserver slot (ns1/ns2/ns3).
// Only nameserver nodes claim NS slots, so only they should be in base domain DNS.
func (n *Node) isNameserverNode(ctx context.Context) bool {
	if n.getRQLiteAdapter() == nil {
		return false
	}
	nodeID := n.GetPeerID()
	if nodeID == "" {
		return false
	}
	db := n.getRQLiteAdapter().GetSQLDB()
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dns_nameservers WHERE node_id = ?`, nodeID,
	).Scan(&count)
	return err == nil && count > 0
}

// getWireGuardIP returns the IPv4 address assigned to the wg0 interface, if any
func (n *Node) getWireGuardIP() (string, error) {
	return wireguard.GetIP()
}

// getNodeIPAddress attempts to determine the node's external IP address
func (n *Node) getNodeIPAddress() (string, error) {
	// Try to detect external IP by connecting to a public server
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		// If that fails, try to get first non-loopback interface IP
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "", err
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && !ipnet.IP.IsPrivate() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String(), nil
				}
			}
		}

		return "", fmt.Errorf("no suitable IP address found")
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	if localAddr.IP.IsPrivate() || localAddr.IP.IsLoopback() {
		// UDP dial returned a private/loopback IP (e.g. WireGuard 10.0.0.x).
		// Fall back to scanning interfaces for a public IPv4.
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "", fmt.Errorf("private IP detected (%s) and failed to list interfaces: %w", localAddr.IP, err)
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && !ipnet.IP.IsPrivate() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String(), nil
				}
			}
		}
		return "", fmt.Errorf("private IP detected (%s) and no public IPv4 found on interfaces", localAddr.IP)
	}
	return localAddr.IP.String(), nil
}

// cleanupPrivateIPRecords deletes any A records with private/loopback IPs from dns_records.
// Old code versions could insert WireGuard IPs (10.0.0.x) into the table. This runs on
// every heartbeat to self-heal.
func cleanupPrivateIPRecords(ctx context.Context, db *sql.DB, logger *logging.ColoredLogger) {
	query := `DELETE FROM dns_records WHERE record_type = 'A' AND namespace = 'system'
		AND (value LIKE '10.%' OR value LIKE '172.16.%' OR value LIKE '172.17.%' OR value LIKE '172.18.%'
		OR value LIKE '172.19.%' OR value LIKE '172.2_.%' OR value LIKE '172.30.%' OR value LIKE '172.31.%'
		OR value LIKE '192.168.%' OR value = '127.0.0.1')`
	result, err := rqlite.SafeExecContext(db, ctx, query)
	if err != nil {
		logger.ComponentWarn(logging.ComponentNode, "Failed to clean up private IP DNS records", zap.Error(err))
		return
	}
	if rows, _ := result.RowsAffected(); rows > 0 {
		logger.ComponentInfo(logging.ComponentNode, "Cleaned up private IP DNS records",
			zap.Int64("deleted", rows))
	}
}

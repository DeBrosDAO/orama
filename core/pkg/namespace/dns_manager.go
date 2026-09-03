package namespace

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
)

// DNSRecordManager manages DNS records for namespace clusters.
// It creates and deletes DNS A records for namespace gateway endpoints.
type DNSRecordManager struct {
	db         rqlite.Client
	baseDomain string
	logger     *zap.Logger
}

// NewDNSRecordManager creates a new DNS record manager
func NewDNSRecordManager(db rqlite.Client, baseDomain string, logger *zap.Logger) *DNSRecordManager {
	return &DNSRecordManager{
		db:         db,
		baseDomain: baseDomain,
		logger:     logger.With(zap.String("component", "dns-record-manager")),
	}
}

// CreateNamespaceRecords creates DNS A records for a namespace cluster.
// Each namespace gets records for ns-{namespace}.{baseDomain} pointing to its gateway nodes.
// Multiple A records enable round-robin DNS load balancing.
func (drm *DNSRecordManager) CreateNamespaceRecords(ctx context.Context, namespaceName string, nodeIPs []string) error {
	internalCtx := client.WithInternalAuth(ctx)

	if len(nodeIPs) == 0 {
		return &ClusterError{Message: "no node IPs provided for DNS records"}
	}

	// FQDN for namespace gateway: ns-{namespace}.{baseDomain}.
	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)

	drm.logger.Info("Creating namespace DNS records",
		zap.String("namespace", namespaceName),
		zap.String("fqdn", fqdn),
		zap.Strings("node_ips", nodeIPs),
	)

	// First, delete any existing records for this namespace
	deleteQuery := `DELETE FROM dns_records WHERE fqdn = ? AND namespace = ?`
	_, err := drm.db.Exec(internalCtx, deleteQuery, fqdn, "namespace:"+namespaceName)
	if err != nil {
		drm.logger.Warn("Failed to delete existing DNS records", zap.Error(err))
		// Continue anyway - the insert will just add more records
	}

	// Create A records for each node IP
	for _, ip := range nodeIPs {
		insertQuery := `
			INSERT INTO dns_records (
				fqdn, record_type, value, ttl, namespace, created_by, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`
		now := time.Now()
		_, err := drm.db.Exec(internalCtx, insertQuery,
			fqdn, "A", ip, 60,
			"namespace:"+namespaceName, "cluster-manager",
			now, now,
		)
		if err != nil {
			return &ClusterError{
				Message: fmt.Sprintf("failed to create DNS record for %s -> %s", fqdn, ip),
				Cause:   err,
			}
		}
	}

	// Also create wildcard records for deployments under this namespace
	// *.ns-{namespace}.{baseDomain} -> same IPs
	wildcardFqdn := fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain)

	// Delete existing wildcard records
	_, _ = drm.db.Exec(internalCtx, deleteQuery, wildcardFqdn, "namespace:"+namespaceName)

	for _, ip := range nodeIPs {
		insertQuery := `
			INSERT INTO dns_records (
				fqdn, record_type, value, ttl, namespace, created_by, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`
		now := time.Now()
		_, err := drm.db.Exec(internalCtx, insertQuery,
			wildcardFqdn, "A", ip, 60,
			"namespace:"+namespaceName, "cluster-manager",
			now, now,
		)
		if err != nil {
			drm.logger.Warn("Failed to create wildcard DNS record",
				zap.String("fqdn", wildcardFqdn),
				zap.String("ip", ip),
				zap.Error(err),
			)
			// Continue - wildcard is nice to have but not critical
		}
	}

	drm.logger.Info("Namespace DNS records created",
		zap.String("namespace", namespaceName),
		zap.Int("record_count", len(nodeIPs)*2), // A + wildcard
	)

	return nil
}

// DeleteNamespaceRecords deletes all DNS records for a namespace
func (drm *DNSRecordManager) DeleteNamespaceRecords(ctx context.Context, namespaceName string) error {
	internalCtx := client.WithInternalAuth(ctx)

	drm.logger.Info("Deleting namespace DNS records",
		zap.String("namespace", namespaceName),
	)

	// Delete all records owned by this namespace
	deleteQuery := `DELETE FROM dns_records WHERE namespace = ?`
	_, err := drm.db.Exec(internalCtx, deleteQuery, "namespace:"+namespaceName)
	if err != nil {
		return &ClusterError{
			Message: "failed to delete namespace DNS records",
			Cause:   err,
		}
	}

	drm.logger.Info("Namespace DNS records deleted",
		zap.String("namespace", namespaceName),
	)

	return nil
}

// GetNamespaceGatewayIPs returns the IP addresses for a namespace's gateway
func (drm *DNSRecordManager) GetNamespaceGatewayIPs(ctx context.Context, namespaceName string) ([]string, error) {
	internalCtx := client.WithInternalAuth(ctx)

	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)

	type recordRow struct {
		Value string `db:"value"`
	}

	var records []recordRow
	query := `SELECT value FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND is_active = TRUE`
	err := drm.db.Query(internalCtx, &records, query, fqdn)
	if err != nil {
		return nil, &ClusterError{
			Message: "failed to query namespace DNS records",
			Cause:   err,
		}
	}

	ips := make([]string, len(records))
	for i, r := range records {
		ips[i] = r.Value
	}

	return ips, nil
}

// AddNamespaceRecord adds DNS A records for a single IP to an existing namespace.
// Unlike CreateNamespaceRecords, this does NOT delete existing records — it's purely additive.
// Used when adding a new node to an under-provisioned cluster (repair).
func (drm *DNSRecordManager) AddNamespaceRecord(ctx context.Context, namespaceName, ip string) error {
	internalCtx := client.WithInternalAuth(ctx)

	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)
	wildcardFqdn := fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain)

	drm.logger.Info("Adding DNS record for namespace",
		zap.String("namespace", namespaceName),
		zap.String("ip", ip),
	)

	now := time.Now()
	for _, f := range []string{fqdn, wildcardFqdn} {
		insertQuery := `
			INSERT INTO dns_records (
				fqdn, record_type, value, ttl, namespace, created_by, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`
		_, err := drm.db.Exec(internalCtx, insertQuery,
			f, "A", ip, 60,
			"namespace:"+namespaceName, "cluster-manager", now, now,
		)
		if err != nil {
			return &ClusterError{
				Message: fmt.Sprintf("failed to add DNS record %s -> %s", f, ip),
				Cause:   err,
			}
		}
	}

	drm.logger.Info("DNS records added for namespace",
		zap.String("namespace", namespaceName),
		zap.String("ip", ip),
	)

	return nil
}

// UpdateNamespaceRecord updates a specific node's DNS record (for failover)
func (drm *DNSRecordManager) UpdateNamespaceRecord(ctx context.Context, namespaceName, oldIP, newIP string) error {
	internalCtx := client.WithInternalAuth(ctx)

	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)
	wildcardFqdn := fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain)

	drm.logger.Info("Updating namespace DNS record",
		zap.String("namespace", namespaceName),
		zap.String("old_ip", oldIP),
		zap.String("new_ip", newIP),
	)

	// Update both the main record and wildcard record.
	//
	// Upsert semantics: the old row may legitimately be gone by the time failover
	// re-points DNS — the #158 sweep purges records for non-active nodes, and
	// HandleDeadNode marks the node offline minutes before it reaches this call.
	// A plain UPDATE would then match zero rows and return nil, reporting success
	// while the replacement node silently never entered the round-robin. Fall back
	// to an additive insert so the new IP is advertised either way.
	for _, f := range []string{fqdn, wildcardFqdn} {
		updateQuery := `UPDATE dns_records SET value = ?, is_active = 1, updated_at = ? WHERE fqdn = ? AND value = ?`
		res, err := drm.db.Exec(internalCtx, updateQuery, newIP, time.Now(), f, oldIP)
		if err != nil {
			drm.logger.Warn("Failed to update DNS record",
				zap.String("fqdn", f),
				zap.Error(err),
			)
			continue
		}
		if res != nil {
			if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
				now := time.Now()
				tag := "namespace:" + namespaceName
				if _, ierr := drm.db.Exec(internalCtx, ensureNamespaceHostRecordSQL,
					f, newIP, tag, now, now, f, newIP, tag); ierr != nil {
					drm.logger.Warn("Failed to insert replacement DNS record after a no-op update",
						zap.String("fqdn", f), zap.String("new_ip", newIP), zap.Error(ierr))
					continue
				}
				drm.logger.Info("Old DNS record was already gone — inserted the replacement additively",
					zap.String("fqdn", f), zap.String("new_ip", newIP))
			}
		}
	}

	return nil
}

// DisableNamespaceRecord marks a specific IP's record as inactive (for temporary failover)
func (drm *DNSRecordManager) DisableNamespaceRecord(ctx context.Context, namespaceName, ip string) (int64, error) {
	internalCtx := client.WithInternalAuth(ctx)

	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)
	wildcardFqdn := fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain)

	var disabled int64
	for _, f := range []string{fqdn, wildcardFqdn} {
		// The "never disable the last active record" guard is INSIDE the
		// statement.
		//
		// It used to be a separate COUNT followed by an unconditional UPDATE.
		// Every node that observes a suspect node runs this, so two observers
		// could both read a count of 2, both conclude they were not the last,
		// and both disable — leaving the namespace resolving nowhere. The
		// window is small and the consequence is a total outage for that
		// namespace, which is the worst trade a race can offer.
		//
		// Each name is guarded on its OWN count. Guarding the wildcard on the
		// primary's count would let the last wildcard record go while the
		// primary still had two.
		res, err := drm.db.Exec(internalCtx, `
			UPDATE dns_records SET is_active = FALSE, updated_at = ?
			 WHERE fqdn = ? AND value = ? AND is_active = TRUE
			   AND (SELECT COUNT(*) FROM dns_records d2
			         WHERE d2.fqdn = dns_records.fqdn
			           AND d2.record_type = 'A'
			           AND d2.is_active = TRUE) > 1`,
			time.Now(), f, ip)
		if err != nil {
			// This used to be `_, _ =` and the function always returned nil, so
			// a failure to withdraw a dead node's record was invisible.
			return disabled, &ClusterError{
				Message: fmt.Sprintf("failed to disable DNS record %s for %s", f, ip),
				Cause:   err,
			}
		}
		if res != nil {
			if n, err := res.RowsAffected(); err == nil {
				disabled += n
			}
		}
	}

	drm.logger.Info("Disabled namespace DNS records",
		zap.String("namespace", namespaceName),
		zap.String("ip", ip),
		zap.Int64("records_disabled", disabled))

	return disabled, nil
}

// CreateTURNRecords creates DNS A records for TURN servers.
// TURN records follow the pattern: turn.ns-{namespace}.{baseDomain} -> TURN node IPs
func (drm *DNSRecordManager) CreateTURNRecords(ctx context.Context, namespaceName string, turnIPs []string) error {
	internalCtx := client.WithInternalAuth(ctx)

	if len(turnIPs) == 0 {
		return &ClusterError{Message: "no TURN IPs provided for DNS records"}
	}

	// Two round-robin hostnames point at the same TURN nodes:
	//   - turn.ns-<ns>.<base>  : legacy host, used for plain UDP/TCP TURN (:3478)
	//   - turn-<ns>.<base>     : single-label TLS host, used for turns:(:5349);
	//                            covered by the *.<base> wildcard cert so it
	//                            validates in browsers (the legacy two-label host
	//                            can't get a CA-valid cert).
	fqdn := fmt.Sprintf("turn.ns-%s.%s.", namespaceName, drm.baseDomain)
	tlsFQDN := turn.TLSHostForNamespace(namespaceName, drm.baseDomain) + "."

	drm.logger.Info("Creating TURN DNS records",
		zap.String("namespace", namespaceName),
		zap.String("fqdn", fqdn),
		zap.String("tls_fqdn", tlsFQDN),
		zap.Strings("turn_ips", turnIPs),
	)

	tag := "namespace-turn:" + namespaceName
	now := time.Now()
	for _, host := range []string{fqdn, tlsFQDN} {
		// Delete existing records for this host+namespace, then recreate.
		deleteQuery := `DELETE FROM dns_records WHERE fqdn = ? AND namespace = ?`
		_, _ = drm.db.Exec(internalCtx, deleteQuery, host, tag)

		for _, ip := range turnIPs {
			insertQuery := `
				INSERT INTO dns_records (
					fqdn, record_type, value, ttl, namespace, created_by, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`
			_, err := drm.db.Exec(internalCtx, insertQuery,
				host, "A", ip, 60, tag, "cluster-manager", now, now,
			)
			if err != nil {
				return &ClusterError{
					Message: fmt.Sprintf("failed to create TURN DNS record %s -> %s", host, ip),
					Cause:   err,
				}
			}
		}
	}

	drm.logger.Info("TURN DNS records created",
		zap.String("namespace", namespaceName),
		zap.Int("record_count", len(turnIPs)*2),
	)

	return nil
}

// ensureTURNRecordSQL additively inserts one node's TURN A record only if an
// identical (fqdn, value, namespace-tag) row is absent (bugboard #158).
const ensureTURNRecordSQL = `INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, created_at, updated_at)
	SELECT ?, 'A', ?, 60, ?, 'turn-dns-reconcile', ?, ?
	WHERE NOT EXISTS (
	    SELECT 1 FROM dns_records
	    WHERE fqdn = ? AND record_type = 'A' AND value = ? AND namespace = ?
	)`

// EnsureTURNRecordForNode additively upserts THIS node's TURN (and stealth) A
// records so a live TURN node always advertises itself (bugboard #158).
//
// CreateTURNRecords is a one-shot DELETE+INSERT run only at WebRTC-enable time,
// and the #158 sweep only DELETES records for inactive nodes — so a TURN node
// whose record was purged while it was briefly down (e.g. mid-deploy) never got
// re-added, leaving the turn domain empty (NXDOMAIN → clients resolve no TURN
// server → zero relay candidates). This is the re-add counterpart: each TURN
// node ensures its own A record exists on every boot.
//
// Per-node and additive: it inserts ONLY this node's value and only if absent,
// so it never touches other TURN nodes' records — safe to run unguarded on
// every node (no leader gating), like the sweep.
func (drm *DNSRecordManager) EnsureTURNRecordForNode(ctx context.Context, namespaceName, nodeIP, stealthHost string) error {
	if nodeIP == "" {
		return nil
	}
	internalCtx := client.WithInternalAuth(ctx)
	now := time.Now()

	ensure := func(fqdn, tag string) error {
		// Read before write. The INSERT is already idempotent, but in rqlite an
		// Exec that inserts ZERO rows is still a replicated, fsync'd Raft entry —
		// and this runs on a 60s reconcile tick from every TURN node, for every
		// namespace, three statements each. In the steady state that is a
		// permanent stream of no-op log entries that only grows the Raft log and
		// snapshots. A leader-routed SELECT is far cheaper than a Raft round, so
		// only write when the row is genuinely missing.
		var existing []struct {
			N int `db:"n"`
		}
		if err := drm.db.Query(internalCtx, &existing,
			`SELECT COUNT(*) AS n FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND value = ? AND namespace = ?`,
			fqdn, nodeIP, tag); err == nil && len(existing) > 0 && existing[0].N > 0 {
			return nil
		}
		// Absent, or the check failed — fall through to the guarded INSERT, which
		// is still a no-op if the row turns out to exist.
		_, err := drm.db.Exec(internalCtx, ensureTURNRecordSQL, fqdn, nodeIP, tag, now, now, fqdn, nodeIP, tag)
		return err
	}

	turnFQDN := fmt.Sprintf("turn.ns-%s.%s.", namespaceName, drm.baseDomain)
	if err := ensure(turnFQDN, "namespace-turn:"+namespaceName); err != nil {
		return &ClusterError{Message: fmt.Sprintf("ensure TURN A record %s -> %s", turnFQDN, nodeIP), Cause: err}
	}
	// Single-label TLS host (turn-<ns>.<base>) for the `turns:…:5349` URI. It is
	// covered by the `*.<base>` wildcard cert, so TURNS validates in browsers —
	// unlike the two-label turnFQDN above, which is only used for plain UDP/TCP
	// TURN now. Same namespace tag/round-robin semantics as turnFQDN.
	tlsFQDN := turn.TLSHostForNamespace(namespaceName, drm.baseDomain) + "."
	if err := ensure(tlsFQDN, "namespace-turn:"+namespaceName); err != nil {
		return &ClusterError{Message: fmt.Sprintf("ensure TURNS A record %s -> %s", tlsFQDN, nodeIP), Cause: err}
	}
	if stealthHost != "" {
		if err := ensure(stealthHost+".", stealthDNSNamespace(namespaceName)); err != nil {
			return &ClusterError{Message: fmt.Sprintf("ensure stealth TURN A record %s -> %s", stealthHost, nodeIP), Cause: err}
		}
	}
	return nil
}

// ensureNamespaceHostRecordSQL additively inserts one node's namespace-host A
// record only if an identical (fqdn, value, namespace-tag) row is absent. Kept
// separate from ensureTURNRecordSQL so each carries its own created_by
// provenance (and so the production-verified TURN query stays untouched).
//
// is_active is intentionally NOT set here: the column defaults to TRUE for a new
// row, and an existing row that recovery deliberately soft-disabled
// (DisableNamespaceRecord, used by the suspect-node path) must stay disabled —
// re-enabling it would fight the health monitor.
const ensureNamespaceHostRecordSQL = `INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, created_at, updated_at)
	SELECT ?, 'A', ?, 60, ?, 'namespace-dns-reconcile', ?, ?
	WHERE NOT EXISTS (
	    SELECT 1 FROM dns_records
	    WHERE fqdn = ? AND record_type = 'A' AND value = ? AND namespace = ?
	)`

// EnsureNamespaceHostRecordForNode additively upserts THIS node's A records for
// the namespace gateway host (`ns-<ns>` and its `*.ns-<ns>` deployment wildcard).
//
// This is the missing counterpart to EnsureTURNRecordForNode. CreateNamespaceRecords
// runs ONCE at provision time and AddNamespaceRecord only on node replacement, so
// nothing re-asserted these rows on boot. That asymmetry made deleting them unsafe:
// a node marked non-active transiently (120s without a heartbeat, a rolling deploy,
// or markNodeOffline) would lose its gateway A record permanently, eroding the
// round-robin one node at a time with no recovery path. With this ensure in place
// the purge becomes recoverable — a flap self-heals on the next heartbeat.
//
// Per-node and additive: inserts ONLY this node's value, only when absent, so it
// never touches another node's row for the same round-robin fqdn — safe to run
// unguarded on every node (no leader gating), exactly like the TURN ensure.
func (drm *DNSRecordManager) EnsureNamespaceHostRecordForNode(ctx context.Context, namespaceName, nodeIP string) error {
	if nodeIP == "" {
		return nil
	}
	internalCtx := client.WithInternalAuth(ctx)
	now := time.Now()
	tag := "namespace:" + namespaceName

	for _, fqdn := range []string{
		fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain),
		fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain),
	} {
		if _, err := drm.db.Exec(internalCtx, ensureNamespaceHostRecordSQL,
			fqdn, nodeIP, tag, now, now, fqdn, nodeIP, tag); err != nil {
			return &ClusterError{
				Message: fmt.Sprintf("ensure namespace host A record %s -> %s", fqdn, nodeIP),
				Cause:   err,
			}
		}
	}
	return nil
}

// reactivateNamespaceHostRecordSQL re-enables THIS node's own namespace-host A
// record. Scoped to one (fqdn, value, namespace) triple, so it can only ever
// touch the calling node's own row in the round-robin.
//
// Bugboard #286: the additive ensure below deliberately never re-enables a
// soft-disabled row, on the grounds that doing so would fight the health monitor.
// That is right for a node making claims about its peers, and wrong for a node
// making a claim about ITSELF: the monitor disables a record because the node
// looked dead, and a node that is running this sweep with its gateway answering
// is demonstrably not dead. Without this the record stays disabled forever after
// a restart and the node silently never rejoins the round-robin — the recovery
// path the ensure's own doc comment says should exist.
const reactivateNamespaceHostRecordSQL = `UPDATE dns_records
	SET is_active = 1, updated_at = ?
	WHERE fqdn = ? AND record_type = 'A' AND value = ? AND namespace = ? AND is_active = 0`

// EnsureNamespaceHostRecordActiveForNode asserts that this node's namespace-host
// records exist AND are active. Call it only with positive evidence that this
// node is serving the namespace (bugboard #286).
func (drm *DNSRecordManager) EnsureNamespaceHostRecordActiveForNode(ctx context.Context, namespaceName, nodeIP string) error {
	if nodeIP == "" {
		return nil
	}
	if err := drm.EnsureNamespaceHostRecordForNode(ctx, namespaceName, nodeIP); err != nil {
		return err
	}

	internalCtx := client.WithInternalAuth(ctx)
	now := time.Now()
	tag := "namespace:" + namespaceName
	for _, fqdn := range []string{
		fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain),
		fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain),
	} {
		if _, err := drm.db.Exec(internalCtx, reactivateNamespaceHostRecordSQL, now, fqdn, nodeIP, tag); err != nil {
			return &ClusterError{
				Message: fmt.Sprintf("reactivate namespace host A record %s -> %s", fqdn, nodeIP),
				Cause:   err,
			}
		}
	}
	return nil
}

// DeleteTURNRecords deletes all TURN DNS records for a namespace.
func (drm *DNSRecordManager) DeleteTURNRecords(ctx context.Context, namespaceName string) error {
	internalCtx := client.WithInternalAuth(ctx)

	drm.logger.Info("Deleting TURN DNS records",
		zap.String("namespace", namespaceName),
	)

	deleteQuery := `DELETE FROM dns_records WHERE namespace = ?`
	_, err := drm.db.Exec(internalCtx, deleteQuery, "namespace-turn:"+namespaceName)
	if err != nil {
		return &ClusterError{
			Message: "failed to delete TURN DNS records",
			Cause:   err,
		}
	}

	return nil
}

// stealthDNSNamespace is the dns_records ownership tag for a namespace's
// stealth TURNS records, distinct from "namespace-turn:" so deleting one set
// never touches the other.
func stealthDNSNamespace(namespaceName string) string {
	return "namespace-turn-stealth:" + namespaceName
}

// CreateStealthTURNRecords creates DNS A records for the stealth TURNS host
// (feat-124): <stealthHost> -> TURN node IPs. The hostname is the neutral
// cdn-<hash>.<base-domain> label from turn.StealthHostForNamespace — it lives
// directly under the base domain (NOT under ns-<namespace>) so the SNI string
// never identifies the app.
func (drm *DNSRecordManager) CreateStealthTURNRecords(ctx context.Context, namespaceName, stealthHost string, turnIPs []string) error {
	internalCtx := client.WithInternalAuth(ctx)

	if stealthHost == "" {
		return &ClusterError{Message: "no stealth host provided for DNS records"}
	}
	if len(turnIPs) == 0 {
		return &ClusterError{Message: "no TURN IPs provided for stealth DNS records"}
	}

	fqdn := stealthHost + "."

	drm.logger.Info("Creating stealth TURNS DNS records",
		zap.String("namespace", namespaceName),
		zap.String("fqdn", fqdn),
		zap.Strings("turn_ips", turnIPs),
	)

	// Refuse to publish a stealth host another namespace already claims
	// (bugboard #283 part 2). The host is a truncated hash of the namespace, so a
	// second claimant is not an accident — it is a bid for another namespace's
	// censorship-resistant identity. Because the DELETE below is scoped to the
	// CALLER's namespace, two claimants would each insert A records for the same
	// fqdn and neither would remove the other's: resolvers return the union, and
	// the victim's stealth clients round-robin onto a node that cannot serve them
	// while disclosing their addresses to whoever holds it.
	var claimants []struct {
		Namespace string `db:"namespace"`
	}
	if err := drm.db.Query(internalCtx, &claimants,
		`SELECT DISTINCT namespace FROM dns_records WHERE fqdn = ? AND namespace != ?`,
		fqdn, stealthDNSNamespace(namespaceName)); err != nil {
		return &ClusterError{Message: "failed to check stealth host uniqueness", Cause: err}
	}
	if len(claimants) > 0 {
		return &ClusterError{Message: fmt.Sprintf(
			"stealth host %s is already claimed by another namespace; refusing to publish overlapping records", fqdn)}
	}

	deleteQuery := `DELETE FROM dns_records WHERE namespace = ?`
	_, _ = drm.db.Exec(internalCtx, deleteQuery, stealthDNSNamespace(namespaceName))

	now := time.Now()
	for _, ip := range turnIPs {
		insertQuery := `
			INSERT INTO dns_records (
				fqdn, record_type, value, ttl, namespace, created_by, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`
		_, err := drm.db.Exec(internalCtx, insertQuery,
			fqdn, "A", ip, 60,
			stealthDNSNamespace(namespaceName),
			"cluster-manager",
			now, now,
		)
		if err != nil {
			return &ClusterError{
				Message: fmt.Sprintf("failed to create stealth TURNS DNS record %s -> %s", fqdn, ip),
				Cause:   err,
			}
		}
	}

	return nil
}

// DeleteStealthTURNRecords deletes a namespace's stealth TURNS DNS records.
func (drm *DNSRecordManager) DeleteStealthTURNRecords(ctx context.Context, namespaceName string) error {
	internalCtx := client.WithInternalAuth(ctx)

	deleteQuery := `DELETE FROM dns_records WHERE namespace = ?`
	_, err := drm.db.Exec(internalCtx, deleteQuery, stealthDNSNamespace(namespaceName))
	if err != nil {
		return &ClusterError{
			Message: "failed to delete stealth TURNS DNS records",
			Cause:   err,
		}
	}
	return nil
}

// EnableNamespaceRecord marks a specific IP's record as active (for recovery)
func (drm *DNSRecordManager) EnableNamespaceRecord(ctx context.Context, namespaceName, ip string) error {
	internalCtx := client.WithInternalAuth(ctx)

	fqdn := fmt.Sprintf("ns-%s.%s.", namespaceName, drm.baseDomain)
	wildcardFqdn := fmt.Sprintf("*.ns-%s.%s.", namespaceName, drm.baseDomain)

	drm.logger.Info("Enabling namespace DNS record",
		zap.String("namespace", namespaceName),
		zap.String("ip", ip),
	)

	for _, f := range []string{fqdn, wildcardFqdn} {
		updateQuery := `UPDATE dns_records SET is_active = 1, updated_at = ? WHERE fqdn = ? AND value = ?`
		_, _ = drm.db.Exec(internalCtx, updateQuery, time.Now(), f, ip)
	}

	return nil
}

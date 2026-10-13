package node

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// withdrawOwnBaseRecordSQL removes this node's A record for one base name
// (`?` 1 fqdn, 2 and 3 this node's IP), unless it is the name's last one: a
// round-robin with no address answers nothing, and a wrong edge check on every
// node must not take the cluster's name off the internet.
const withdrawOwnBaseRecordSQL = `DELETE FROM dns_records
	 WHERE fqdn = ? AND record_type = 'A' AND namespace = 'system' AND value = ?
	   AND EXISTS (
	       SELECT 1 FROM dns_records AS survivor
	        WHERE survivor.fqdn = dns_records.fqdn AND survivor.record_type = 'A'
	          AND survivor.namespace = 'system' AND survivor.is_active = TRUE
	          AND survivor.value != ?
	   )`

// withdrawOwnNamespaceHostRecordsSQL removes this node from every namespace
// gateway round-robin (`ns-<name>` and `*.ns-<name>`; `?` 1 and 2 this node's
// IP), never emptying one. TURN records are left alone: TURN does not go
// through Caddy.
const withdrawOwnNamespaceHostRecordsSQL = `DELETE FROM dns_records
	 WHERE record_type = 'A' AND namespace LIKE 'namespace:%' AND value = ?
	   AND (fqdn LIKE 'ns-%' OR fqdn LIKE '*.ns-%')
	   AND EXISTS (
	       SELECT 1 FROM dns_records AS survivor
	        WHERE survivor.fqdn = dns_records.fqdn AND survivor.record_type = 'A'
	          AND survivor.is_active = TRUE AND survivor.value != ?
	   )`

// withdrawOwnRecords removes ip from the base names and the namespace gateway
// round-robins and returns how many records went.
func withdrawOwnRecords(ctx context.Context, db *sql.DB, baseFQDNs []string, ip string) (int64, error) {
	var removed int64
	for _, fqdn := range baseFQDNs {
		res, err := rqlite.SafeExecContext(db, ctx, withdrawOwnBaseRecordSQL, fqdn, ip, ip)
		if err != nil {
			return removed, fmt.Errorf("withdraw %s from %s: %w", ip, fqdn, err)
		}
		n, _ := res.RowsAffected()
		removed += n
	}
	res, err := rqlite.SafeExecContext(db, ctx, withdrawOwnNamespaceHostRecordsSQL, ip, ip)
	if err != nil {
		return removed, fmt.Errorf("withdraw %s from the namespace gateway round-robins: %w", ip, err)
	}
	n, _ := res.RowsAffected()
	return removed + n, nil
}

// baseRecordFQDNs are the system names every serving node is an A record of.
func (n *Node) baseRecordFQDNs(baseDomain string) []string {
	fqdns := []string{baseDomain + ".", "*." + baseDomain + "."}
	if d := n.config.Node.Domain; d != "" && d != baseDomain {
		fqdns = append(fqdns, d+".", "*."+d+".")
	}
	return fqdns
}

// withdrawFromDNS takes this node out of the round-robins while its edge does
// not serve (why); the heartbeat that runs every tick re-adds it once it does.
func (n *Node) withdrawFromDNS(ctx context.Context, why error) {
	baseDomain := n.config.HTTPGateway.BaseDomain
	if baseDomain == "" {
		baseDomain = n.config.Node.Domain
	}
	if baseDomain == "" || n.getRQLiteAdapter() == nil {
		return
	}
	ip, err := n.getNodeIPAddress()
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "This node does not terminate TLS, and its address is unknown, so it cannot withdraw its DNS records",
			zap.NamedError("edge", why), zap.Error(err))
		return
	}
	removed, err := withdrawOwnRecords(ctx, n.getRQLiteAdapter().GetSQLDB(), n.baseRecordFQDNs(baseDomain), ip)
	if err != nil {
		n.logger.ComponentWarn(logging.ComponentNode, "This node does not terminate TLS and failed to withdraw its DNS records",
			zap.NamedError("edge", why), zap.Error(err))
		return
	}
	n.logger.ComponentWarn(logging.ComponentNode, "This node does not terminate TLS: kept out of DNS until it does",
		zap.NamedError("edge", why), zap.Int64("records_removed", removed))
}

package node

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Nameserver slots and the zone apex records derived from them.
//
// A nameserver node claims a slot — ns1, ns2, … — in dns_nameservers and
// writes its glue A record (nsN.<base> → its public IP). The zone's NS set and
// its SOA are derived from those claims, so a cluster with one, two or five
// nameservers publishes exactly those, and never an NS name that has no glue.
// They used to be ns1..ns3 whatever the cluster was: a one-nameserver cluster
// published two NS names that resolved nowhere.

// maxNameserverSlots bounds the slots a domain can have. Thirteen NS records
// with their glue is what still fits a classic 512-byte referral — the same
// limit that sets the number of root servers.
const maxNameserverSlots = 13

// nameserverSlotPrefix is the slot hostname's prefix: ns1, ns2, …
const nameserverSlotPrefix = "ns"

// soaTimers are the SOA's refresh, retry, expire and minimum fields.
const soaTimers = "3600 1800 604800 300"

// slotHostname is the hostname of slot i.
func slotHostname(i int) string {
	return nameserverSlotPrefix + strconv.Itoa(i)
}

// slotNumber is the number of a slot hostname, or 0 for anything else.
func slotNumber(hostname string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(hostname, nameserverSlotPrefix))
	if err != nil || !strings.HasPrefix(hostname, nameserverSlotPrefix) || n < 1 {
		return 0
	}
	return n
}

// ensureGlueSQL writes a slot's glue record. The glue names exactly one
// address — the slot holder's — so pruneGlueSQL removes any other.
const ensureGlueSQL = `INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
	VALUES (?, 'A', ?, 300, 'system', 'system', TRUE, datetime('now'), datetime('now'))
	ON CONFLICT(fqdn, record_type, value) DO UPDATE SET is_active = TRUE, updated_at = datetime('now')`

const pruneGlueSQL = `DELETE FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND namespace = 'system' AND value != ?`

// queryStrings runs a one-column query and returns its values, sorted.
func queryStrings(ctx context.Context, db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// glueValues is what nsFQDN's glue resolves to now.
func glueValues(ctx context.Context, db *sql.DB, nsFQDN string) ([]string, error) {
	vals, err := queryStrings(ctx, db,
		`SELECT value FROM dns_records WHERE fqdn = ? AND record_type = 'A' AND namespace = 'system' AND is_active = TRUE`, nsFQDN)
	if err != nil {
		return nil, fmt.Errorf("read glue for %s: %w", nsFQDN, err)
	}
	return vals, nil
}

// writeGlue makes nsFQDN resolve to ip; pruneGlue then removes any other
// address. Written in that order so the slot always has glue for its address:
// a slot without glue drops out of the zone's NS set.
func writeGlue(ctx context.Context, db *sql.DB, nsFQDN, ip string) error {
	if _, err := rqlite.SafeExecContext(db, ctx, ensureGlueSQL, nsFQDN, ip); err != nil {
		return fmt.Errorf("write glue %s -> %s: %w", nsFQDN, ip, err)
	}
	return nil
}

func pruneGlue(ctx context.Context, db *sql.DB, nsFQDN, ip string) error {
	if _, err := rqlite.SafeExecContext(db, ctx, pruneGlueSQL, nsFQDN, ip); err != nil {
		return fmt.Errorf("prune stale glue for %s: %w", nsFQDN, err)
	}
	return nil
}

// claimNameserverSlot gives this node a slot for domain — the one it holds,
// else the lowest free one — records its address, and writes the glue. It
// returns the slot's hostname.
func claimNameserverSlot(ctx context.Context, db *sql.DB, nodeID, domain, ip string) (string, error) {
	if nodeID == "" {
		return "", fmt.Errorf("claim a nameserver slot for %s: this node has no peer id yet", domain)
	}

	var held, heldIP string
	err := db.QueryRowContext(ctx,
		`SELECT hostname, ip_address FROM dns_nameservers WHERE node_id = ? AND domain = ?`, nodeID, domain,
	).Scan(&held, &heldIP)
	switch {
	case err == nil:
		return held, refreshHeldSlot(ctx, db, held, heldIP, domain, ip)
	case !errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("read this node's nameserver slot for %s: %w", domain, err)
	}

	for i := 1; i <= maxNameserverSlots; i++ {
		hostname := slotHostname(i)
		res, err := rqlite.SafeExecContext(db, ctx,
			`INSERT INTO dns_nameservers (hostname, node_id, ip_address, domain) VALUES (?, ?, ?, ?)
			ON CONFLICT(hostname) DO NOTHING`,
			hostname, nodeID, ip, domain)
		if err != nil {
			return "", fmt.Errorf("claim nameserver slot %s.%s: %w", hostname, domain, err)
		}
		claimed, err := res.RowsAffected()
		if err != nil {
			return "", fmt.Errorf("claim nameserver slot %s.%s: %w", hostname, domain, err)
		}
		if claimed > 0 {
			nsFQDN := hostname + "." + domain + "."
			if err := writeGlue(ctx, db, nsFQDN, ip); err != nil {
				return "", err
			}
			return hostname, pruneGlue(ctx, db, nsFQDN, ip)
		}
	}
	return "", fmt.Errorf("all %d nameserver slots for %s are held by other nodes; "+
		"retire a departed nameserver with `orama node remove` to free its slot", maxNameserverSlots, domain)
}

// refreshHeldSlot keeps a held slot's address and glue on ip. It writes
// nothing when both already are, which is every sweep but the one after an
// address change.
func refreshHeldSlot(ctx context.Context, db *sql.DB, hostname, heldIP, domain, ip string) error {
	nsFQDN := hostname + "." + domain + "."
	glue, err := glueValues(ctx, db, nsFQDN)
	if err != nil {
		return err
	}
	if heldIP == ip && len(glue) == 1 && glue[0] == ip {
		return nil
	}
	if err := writeGlue(ctx, db, nsFQDN, ip); err != nil {
		return err
	}
	if _, err := rqlite.SafeExecContext(db, ctx,
		`UPDATE dns_nameservers SET ip_address = ?, updated_at = datetime('now') WHERE hostname = ? AND domain = ?`,
		ip, hostname, domain); err != nil {
		return fmt.Errorf("record address %s for nameserver slot %s: %w", ip, nsFQDN, err)
	}
	return pruneGlue(ctx, db, nsFQDN, ip)
}

// releaseNameserverSlot frees the slot nodeID holds and removes its glue —
// all of it, whatever address it names: the slot is going away.
func releaseNameserverSlot(ctx context.Context, db *sql.DB, nodeID string) error {
	if _, err := rqlite.SafeExecContext(db, ctx,
		`DELETE FROM dns_records WHERE record_type = 'A' AND namespace = 'system'
		   AND fqdn IN (SELECT hostname||'.'||domain||'.' FROM dns_nameservers WHERE node_id = ?)`,
		nodeID); err != nil {
		return fmt.Errorf("remove glue of the nameserver slot held by %s: %w", nodeID, err)
	}
	if _, err := rqlite.SafeExecContext(db, ctx, `DELETE FROM dns_nameservers WHERE node_id = ?`, nodeID); err != nil {
		return fmt.Errorf("release the nameserver slot held by %s: %w", nodeID, err)
	}
	return nil
}

// slotGluedSQL is true for a dns_nameservers row `ns` whose glue record
// exists and resolves: the only slots the zone may publish.
const slotGluedSQL = `EXISTS (SELECT 1 FROM dns_records g
	                WHERE g.fqdn = ns.hostname||'.'||ns.domain||'.'
	                  AND g.record_type = 'A' AND g.value = ns.ip_address AND g.is_active = TRUE)`

// gluedSlotNamesSQL selects the NS names (nsN.<domain>.) of domain's glued
// slots. `?` is the domain.
const gluedSlotNamesSQL = `SELECT ns.hostname||'.'||ns.domain||'.' FROM dns_nameservers ns
	 WHERE ns.domain = ? AND ` + slotGluedSQL

// ensureNSRecordsSQL publishes an NS record for every glued slot. `?` order:
// apex fqdn, domain.
const ensureNSRecordsSQL = `INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
	SELECT ?, 'NS', ns.hostname||'.'||ns.domain||'.', 300, 'system', 'system', TRUE, datetime('now'), datetime('now')
	  FROM dns_nameservers ns
	 WHERE ns.domain = ? AND ` + slotGluedSQL + `
	ON CONFLICT(fqdn, record_type, value) DO UPDATE SET is_active = TRUE, updated_at = datetime('now')`

// pruneNSRecordsSQL removes every system NS record at the apex that is not a
// glued slot — but only while at least one slot is glued. With none (every
// nameserver missed its heartbeat at once, or none has claimed yet) the last
// known set stays: a zone with no NS records at all serves nothing, and the
// set is corrected the moment a slot is glued again. `?` order: apex fqdn,
// domain, domain.
const pruneNSRecordsSQL = `DELETE FROM dns_records
	 WHERE fqdn = ? AND record_type = 'NS' AND namespace = 'system'
	   AND value NOT IN (` + gluedSlotNamesSQL + `)
	   AND EXISTS (` + gluedSlotNamesSQL + `)`

// reconcileNameserverRecords makes the zone apex's NS set exactly the glued
// slots, and — on the node holding the lowest glued slot — makes the SOA name
// that slot as the primary.
//
// Every node runs it on every sweep. The NS statements are set-based and
// idempotent, so they converge from anywhere. The SOA is rewritten by one node
// only, because its serial differs per writer and two writers would leave two
// SOA rows.
func reconcileNameserverRecords(ctx context.Context, db *sql.DB, baseDomain, selfNodeID string) error {
	apex := baseDomain + "."
	if err := reconcileNSSet(ctx, db, apex, baseDomain); err != nil {
		return err
	}
	primary, holder, err := primaryNameserver(ctx, db, baseDomain)
	if err != nil || primary == "" || holder != selfNodeID {
		return err
	}
	return ensureSOA(ctx, db, apex, primary+"."+apex)
}

// reconcileNSSet makes the apex NS set the glued slots, writing only when
// they differ: every node runs this every sweep, and a no-op write is still
// a raft round-trip.
func reconcileNSSet(ctx context.Context, db *sql.DB, apex, domain string) error {
	want, err := queryStrings(ctx, db, gluedSlotNamesSQL, domain)
	if err != nil {
		return fmt.Errorf("read glued nameserver slots for %s: %w", domain, err)
	}
	have, err := queryStrings(ctx, db,
		`SELECT value FROM dns_records WHERE fqdn = ? AND record_type = 'NS' AND namespace = 'system' AND is_active = TRUE`, apex)
	if err != nil {
		return fmt.Errorf("read NS records for %s: %w", domain, err)
	}
	if slices.Equal(want, have) {
		return nil
	}
	if _, err := rqlite.SafeExecContext(db, ctx, ensureNSRecordsSQL, apex, domain); err != nil {
		return fmt.Errorf("publish NS records for %s: %w", domain, err)
	}
	if _, err := rqlite.SafeExecContext(db, ctx, pruneNSRecordsSQL, apex, domain, domain); err != nil {
		return fmt.Errorf("remove NS records without a claimed, glued slot for %s: %w", domain, err)
	}
	return nil
}

// primaryNameserver is the lowest-numbered glued slot of domain and the node
// holding it; "" when no slot is glued.
func primaryNameserver(ctx context.Context, db *sql.DB, domain string) (string, string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ns.hostname, ns.node_id FROM dns_nameservers ns WHERE ns.domain = ? AND `+slotGluedSQL,
		domain)
	if err != nil {
		return "", "", fmt.Errorf("read nameserver slots for %s: %w", domain, err)
	}
	defer rows.Close()

	type slot struct{ hostname, nodeID string }
	var slots []slot
	for rows.Next() {
		var s slot
		if err := rows.Scan(&s.hostname, &s.nodeID); err != nil {
			return "", "", fmt.Errorf("read nameserver slots for %s: %w", domain, err)
		}
		if slotNumber(s.hostname) > 0 {
			slots = append(slots, s)
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", fmt.Errorf("read nameserver slots for %s: %w", domain, err)
	}
	if len(slots) == 0 {
		return "", "", nil
	}
	sort.Slice(slots, func(i, j int) bool { return slotNumber(slots[i].hostname) < slotNumber(slots[j].hostname) })
	return slots[0].hostname, slots[0].nodeID, nil
}

// ensureSOA makes the apex carry exactly one SOA whose primary is mname. It
// is changed in place, never deleted and re-inserted: between those two
// writes the zone would have no SOA at all.
func ensureSOA(ctx context.Context, db *sql.DB, apex, mname string) error {
	values, err := queryStrings(ctx, db,
		`SELECT value FROM dns_records WHERE fqdn = ? AND record_type = 'SOA' AND namespace = 'system'`, apex)
	if err != nil {
		return fmt.Errorf("read SOA for %s: %w", apex, err)
	}
	if len(values) == 1 && strings.HasPrefix(values[0], mname+" ") {
		return nil
	}

	soa := fmt.Sprintf("%s admin.%s %d %s", mname, apex, time.Now().Unix(), soaTimers)
	if len(values) == 0 {
		if _, err := rqlite.SafeExecContext(db, ctx,
			`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
			VALUES (?, 'SOA', ?, 300, 'system', 'system', TRUE, datetime('now'), datetime('now'))`,
			apex, soa); err != nil {
			return fmt.Errorf("write SOA for %s: %w", apex, err)
		}
		return nil
	}
	if len(values) > 1 {
		if _, err := rqlite.SafeExecContext(db, ctx,
			`DELETE FROM dns_records WHERE fqdn = ? AND record_type = 'SOA' AND namespace = 'system'
			   AND id != (SELECT MIN(id) FROM dns_records WHERE fqdn = ? AND record_type = 'SOA' AND namespace = 'system')`,
			apex, apex); err != nil {
			return fmt.Errorf("remove duplicate SOA records for %s: %w", apex, err)
		}
	}
	if _, err := rqlite.SafeExecContext(db, ctx,
		`UPDATE dns_records SET value = ?, is_active = TRUE, updated_at = datetime('now')
		  WHERE fqdn = ? AND record_type = 'SOA' AND namespace = 'system'`,
		soa, apex); err != nil {
		return fmt.Errorf("update SOA for %s: %w", apex, err)
	}
	return nil
}

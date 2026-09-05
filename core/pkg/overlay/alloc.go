// Package overlay owns the WireGuard overlay address space and the one way a
// peer gets written into it.
//
// It exists because there were three independent implementations of "pick an
// address and insert a wireguard_peers row" — the join handler, the peer
// registration endpoint and the OramaOS enrolment handler — and all three were
// wrong in the same two ways: they read the table and wrote it in separate
// statements, and they wrote with INSERT OR REPLACE, so the loser of the race
// silently deleted the winner's row and stole its address. A node that had just
// joined would find itself cut out of the mesh by the next one.
//
// A fourth writer remains outside this package by design: a node's WireGuard
// self-registration (pkg/node.ensureWireGuardSelfRegistered). It allocates
// nothing — it re-asserts the row it was already given, keyed by its own node
// id — so it upserts rather than going through Register.
package overlay

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
)

// Range describes the overlay: 10.0.0.0/24, with .1 reserved for the genesis
// node, .0 the network address and .255 the broadcast address.
const (
	Prefix    = "10.0.0."
	FirstHost = 2
	LastHost  = 254

	// wgListenPort is the port every node's wg0 listens on.
	wgListenPort = 51820
)

// allocationAttempts bounds the retry when a concurrent join takes the address
// between the read and the insert. Each attempt re-reads, so the only way to
// exhaust this is genuine contention from eight simultaneous joins.
const allocationAttempts = 8

// Querier is the slice of rqlite.Client this package needs. Narrow so tests
// drive it without a database and so nothing here can reach for another table.
type Querier interface {
	Query(ctx context.Context, dest any, query string, args ...any) error
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Peer is a row to be written into the overlay. WGIP is assigned by Register.
type Peer struct {
	// NodeID is the joining node's libp2p peer id. Empty is allowed — a node
	// installed before the installer sent one — and yields a synthetic id.
	NodeID         string
	PublicKey      string
	PublicIP       string
	WGPort         int
	OperatorWallet string
}

// Register allocates the lowest free overlay address and inserts the peer,
// returning the address it was given.
//
// The row is written UNCONFIRMED: confirmed_at stays NULL until the node is
// seen in dns_nodes. A join that fails after this point therefore leaves
// something the membership reconciler can identify and remove, rather than a
// row indistinguishable from a live peer that every survivor re-applies to its
// interface every sixty seconds for ever.
func Register(ctx context.Context, db Querier, p Peer) (string, error) {
	if p.PublicKey == "" || p.PublicIP == "" {
		return "", fmt.Errorf("overlay: public key and public IP are required")
	}
	// The node id becomes this row's primary key and is read back by every
	// consumer that wants to know which machine the row belongs to, so an
	// unparseable one is refused here rather than stored and tripped over
	// later.
	if p.NodeID != "" {
		if _, err := peer.Decode(p.NodeID); err != nil {
			return "", fmt.Errorf("overlay: %q is not a valid peer id: %w", p.NodeID, err)
		}
	}
	if p.WGPort == 0 {
		p.WGPort = wgListenPort
	}

	var lastErr error
	for attempt := 0; attempt < allocationAttempts; attempt++ {
		wgIP, err := NextFree(ctx, db)
		if err != nil {
			return "", err
		}

		id := p.NodeID
		if id == "" {
			id = fmt.Sprintf("node-%s", wgIP)
		}

		_, err = db.Exec(ctx,
			`INSERT INTO wireguard_peers (node_id, wg_ip, public_key, public_ip, wg_port, operator_wallet)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, wgIP, p.PublicKey, p.PublicIP, p.WGPort, p.OperatorWallet)
		if err == nil {
			return wgIP, nil
		}
		lastErr = err

		// Only a conflict on the ADDRESS is a lost allocation race. A conflict
		// on the public key or the node id means this peer is already
		// registered under a different address, and no number of retries will
		// change that — each one would pick a new address and fail on the same
		// column, turning a clear error into eight round trips and a confusing
		// one.
		if !conflictsOn(err, "wg_ip") {
			return "", fmt.Errorf("overlay: register peer: %w", err)
		}
	}

	return "", fmt.Errorf("overlay: could not allocate an address after %d attempts: %w",
		allocationAttempts, lastErr)
}

// NextFree returns the lowest unallocated address in the overlay range.
//
// Lowest, not highest+1: addresses were never reused, so a cluster that had
// churned through nodes eventually rolled past 10.0.0.254 into 10.0.1.x —
// outside the /24 that the wg0 PostUp rule and the internal-auth check both
// accept, so those nodes could reach nobody and nobody could reach them.
func NextFree(ctx context.Context, db Querier) (string, error) {
	var rows []struct {
		WGIP string `db:"wg_ip"`
	}
	if err := db.Query(ctx, &rows, "SELECT wg_ip FROM wireguard_peers"); err != nil {
		return "", fmt.Errorf("overlay: read allocated addresses: %w", err)
	}

	taken := make(map[int]struct{}, len(rows))
	for _, row := range rows {
		if host, ok := Host(row.WGIP); ok {
			taken[host] = struct{}{}
		}
	}

	for host := FirstHost; host <= LastHost; host++ {
		if _, used := taken[host]; !used {
			return Address(host), nil
		}
	}
	return "", fmt.Errorf("overlay: address space exhausted, all of %s%d-%d are allocated",
		Prefix, FirstHost, LastHost)
}

// Address renders the overlay address for a host octet.
func Address(host int) string {
	return fmt.Sprintf("%s%d", Prefix, host)
}

// Host extracts the host octet of an address inside the overlay range, and
// reports whether the address is in that range at all.
func Host(ip string) (int, bool) {
	var a, b, c, d int
	if n, err := fmt.Sscanf(ip, "%d.%d.%d.%d", &a, &b, &c, &d); err != nil || n != 4 {
		return 0, false
	}
	if a != 10 || b != 0 || c != 0 || d < 0 || d > 255 {
		return 0, false
	}
	return d, true
}

// IsConflict reports whether err is a uniqueness violation on any column — the
// caller named an identity that is already registered.
//
// Callers use it to tell a caller error from a cluster fault. In the join
// handler that distinction is what stops an invite token being replayed: a
// conflict means the request was bad, so the token stays spent, while only a
// genuine server-side failure releases it.
func IsConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "constraint failed: unique")
}

// conflictsOn reports whether err is a uniqueness violation on the named
// column.
//
// It matches on the message because rqlite returns constraint failures as
// opaque strings over its HTTP API: there is no driver error type to compare
// against, and no code either. SQLite names the column it rejected —
// "UNIQUE constraint failed: wireguard_peers.wg_ip" — and rqlite passes that
// through.
//
// A message this does not recognise yields false, so Register surfaces the
// error instead of retrying it. That is the safe direction: the cost is a join
// that has to be run again with a clear error, rather than a retry loop
// spinning on something retrying cannot fix.
func conflictsOn(err error, column string) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "unique constraint failed") &&
		!strings.Contains(msg, "constraint failed: unique") {
		return false
	}
	return strings.Contains(msg, "."+strings.ToLower(column))
}

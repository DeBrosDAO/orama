package nodenames

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config/validate"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

const (
	// SyncInterval is how often the chain is read once the zone is up to date. A claim or a release
	// is in DNS within an interval plus the record TTL.
	SyncInterval = time.Minute
	// SyncTimeout bounds one pass: the chain pages and the writes.
	SyncTimeout = 45 * time.Second
	// CatchUpDelay is the wait before the next pass when a pass left writes undone.
	CatchUpDelay = time.Second
	// MaxPages bounds the pages one pass reads: a million names. A chain that answers more, or a
	// page key that does not advance, is a fault to report, not a list to walk for ever.
	MaxPages = 1000
	// MaxWritesPerPass bounds the rows one pass writes through the registry's Raft log. A first
	// pass over a large chain converges over several passes, CatchUpDelay apart.
	MaxWritesPerPass = 2000
)

const (
	selectOwnedSQL = `SELECT fqdn, record_type, value, is_active FROM dns_records WHERE namespace = ?`

	insertSQL = `INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, TRUE, datetime('now'), datetime('now'))
	ON CONFLICT(fqdn, record_type, value) DO NOTHING`

	activateSQL = `UPDATE dns_records SET is_active = TRUE, updated_at = datetime('now')
	WHERE fqdn = ? AND record_type = ? AND value = ? AND namespace = ?`

	deleteSQL = `DELETE FROM dns_records WHERE fqdn = ? AND record_type = ? AND value = ? AND namespace = ?`
)

// Syncer keeps the dns_records of one zone equal to the names the chain holds. It owns the rows
// tagged RecordNamespace and touches no others. Running it twice, or on several nodes of the
// cluster at once, writes the same rows: every statement is keyed on (fqdn, type, value).
type Syncer struct {
	// Registry returns the cluster registry's database, asked for on every pass: the handle can be
	// replaced when the registry restarts, and it may not exist yet at start-up.
	Registry func() (*sql.DB, error)
	Chain    Chain
	// Zone is the domain names are published under, for example stagenet.orama.network.
	Zone string
}

// Stats is what one pass did.
type Stats struct {
	// Names is how many claimed names the chain holds.
	Names int
	// Added, Reactivated and Removed are the rows written.
	Added, Reactivated, Removed int
	// Remaining is how many writes the pass left for the next one (MaxWritesPerPass).
	Remaining int
	// Refused lists the names and addresses that were not published, and why.
	Refused []Refusal
}

// Changed reports whether the pass wrote anything.
func (s Stats) Changed() bool { return s.Added+s.Reactivated+s.Removed > 0 }

// Sync reads every claimed name from the chain and brings the zone's rows in line: it adds the
// records that are missing, reactivates the ones something deactivated, and removes the ones whose
// name was released or whose node's address changed. Records are added before any is removed, so a
// name that moves is never unresolvable in between.
//
// Nothing is written unless the whole list was read: a chain that fails half way leaves the zone as
// it was, rather than deleting the names on the pages that were not read.
func (s *Syncer) Sync(ctx context.Context) (Stats, error) {
	if err := validate.ValidateZone(s.Zone); err != nil {
		return Stats{}, fmt.Errorf("node names zone: %w", err)
	}
	entries, err := s.readAll(ctx)
	if err != nil {
		return Stats{}, err
	}
	db, err := s.Registry()
	if err != nil {
		return Stats{}, fmt.Errorf("open the cluster registry to publish node names: %w", err)
	}
	want, refused := Desired(entries, s.Zone)
	stats := Stats{Names: len(entries), Refused: refused}
	have, err := owned(ctx, db)
	if err != nil {
		return stats, err
	}
	return stats, reconcile(ctx, db, want, have, &stats)
}

// readAll pages the chain to the end.
func (s *Syncer) readAll(ctx context.Context) ([]Named, error) {
	var all []Named
	key := ""
	seen := map[string]struct{}{}
	for pages := 0; ; pages++ {
		if pages == MaxPages {
			return nil, fmt.Errorf("the chain returned more than %d pages of names (%d names); refusing to sync a list that does not end", MaxPages, len(all))
		}
		page, err := s.Chain.NodeNames(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("read page %d of the claimed names: %w", pages+1, err)
		}
		all = append(all, page.Nodes...)
		if page.NextKey == "" {
			return all, nil
		}
		if _, repeated := seen[page.NextKey]; repeated {
			return nil, fmt.Errorf("the chain returned the page key %q twice: its list of names does not advance", page.NextKey)
		}
		seen[page.NextKey] = struct{}{}
		key = page.NextKey
	}
}

// owned reads the rows this package owns, with whether each is active.
func owned(ctx context.Context, db *sql.DB) (map[Record]bool, error) {
	rows, err := rqlite.SafeQueryContext(db, ctx, selectOwnedSQL, RecordNamespace)
	if err != nil {
		return nil, fmt.Errorf("read the node-name records in dns_records: %w", err)
	}
	defer rows.Close()
	have := map[Record]bool{}
	for rows.Next() {
		var r Record
		var active bool
		if err := rows.Scan(&r.FQDN, &r.Type, &r.Value, &active); err != nil {
			return nil, fmt.Errorf("read a node-name record: %w", err)
		}
		have[r] = active
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the node-name records in dns_records: %w", err)
	}
	return have, nil
}

// reconcile writes the difference between want and have, adds first. A failed write is reported
// and does not stop the others: one row that cannot be written must not starve the rest.
func reconcile(ctx context.Context, db *sql.DB, want []Record, have map[Record]bool, stats *Stats) error {
	wanted := make(map[Record]struct{}, len(want))
	var add, activate, remove []Record
	for _, r := range want {
		wanted[r] = struct{}{}
		if active, present := have[r]; !present {
			add = append(add, r)
		} else if !active {
			activate = append(activate, r)
		}
	}
	for r := range have {
		if _, keep := wanted[r]; !keep {
			remove = append(remove, r)
		}
	}
	var errs []error
	budget := MaxWritesPerPass
	for _, step := range []struct {
		what    string
		rows    []Record
		sql     string
		args    func(Record) []any
		counter *int
	}{
		{"add", add, insertSQL, func(r Record) []any {
			return []any{r.FQDN, r.Type, r.Value, RecordTTL, RecordNamespace, RecordNamespace}
		}, &stats.Added},
		{"reactivate", activate, activateSQL, ownedArgs, &stats.Reactivated},
		{"remove", remove, deleteSQL, ownedArgs, &stats.Removed},
	} {
		for _, r := range step.rows {
			if budget == 0 {
				stats.Remaining++
				continue
			}
			budget--
			res, err := rqlite.SafeExecContext(db, ctx, step.sql, step.args(r)...)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s %s %s %s: %w", step.what, r.FQDN, r.Type, r.Value, err))
				continue
			}
			if affected, aerr := res.RowsAffected(); aerr == nil && affected > 0 {
				*step.counter++
			}
		}
	}
	return errors.Join(errs...)
}

func ownedArgs(r Record) []any { return []any{r.FQDN, r.Type, r.Value, RecordNamespace} }

// Run syncs until ctx ends: once at once, then every SyncInterval, or after CatchUpDelay while a
// pass has writes left. report receives every pass's outcome.
func (s *Syncer) Run(ctx context.Context, report func(Stats, error)) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		passCtx, cancel := context.WithTimeout(ctx, SyncTimeout)
		stats, err := s.Sync(passCtx)
		cancel()
		report(stats, err)
		wait := SyncInterval
		if err == nil && stats.Remaining > 0 {
			wait = CatchUpDelay
		}
		timer.Reset(wait)
	}
}

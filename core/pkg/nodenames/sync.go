package nodenames

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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

	// MaxRemovalsNumerator over MaxRemovalsDenominator is the share of the rows the sync owns that
	// one pass may remove, and MinRemovalsPerPass is the most it may remove from a small zone. A
	// pass that wants to remove more than that has most likely read a truncated or stale list, so
	// it removes the bound and leaves the rest for the next passes, where a corrected read puts
	// back what was removed in error and costs a minute of DNS rather than the whole zone.
	MaxRemovalsNumerator   = 1
	MaxRemovalsDenominator = 4
	MinRemovalsPerPass     = 50

	// foreignRefusal is why a name with another owner's record at its fqdn is not published.
	foreignRefusal = "already has records of another owner in dns_records"
)

const (
	selectOwnedSQL = `SELECT fqdn, record_type, value, is_active FROM dns_records WHERE namespace = ?`

	// selectForeignSQL lists the names below the zone that someone else already answers for.
	selectForeignSQL = `SELECT DISTINCT fqdn FROM dns_records WHERE namespace != ? AND fqdn LIKE ?`

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
	// Zone is the dedicated sub-zone names are published under, for example
	// nodes.stagenet.orama.network: strictly below the cluster's base domain.
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
	// CatchingUp is set when the chain node was still catching up: its list may be old, so nothing
	// was removed.
	CatchingUp bool
	// RemovalsHeld is how many removals the pass left undone, because the chain node was catching
	// up or because the pass had reached its bound (MaxRemovalsNumerator).
	RemovalsHeld int
	// Refused lists the names and addresses that were not published, and why.
	Refused []Refusal
}

// Changed reports whether the pass wrote anything.
func (s Stats) Changed() bool { return s.Added+s.Reactivated+s.Removed > 0 }

// plan is the difference between the records wanted and the rows held.
type plan struct {
	add, activate, remove []Record
}

// Sync reads every claimed name from the chain and brings the zone's rows in line: it adds the
// records that are missing, reactivates the ones something deactivated, and removes the records of
// a name that was released or whose node's address changed. Records are added before any is
// removed, so a name that moves is never unresolvable in between.
//
// Nothing is written unless the whole list was read: a chain that fails half way leaves the zone as
// it was, rather than deleting the names on the pages that were not read. Nothing is removed while
// the chain node is catching up, and a pass removes at most a bounded share of the zone.
func (s *Syncer) Sync(ctx context.Context) (Stats, error) {
	if err := validate.ValidateZone(s.Zone); err != nil {
		return Stats{}, fmt.Errorf("node names zone: %w", err)
	}
	catchingUp, err := s.Chain.CatchingUp(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("ask the chain node whether it is catching up: %w", err)
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
	foreign, err := foreignNames(ctx, db, s.Zone)
	if err != nil {
		return Stats{}, err
	}
	want, shadowing := dropForeign(want, foreign)
	stats := Stats{Names: len(entries), CatchingUp: catchingUp, Refused: append(refused, shadowing...)}
	have, err := owned(ctx, db)
	if err != nil {
		return stats, err
	}
	p := diff(want, have)
	p.remove, stats.RemovalsHeld = boundRemovals(p.remove, len(have), catchingUp)
	return stats, apply(ctx, db, p, &stats)
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

// foreignNames is the set of names below zone that a record of another owner already answers for.
// A claimed name must not add an address next to a host the cluster itself publishes, so such a
// name is not published.
func foreignNames(ctx context.Context, db *sql.DB, zone string) (map[string]struct{}, error) {
	rows, err := rqlite.SafeQueryContext(db, ctx, selectForeignSQL, RecordNamespace, "%."+zone+".")
	if err != nil {
		return nil, fmt.Errorf("read the names below %s that other writers own: %w", zone, err)
	}
	defer rows.Close()
	foreign := map[string]struct{}{}
	for rows.Next() {
		var fqdn string
		if err := rows.Scan(&fqdn); err != nil {
			return nil, fmt.Errorf("read a name that another writer owns: %w", err)
		}
		foreign[fqdn] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the names below %s that other writers own: %w", zone, err)
	}
	return foreign, nil
}

// dropForeign removes the records of names that another owner holds, and reports each such name.
func dropForeign(want []Record, foreign map[string]struct{}) ([]Record, []Refusal) {
	var kept []Record
	var refused []Refusal
	reported := map[string]struct{}{}
	for _, r := range want {
		if _, taken := foreign[r.FQDN]; !taken {
			kept = append(kept, r)
			continue
		}
		if _, done := reported[r.FQDN]; !done {
			reported[r.FQDN] = struct{}{}
			refused = append(refused, Refusal{Name: r.FQDN, Reason: foreignRefusal})
		}
	}
	return kept, refused
}

// owned reads the rows this package owns, with whether each is active. is_active comes back as a
// bool from SQLite's driver and as a JSON number (float64) from the rqlite driver, and database/sql
// converts neither to the other, so it is scanned as it comes and read by truthy.
func owned(ctx context.Context, db *sql.DB) (map[Record]bool, error) {
	rows, err := rqlite.SafeQueryContext(db, ctx, selectOwnedSQL, RecordNamespace)
	if err != nil {
		return nil, fmt.Errorf("read the node-name records in dns_records: %w", err)
	}
	defer rows.Close()
	have := map[Record]bool{}
	for rows.Next() {
		var r Record
		var active any
		if err := rows.Scan(&r.FQDN, &r.Type, &r.Value, &active); err != nil {
			return nil, fmt.Errorf("read a node-name record: %w", err)
		}
		on, err := truthy(active)
		if err != nil {
			return nil, fmt.Errorf("read the is_active of %s %s %s: %w", r.FQDN, r.Type, r.Value, err)
		}
		have[r] = on
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the node-name records in dns_records: %w", err)
	}
	return have, nil
}

// truthy reads a boolean column the way either driver returns it.
func truthy(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case int64:
		return x != 0, nil
	case float64:
		return x != 0, nil
	case []byte:
		return truthy(string(x))
	case string:
		switch x {
		case "1", "true", "TRUE":
			return true, nil
		case "0", "false", "FALSE":
			return false, nil
		}
	}
	return false, fmt.Errorf("%v (%T) is not a boolean", v, v)
}

// diff is what must be added, reactivated and removed to make have equal want. Each list is sorted,
// so every node of the cluster does the same writes in the same order.
func diff(want []Record, have map[Record]bool) plan {
	var p plan
	wanted := make(map[Record]struct{}, len(want))
	for _, r := range want {
		wanted[r] = struct{}{}
		if active, present := have[r]; !present {
			p.add = append(p.add, r)
		} else if !active {
			p.activate = append(p.activate, r)
		}
	}
	for r := range have {
		if _, keep := wanted[r]; !keep {
			p.remove = append(p.remove, r)
		}
	}
	sortRecords(p.remove)
	return p
}

func sortRecords(rs []Record) {
	sort.Slice(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.FQDN != b.FQDN {
			return a.FQDN < b.FQDN
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Value < b.Value
	})
}

// boundRemovals returns the removals this pass may make and how many it holds back. While the chain
// node is catching up none is made; otherwise at most the larger of MinRemovalsPerPass and the
// MaxRemovalsNumerator/MaxRemovalsDenominator share of the rows held.
func boundRemovals(remove []Record, held int, catchingUp bool) ([]Record, int) {
	if catchingUp {
		return nil, len(remove)
	}
	allowed := max(MinRemovalsPerPass, held*MaxRemovalsNumerator/MaxRemovalsDenominator)
	if len(remove) <= allowed {
		return remove, 0
	}
	return remove[:allowed], len(remove) - allowed
}

// writeStep is one kind of write of a pass.
type writeStep struct {
	what    string
	rows    []Record
	sql     string
	args    func(Record) []any
	counter *int
}

func addArgs(r Record) []any {
	return []any{r.FQDN, r.Type, r.Value, RecordTTL, RecordNamespace, RecordNamespace}
}

func ownedArgs(r Record) []any { return []any{r.FQDN, r.Type, r.Value, RecordNamespace} }

// apply writes the plan, adds first, within the pass's write budget. A failed write is reported and
// does not stop the others: one row that cannot be written must not starve the rest. A cancelled
// context ends the pass at once, with one error and not one per remaining row.
func apply(ctx context.Context, db *sql.DB, p plan, stats *Stats) error {
	steps := []writeStep{
		{"add", p.add, insertSQL, addArgs, &stats.Added},
		{"reactivate", p.activate, activateSQL, ownedArgs, &stats.Reactivated},
		{"remove", p.remove, deleteSQL, ownedArgs, &stats.Removed},
	}
	var errs []error
	budget := MaxWritesPerPass
	for _, step := range steps {
		for _, r := range step.rows {
			if err := ctx.Err(); err != nil {
				return errors.Join(append(errs, fmt.Errorf("the pass ended before every row was written: %w", err))...)
			}
			if budget == 0 {
				stats.Remaining++
				continue
			}
			budget--
			if err := step.write(ctx, db, r); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (s writeStep) write(ctx context.Context, db *sql.DB, r Record) error {
	res, err := rqlite.SafeExecContext(db, ctx, s.sql, s.args(r)...)
	if err != nil {
		return fmt.Errorf("%s %s %s %s: %w", s.what, r.FQDN, r.Type, r.Value, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s %s %s %s: count the rows written: %w", s.what, r.FQDN, r.Type, r.Value, err)
	}
	if affected > 0 {
		*s.counter++
	}
	return nil
}

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

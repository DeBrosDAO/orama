package nodenames

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config/validate"
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
	// BatchSize is the rows written in one request: one transaction, one Raft log entry. A pass
	// of MaxWritesPerPass rows is at most MaxWritesPerPass/BatchSize requests.
	BatchSize = 100

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
	// Timeout bounds one pass; zero is SyncTimeout.
	Timeout time.Duration
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

// timeout is the time one pass may take.
func (s *Syncer) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return SyncTimeout
}

// Run syncs until ctx ends: once at once, then every SyncInterval, or after CatchUpDelay while a
// pass has writes left or ran out of time. report receives every pass's outcome.
func (s *Syncer) Run(ctx context.Context, report func(Stats, error)) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		passCtx, cancel := context.WithTimeout(ctx, s.timeout())
		stats, err := s.Sync(passCtx)
		// Whether the pass ran out of time is the pass context's word, read before cancel() makes
		// it Canceled. The error says so only if every layer between the driver and here wrapped
		// it, and a driver or a joined error may have flattened it to text.
		timedOut := errors.Is(passCtx.Err(), context.DeadlineExceeded)
		cancel()
		report(stats, err)
		timer.Reset(nextWait(ctx, stats, err, timedOut))
	}
}

// nextWait is the wait before the next pass: SyncInterval, unless the pass left work that a prompt
// retry would finish. That is a pass that ran out of its write budget, and a pass that failed
// because its own time ran out (rows written before the deadline stay written, and waiting a
// whole interval for the rest would leave the zone half updated). A pass that failed for another
// reason waits the interval; so does any pass once the run itself is ending.
func nextWait(run context.Context, stats Stats, err error, timedOut bool) time.Duration {
	if run.Err() != nil {
		return SyncInterval
	}
	if err == nil && stats.Remaining > 0 {
		return CatchUpDelay
	}
	if err != nil && timedOut {
		return CatchUpDelay
	}
	return SyncInterval
}

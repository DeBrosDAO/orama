package nodenames

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// What a pass writes: the plan as transactions of BatchSize rows.

// op is one row write of a pass.
type op struct {
	what    string
	rec     Record
	stmt    rqlite.Statement
	counter *int
}

func (o op) describe() string {
	return fmt.Sprintf("%s %s %s %s", o.what, o.rec.FQDN, o.rec.Type, o.rec.Value)
}

func addArgs(r Record) []any {
	return []any{r.FQDN, r.Type, r.Value, RecordTTL, RecordNamespace, RecordNamespace}
}

func ownedArgs(r Record) []any { return []any{r.FQDN, r.Type, r.Value, RecordNamespace} }

// ops lists the plan's writes, adds first, within the pass's write budget; the rest is counted in
// stats.Remaining for the next pass.
func ops(p plan, stats *Stats) []op {
	var all []op
	for _, r := range p.add {
		all = append(all, op{"add", r, rqlite.Statement{Query: insertSQL, Arguments: addArgs(r)}, &stats.Added})
	}
	for _, r := range p.activate {
		all = append(all, op{"reactivate", r, rqlite.Statement{Query: activateSQL, Arguments: ownedArgs(r)}, &stats.Reactivated})
	}
	for _, r := range p.remove {
		all = append(all, op{"remove", r, rqlite.Statement{Query: deleteSQL, Arguments: ownedArgs(r)}, &stats.Removed})
	}
	if len(all) > MaxWritesPerPass {
		stats.Remaining = len(all) - MaxWritesPerPass
		all = all[:MaxWritesPerPass]
	}
	return all
}

// apply writes the plan in transactions of at most BatchSize rows, each one request to the
// registry, adds first. A row the database rejects is reported and left out of its batch, which is
// sent again without it: one row that cannot be written must not starve the rest. A cancelled
// context or a failed request ends the pass at once, with one error and not one per remaining row.
// The counters count the rows sent: an add another node wrote first is a no-op that still counts.
func apply(ctx context.Context, db *sql.DB, p plan, stats *Stats) error {
	pending := ops(p, stats)
	var errs []error
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, fmt.Errorf("the pass ended before every row was written: %w", err))...)
		}
		n := min(BatchSize, len(pending))
		chunkErrs, err := execChunk(ctx, db, pending[:n])
		errs = append(errs, chunkErrs...)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		pending = pending[n:]
	}
	return errors.Join(errs...)
}

// execChunk sends one batch. When the database blames a statement, that row is reported and the
// batch is sent again without it; each round removes a row, so the loop ends. A fault that blames
// no row (transport, lost leader, deadline) is returned as the pass's error.
func execChunk(ctx context.Context, db *sql.DB, chunk []op) (rowErrs []error, err error) {
	for len(chunk) > 0 {
		stmts := make([]rqlite.Statement, len(chunk))
		for i, o := range chunk {
			stmts[i] = o.stmt
		}
		failed, execErr := rqlite.ExecBatch(ctx, db, stmts)
		if execErr == nil {
			for _, o := range chunk {
				*o.counter++
			}
			return rowErrs, nil
		}
		if failed < 0 {
			return rowErrs, fmt.Errorf("write %d node-name rows in one request: %w", len(chunk), execErr)
		}
		rowErrs = append(rowErrs, fmt.Errorf("%s: %w", chunk[failed].describe(), execErr))
		chunk = append(chunk[:failed:failed], chunk[failed+1:]...)
	}
	return rowErrs, nil
}

package serverless

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// MaxRetainedFunctionVersions bounds how many versions of one function the
// registry keeps. A deploy past the bound removes the oldest version, so a
// function redeployed on every commit does not grow its table without limit.
// Ten is deep enough to pin a rollback target across a release train and small
// enough that the rows of a busy function stay in the tens.
const MaxRetainedFunctionVersions = 10

// triggerTables are the tables whose rows say "run THIS function when X
// happens". They follow the current version: a deploy moves them to the new
// row, so a trigger fires the code that was just shipped and a version row is
// never the owner of a trigger the next deploy would orphan.
var triggerTables = []string{
	"function_cron_triggers",
	"function_pubsub_triggers",
	"function_db_triggers",
	"function_timers",
}

// historyTables hold what happened while a version was current. They stay with
// the version that produced them until that version is pruned, when they move to
// the oldest version kept: logs, invocations and jobs are read by function name,
// so the history of a function is not cut short by retention, and no row is left
// pointing at a deleted version (rqlite runs without foreign keys, so ON DELETE
// CASCADE never fires).
var historyTables = []string{
	"function_invocations",
	"function_logs",
	"function_jobs",
}

// versionIDRow scans one function row id.
type versionIDRow struct {
	ID string `db:"id"`
}

// adoptVersionState finishes a deploy of a new version: it moves the triggers
// the earlier versions owned onto newID, then removes the versions beyond
// MaxRetainedFunctionVersions. One transaction, so a function is never left with
// its triggers on a row about to be removed.
func (r *Registry) adoptVersionState(ctx context.Context, namespace, name, newID string) error {
	err := r.db.Tx(ctx, func(tx rqlite.Tx) error {
		for _, table := range triggerTables {
			q := `UPDATE ` + table + ` SET function_id = ? WHERE function_id IN (
				SELECT id FROM functions WHERE namespace = ? AND name = ? AND id != ?)`
			if _, err := tx.Exec(ctx, q, newID, namespace, name, newID); err != nil {
				return fmt.Errorf("failed to move %s to the new version: %w", table, err)
			}
		}
		return pruneOldVersions(ctx, tx, namespace, name)
	})
	if err != nil {
		return fmt.Errorf("failed to finish deploy of %s/%s: %w", namespace, name, err)
	}
	r.logger.Debug("Function version state adopted",
		zap.String("namespace", namespace), zap.String("name", name), zap.String("id", newID))
	return nil
}

// pruneOldVersions deletes every version of the function older than the newest
// MaxRetainedFunctionVersions, moving their invocation history to the oldest
// version kept and dropping their env vars.
func pruneOldVersions(ctx context.Context, tx rqlite.Tx, namespace, name string) error {
	var kept []versionIDRow
	if err := tx.Query(ctx, &kept, `
		SELECT id FROM functions WHERE namespace = ? AND name = ?
		ORDER BY version DESC LIMIT ?`, namespace, name, MaxRetainedFunctionVersions); err != nil {
		return fmt.Errorf("failed to list retained versions: %w", err)
	}
	if len(kept) < MaxRetainedFunctionVersions {
		return nil
	}
	oldestKept := kept[len(kept)-1].ID

	var pruned []versionIDRow
	if err := tx.Query(ctx, &pruned, `
		SELECT id FROM functions WHERE namespace = ? AND name = ?
		ORDER BY version DESC LIMIT -1 OFFSET ?`, namespace, name, MaxRetainedFunctionVersions); err != nil {
		return fmt.Errorf("failed to list versions to prune: %w", err)
	}
	for _, old := range pruned {
		for _, table := range historyTables {
			if _, err := tx.Exec(ctx, `UPDATE `+table+` SET function_id = ? WHERE function_id = ?`,
				oldestKept, old.ID); err != nil {
				return fmt.Errorf("failed to move %s of pruned version: %w", table, err)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM function_env_vars WHERE function_id = ?`, old.ID); err != nil {
			return fmt.Errorf("failed to delete env vars of pruned version: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM functions WHERE id = ?`, old.ID); err != nil {
			return fmt.Errorf("failed to delete pruned version: %w", err)
		}
	}
	return nil
}

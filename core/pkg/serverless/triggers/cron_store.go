package triggers

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CronTriggerStore manages cron-trigger persistence in RQLite. Reads/writes
// the function_cron_triggers table created by migration 004.
//
// Each row carries the cron expression, last/next run timestamps, last
// status / error, and an enabled flag — that's all the scheduler needs to
// pick due triggers up. Function metadata (name + namespace) is JOINed at
// dispatch time.
type CronTriggerStore struct {
	db     rqlite.Client
	logger *zap.Logger
}

// NewCronTriggerStore creates a new cron trigger store.
func NewCronTriggerStore(db rqlite.Client, logger *zap.Logger) *CronTriggerStore {
	return &CronTriggerStore{db: db, logger: logger}
}

// cronRow maps to function_cron_triggers for query scanning.
type cronRow struct {
	ID             string
	FunctionID     string
	CronExpression string
	NextRunAt      time.Time
	LastRunAt      *time.Time
	LastStatus     *string
	LastError      *string
	Enabled        bool
	CreatedAt      time.Time
}

// CronDueRow is the JOINed row returned by ListDue: trigger + the function
// metadata the scheduler needs to invoke it.
type CronDueRow struct {
	TriggerID      string
	FunctionID     string
	FunctionName   string
	Namespace      string
	CronExpression string
	NextRunAt      time.Time
}

// Add registers a new cron trigger. Validates the expression up front so
// callers get an immediate error instead of discovering it at scheduler
// firing time.
func (s *CronTriggerStore) Add(ctx context.Context, functionID, expr string) (string, error) {
	if functionID == "" {
		return "", fmt.Errorf("function ID required")
	}
	parsed, err := ParseCron(expr)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	next, err := parsed.Next(now)
	if err != nil {
		return "", err
	}

	id := uuid.New().String()
	query := `
		INSERT INTO function_cron_triggers
			(id, function_id, cron_expression, next_run_at, enabled, created_at)
		VALUES (?, ?, ?, ?, TRUE, ?)
	`
	if _, err := s.db.Exec(ctx, query, id, functionID, expr, next, now); err != nil {
		return "", fmt.Errorf("failed to add cron trigger: %w", err)
	}
	s.logger.Info("Cron trigger added",
		zap.String("trigger_id", id),
		zap.String("function_id", functionID),
		zap.String("expr", expr),
		zap.Time("next_run_at", next),
	)
	return id, nil
}

// Remove deletes a trigger by ID.
func (s *CronTriggerStore) Remove(ctx context.Context, triggerID string) error {
	if triggerID == "" {
		return fmt.Errorf("trigger ID required")
	}
	query := `DELETE FROM function_cron_triggers WHERE id = ?`
	res, err := s.db.Exec(ctx, query, triggerID)
	if err != nil {
		return fmt.Errorf("failed to remove cron trigger: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return fmt.Errorf("trigger not found: %s", triggerID)
	}
	return nil
}

// RemoveByFunction wipes every cron trigger for a function. Used during
// re-deploy so stale schedules don't survive a function rewrite.
func (s *CronTriggerStore) RemoveByFunction(ctx context.Context, functionID string) error {
	if functionID == "" {
		return fmt.Errorf("function ID required")
	}
	_, err := s.db.Exec(ctx, `DELETE FROM function_cron_triggers WHERE function_id = ?`, functionID)
	return err
}

// ListByFunction returns every cron trigger for a function. Used by the
// CLI's `triggers list` and the gateway's HandleListTriggers.
func (s *CronTriggerStore) ListByFunction(ctx context.Context, functionID string) ([]serverless.CronTrigger, error) {
	if functionID == "" {
		return nil, fmt.Errorf("function ID required")
	}
	query := `
		SELECT id, function_id, cron_expression, next_run_at, last_run_at,
		       last_status, last_error, enabled, created_at
		FROM function_cron_triggers
		WHERE function_id = ?
	`
	var rows []cronRow
	if err := s.db.Query(ctx, &rows, query, functionID); err != nil {
		return nil, fmt.Errorf("failed to list cron triggers: %w", err)
	}
	out := make([]serverless.CronTrigger, len(rows))
	for i, r := range rows {
		nextRunAt := r.NextRunAt // copy so &local doesn't capture the loop var
		t := serverless.CronTrigger{
			ID:             r.ID,
			FunctionID:     r.FunctionID,
			CronExpression: r.CronExpression,
			NextRunAt:      &nextRunAt,
			Enabled:        r.Enabled,
		}
		if r.LastRunAt != nil {
			lastRunAt := *r.LastRunAt
			t.LastRunAt = &lastRunAt
		}
		out[i] = t
	}
	return out, nil
}

// ListDue returns enabled triggers whose next_run_at has elapsed, joined
// with the owning function so the scheduler can invoke them without an
// extra registry round-trip per trigger. Bounded by `limit` to avoid
// unbounded scan of a backlog after a long outage.
func (s *CronTriggerStore) ListDue(ctx context.Context, now time.Time, limit int) ([]CronDueRow, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT t.id AS trigger_id, t.function_id AS function_id,
		       f.name AS function_name, f.namespace AS namespace,
		       t.cron_expression AS cron_expression, t.next_run_at AS next_run_at
		FROM function_cron_triggers t
		JOIN functions f ON t.function_id = f.id
		WHERE t.enabled = TRUE
		  AND f.status = 'active'
		  AND t.next_run_at <= ?
		ORDER BY t.next_run_at ASC
		LIMIT ?
	`
	var rows []CronDueRow
	if err := s.db.Query(ctx, &rows, query, now, limit); err != nil {
		return nil, fmt.Errorf("failed to query due cron triggers: %w", err)
	}
	return rows, nil
}

// MarkRun updates next_run_at + last_run_at + last_status / last_error
// after the scheduler invokes a trigger. Idempotent: if the row was
// removed concurrently, the UPDATE is a no-op.
func (s *CronTriggerStore) MarkRun(
	ctx context.Context,
	triggerID string,
	ranAt time.Time,
	nextRunAt time.Time,
	status string,
	errMsg string,
) error {
	query := `
		UPDATE function_cron_triggers
		SET last_run_at = ?, next_run_at = ?, last_status = ?, last_error = ?
		WHERE id = ?
	`
	_, err := s.db.Exec(ctx, query, ranAt, nextRunAt, status, errMsg, triggerID)
	return err
}

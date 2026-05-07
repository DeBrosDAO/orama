package registry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// InvocationLogger handles logging of function invocations and their logs.
type InvocationLogger struct {
	db     rqlite.Client
	logger *zap.Logger
}

// NewInvocationLogger creates a new invocation logger.
func NewInvocationLogger(db rqlite.Client, logger *zap.Logger) *InvocationLogger {
	return &InvocationLogger{
		db:     db,
		logger: logger,
	}
}

// Log records a function invocation and its logs to the database.
func (l *InvocationLogger) Log(ctx context.Context, inv *InvocationRecordData) error {
	if inv == nil {
		return nil
	}

	invQuery := `
		INSERT INTO function_invocations (
			id, function_id, request_id, trigger_type, caller_wallet,
			input_size, output_size, started_at, completed_at,
			duration_ms, status, error_message, memory_used_mb
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := l.db.Exec(ctx, invQuery,
		inv.ID, inv.FunctionID, inv.RequestID, inv.TriggerType, inv.CallerWallet,
		inv.InputSize, inv.OutputSize, inv.StartedAt, inv.CompletedAt,
		inv.DurationMS, inv.Status, inv.ErrorMessage, inv.MemoryUsedMB,
	)
	if err != nil {
		return fmt.Errorf("failed to insert invocation record: %w", err)
	}

	if len(inv.Logs) > 0 {
		for _, entry := range inv.Logs {
			logID := uuid.New().String()
			logQuery := `
				INSERT INTO function_logs (
					id, function_id, invocation_id, level, message, timestamp
				) VALUES (?, ?, ?, ?, ?, ?)
			`
			_, err := l.db.Exec(ctx, logQuery,
				logID, inv.FunctionID, inv.ID, entry.Level, entry.Message, entry.Timestamp,
			)
			if err != nil {
				l.logger.Warn("Failed to insert function log", zap.Error(err))
			}
		}
	}

	return nil
}

// GetLogs retrieves WASM-emitted log entries for a function (rows in
// function_logs). Functions that don't call log_info / log_error from
// their WASM code will return an empty slice here — that's expected.
//
// For "what happened when this function was invoked" use GetInvocations
// instead; it always populates as long as the function has been invoked.
func (l *InvocationLogger) GetLogs(ctx context.Context, namespace, name string, limit int) ([]LogEntry, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT l.level, l.message, l.timestamp
		FROM function_logs l
		JOIN functions f ON l.function_id = f.id
		WHERE f.namespace = ? AND f.name = ?
		ORDER BY l.timestamp DESC
		LIMIT ?
	`

	var results []struct {
		Level     string    `db:"level"`
		Message   string    `db:"message"`
		Timestamp time.Time `db:"timestamp"`
	}

	if err := l.db.Query(ctx, &results, query, namespace, name, limit); err != nil {
		return nil, fmt.Errorf("failed to query logs: %w", err)
	}

	logs := make([]LogEntry, len(results))
	for i, res := range results {
		logs[i] = LogEntry{
			Level:     res.Level,
			Message:   res.Message,
			Timestamp: res.Timestamp,
		}
	}

	return logs, nil
}

// GetInvocations returns invocation history for a function in reverse
// chronological order, with any associated WASM log entries nested per
// record under WASMLogs.
//
// This is what `orama function logs <name>` displays — it's always
// populated as long as the function has been invoked at least once.
// Returns an empty slice (not an error) when there are no invocations.
//
// Implementation: two queries — first the invocation rows, then a
// single batched query for all WASM log entries belonging to those
// invocation IDs. We don't LEFT JOIN because that would return one
// row per (invocation × log entry) which is awkward to scan.
func (l *InvocationLogger) GetInvocations(ctx context.Context, namespace, name string, limit int) ([]Invocation, error) {
	if limit <= 0 {
		limit = 50
	}

	invQuery := `
		SELECT i.id, i.request_id, i.trigger_type, i.caller_wallet,
			i.input_size, i.output_size, i.started_at, i.completed_at,
			i.duration_ms, i.status, i.error_message, i.memory_used_mb
		FROM function_invocations i
		JOIN functions f ON i.function_id = f.id
		WHERE f.namespace = ? AND f.name = ?
		ORDER BY i.started_at DESC
		LIMIT ?
	`

	var rows []struct {
		ID           string    `db:"id"`
		RequestID    string    `db:"request_id"`
		TriggerType  string    `db:"trigger_type"`
		CallerWallet string    `db:"caller_wallet"`
		InputSize    int       `db:"input_size"`
		OutputSize   int       `db:"output_size"`
		StartedAt    time.Time `db:"started_at"`
		CompletedAt  time.Time `db:"completed_at"`
		DurationMS   int64     `db:"duration_ms"`
		Status       string    `db:"status"`
		ErrorMessage string    `db:"error_message"`
		MemoryUsedMB float64   `db:"memory_used_mb"`
	}
	if err := l.db.Query(ctx, &rows, invQuery, namespace, name, limit); err != nil {
		return nil, fmt.Errorf("failed to query invocations: %w", err)
	}
	if len(rows) == 0 {
		return []Invocation{}, nil
	}

	// Batched fetch of WASM log entries for these invocation IDs.
	// We use IN (?, ?, ...) with one placeholder per ID. This stays
	// fast because limit is bounded (default 50).
	invIDs := make([]string, len(rows))
	for i, r := range rows {
		invIDs[i] = r.ID
	}
	logsByInv, err := l.fetchLogsForInvocations(ctx, invIDs)
	if err != nil {
		// Don't fail the whole call if WASM-log fetch fails; the
		// invocation summary is still useful. Log and continue.
		l.logger.Warn("failed to fetch nested WASM logs; returning invocations without them",
			zap.Error(err))
		logsByInv = map[string][]LogEntry{}
	}

	out := make([]Invocation, len(rows))
	for i, r := range rows {
		out[i] = Invocation{
			ID:           r.ID,
			RequestID:    r.RequestID,
			TriggerType:  r.TriggerType,
			CallerWallet: r.CallerWallet,
			InputSize:    r.InputSize,
			OutputSize:   r.OutputSize,
			StartedAt:    r.StartedAt,
			CompletedAt:  r.CompletedAt,
			DurationMS:   r.DurationMS,
			Status:       r.Status,
			ErrorMessage: r.ErrorMessage,
			MemoryUsedMB: r.MemoryUsedMB,
			WASMLogs:     logsByInv[r.ID],
		}
	}
	return out, nil
}

// fetchLogsForInvocations returns a map of invocation_id → []LogEntry,
// fetching all entries in one query.
func (l *InvocationLogger) fetchLogsForInvocations(ctx context.Context, invocationIDs []string) (map[string][]LogEntry, error) {
	if len(invocationIDs) == 0 {
		return map[string][]LogEntry{}, nil
	}
	placeholders := strings.Repeat("?,", len(invocationIDs))
	placeholders = placeholders[:len(placeholders)-1]
	query := `
		SELECT invocation_id, level, message, timestamp
		FROM function_logs
		WHERE invocation_id IN (` + placeholders + `)
		ORDER BY timestamp ASC
	`

	args := make([]interface{}, len(invocationIDs))
	for i, id := range invocationIDs {
		args[i] = id
	}

	var rows []struct {
		InvocationID string    `db:"invocation_id"`
		Level        string    `db:"level"`
		Message      string    `db:"message"`
		Timestamp    time.Time `db:"timestamp"`
	}
	if err := l.db.Query(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("failed to query logs: %w", err)
	}

	out := make(map[string][]LogEntry, len(invocationIDs))
	for _, r := range rows {
		out[r.InvocationID] = append(out[r.InvocationID], LogEntry{
			Level:     r.Level,
			Message:   r.Message,
			Timestamp: r.Timestamp,
		})
	}
	return out, nil
}


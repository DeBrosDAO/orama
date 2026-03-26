package registry

import (
	"context"
	"fmt"
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

// GetLogs retrieves logs for a function.
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

package serverless

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Ensure DBTriggerManager implements TriggerManager interface.
var _ TriggerManager = (*DBTriggerManager)(nil)

// DBTriggerManager manages function triggers using RQLite database.
type DBTriggerManager struct {
	db     rqlite.Client
	logger *zap.Logger
}

// NewDBTriggerManager creates a new trigger manager.
func NewDBTriggerManager(db rqlite.Client, logger *zap.Logger) *DBTriggerManager {
	return &DBTriggerManager{
		db:     db,
		logger: logger,
	}
}

// pubsubTriggerRow represents a row from the function_pubsub_triggers table.
type pubsubTriggerRow struct {
	ID         string    `db:"id"`
	FunctionID string    `db:"function_id"`
	Topic      string    `db:"topic"`
	Enabled    bool      `db:"enabled"`
	CreatedAt  time.Time `db:"created_at"`
}

// AddPubSubTrigger adds a pubsub trigger to a function.
func (m *DBTriggerManager) AddPubSubTrigger(ctx context.Context, functionID, topic string) error {
	functionID = strings.TrimSpace(functionID)
	topic = strings.TrimSpace(topic)

	if functionID == "" {
		return &ValidationError{Field: "function_id", Message: "cannot be empty"}
	}
	if topic == "" {
		return &ValidationError{Field: "topic", Message: "cannot be empty"}
	}

	// Check if trigger already exists for this function and topic
	var existing []pubsubTriggerRow
	checkQuery := `SELECT id FROM function_pubsub_triggers WHERE function_id = ? AND topic = ? AND enabled = TRUE`
	if err := m.db.Query(ctx, &existing, checkQuery, functionID, topic); err != nil {
		return fmt.Errorf("failed to check existing trigger: %w", err)
	}
	if len(existing) > 0 {
		return &ValidationError{Field: "topic", Message: "trigger already exists for this topic"}
	}

	// Generate trigger ID
	triggerID := "trig_" + uuid.New().String()[:8]

	// Insert trigger
	query := `INSERT INTO function_pubsub_triggers (id, function_id, topic, enabled, created_at) VALUES (?, ?, ?, TRUE, ?)`
	_, err := m.db.Exec(ctx, query, triggerID, functionID, topic, time.Now())
	if err != nil {
		return fmt.Errorf("failed to create pubsub trigger: %w", err)
	}

	m.logger.Info("PubSub trigger created",
		zap.String("trigger_id", triggerID),
		zap.String("function_id", functionID),
		zap.String("topic", topic),
	)

	return nil
}

// AddPubSubTriggerWithID adds a pubsub trigger and returns the trigger ID.
func (m *DBTriggerManager) AddPubSubTriggerWithID(ctx context.Context, functionID, topic string) (string, error) {
	functionID = strings.TrimSpace(functionID)
	topic = strings.TrimSpace(topic)

	if functionID == "" {
		return "", &ValidationError{Field: "function_id", Message: "cannot be empty"}
	}
	if topic == "" {
		return "", &ValidationError{Field: "topic", Message: "cannot be empty"}
	}

	// Check if trigger already exists for this function and topic
	var existing []pubsubTriggerRow
	checkQuery := `SELECT id FROM function_pubsub_triggers WHERE function_id = ? AND topic = ? AND enabled = TRUE`
	if err := m.db.Query(ctx, &existing, checkQuery, functionID, topic); err != nil {
		return "", fmt.Errorf("failed to check existing trigger: %w", err)
	}
	if len(existing) > 0 {
		return "", &ValidationError{Field: "topic", Message: "trigger already exists for this topic"}
	}

	// Generate trigger ID
	triggerID := "trig_" + uuid.New().String()[:8]

	// Insert trigger
	query := `INSERT INTO function_pubsub_triggers (id, function_id, topic, enabled, created_at) VALUES (?, ?, ?, TRUE, ?)`
	_, err := m.db.Exec(ctx, query, triggerID, functionID, topic, time.Now())
	if err != nil {
		return "", fmt.Errorf("failed to create pubsub trigger: %w", err)
	}

	m.logger.Info("PubSub trigger created",
		zap.String("trigger_id", triggerID),
		zap.String("function_id", functionID),
		zap.String("topic", topic),
	)

	return triggerID, nil
}

// AddCronTrigger adds a cron-based trigger to a function.
func (m *DBTriggerManager) AddCronTrigger(ctx context.Context, functionID, cronExpr string) error {
	// TODO: Implement cron trigger support
	return fmt.Errorf("cron triggers not yet implemented")
}

// AddDBTrigger adds a database trigger to a function.
func (m *DBTriggerManager) AddDBTrigger(ctx context.Context, functionID, tableName string, operation DBOperation, condition string) error {
	// TODO: Implement database trigger support
	return fmt.Errorf("database triggers not yet implemented")
}

// ScheduleOnce schedules a one-time execution.
func (m *DBTriggerManager) ScheduleOnce(ctx context.Context, functionID string, runAt time.Time, payload []byte) (string, error) {
	// TODO: Implement one-time timer support
	return "", fmt.Errorf("one-time timers not yet implemented")
}

// RemoveTrigger removes a trigger by ID.
func (m *DBTriggerManager) RemoveTrigger(ctx context.Context, triggerID string) error {
	triggerID = strings.TrimSpace(triggerID)
	if triggerID == "" {
		return &ValidationError{Field: "trigger_id", Message: "cannot be empty"}
	}

	// Try to delete from pubsub triggers first
	query := `DELETE FROM function_pubsub_triggers WHERE id = ?`
	result, err := m.db.Exec(ctx, query, triggerID)
	if err != nil {
		return fmt.Errorf("failed to remove trigger: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return ErrTriggerNotFound
	}

	m.logger.Info("Trigger removed", zap.String("trigger_id", triggerID))
	return nil
}

// ListPubSubTriggers returns all pubsub triggers for a function.
func (m *DBTriggerManager) ListPubSubTriggers(ctx context.Context, functionID string) ([]PubSubTrigger, error) {
	functionID = strings.TrimSpace(functionID)
	if functionID == "" {
		return nil, &ValidationError{Field: "function_id", Message: "cannot be empty"}
	}

	query := `SELECT id, function_id, topic, enabled FROM function_pubsub_triggers WHERE function_id = ? AND enabled = TRUE`
	var rows []pubsubTriggerRow
	if err := m.db.Query(ctx, &rows, query, functionID); err != nil {
		return nil, fmt.Errorf("failed to list pubsub triggers: %w", err)
	}

	triggers := make([]PubSubTrigger, len(rows))
	for i, row := range rows {
		triggers[i] = PubSubTrigger{
			ID:         row.ID,
			FunctionID: row.FunctionID,
			Topic:      row.Topic,
			Enabled:    row.Enabled,
		}
	}

	return triggers, nil
}

// GetTriggersByTopic returns all enabled triggers for a specific topic.
func (m *DBTriggerManager) GetTriggersByTopic(ctx context.Context, topic string) ([]PubSubTrigger, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, &ValidationError{Field: "topic", Message: "cannot be empty"}
	}

	query := `SELECT id, function_id, topic, enabled FROM function_pubsub_triggers WHERE topic = ? AND enabled = TRUE`
	var rows []pubsubTriggerRow
	if err := m.db.Query(ctx, &rows, query, topic); err != nil {
		return nil, fmt.Errorf("failed to get triggers by topic: %w", err)
	}

	triggers := make([]PubSubTrigger, len(rows))
	for i, row := range rows {
		triggers[i] = PubSubTrigger{
			ID:         row.ID,
			FunctionID: row.FunctionID,
			Topic:      row.Topic,
			Enabled:    row.Enabled,
		}
	}

	return triggers, nil
}

// GetPubSubTrigger returns a specific pubsub trigger by ID.
func (m *DBTriggerManager) GetPubSubTrigger(ctx context.Context, triggerID string) (*PubSubTrigger, error) {
	triggerID = strings.TrimSpace(triggerID)
	if triggerID == "" {
		return nil, &ValidationError{Field: "trigger_id", Message: "cannot be empty"}
	}

	query := `SELECT id, function_id, topic, enabled FROM function_pubsub_triggers WHERE id = ?`
	var rows []pubsubTriggerRow
	if err := m.db.Query(ctx, &rows, query, triggerID); err != nil {
		return nil, fmt.Errorf("failed to get trigger: %w", err)
	}

	if len(rows) == 0 {
		return nil, ErrTriggerNotFound
	}

	row := rows[0]
	return &PubSubTrigger{
		ID:         row.ID,
		FunctionID: row.FunctionID,
		Topic:      row.Topic,
		Enabled:    row.Enabled,
	}, nil
}

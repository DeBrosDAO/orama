// Package triggers provides PubSub trigger management for the serverless engine.
// It handles registering, querying, and removing triggers that automatically invoke
// functions when messages are published to specific PubSub topics.
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

// TriggerMatch contains the fields needed to dispatch a trigger invocation.
// It's the result of JOINing function_pubsub_triggers with functions.
type TriggerMatch struct {
	TriggerID    string
	FunctionID   string
	FunctionName string
	Namespace    string
	Topic        string
}

// triggerRow maps to the function_pubsub_triggers table for query scanning.
type triggerRow struct {
	ID         string
	FunctionID string
	Topic      string
	Enabled    bool
	CreatedAt  time.Time
}

// triggerMatchRow maps to the JOIN query result for scanning.
type triggerMatchRow struct {
	TriggerID    string
	FunctionID   string
	FunctionName string
	Namespace    string
	Topic        string
}

// PubSubTriggerStore manages PubSub trigger persistence in RQLite.
type PubSubTriggerStore struct {
	db     rqlite.Client
	logger *zap.Logger
}

// NewPubSubTriggerStore creates a new PubSub trigger store.
func NewPubSubTriggerStore(db rqlite.Client, logger *zap.Logger) *PubSubTriggerStore {
	return &PubSubTriggerStore{
		db:     db,
		logger: logger,
	}
}

// Add registers a new PubSub trigger for a function.
// Returns the trigger ID.
func (s *PubSubTriggerStore) Add(ctx context.Context, functionID, topic string) (string, error) {
	if functionID == "" {
		return "", fmt.Errorf("function ID required")
	}
	if topic == "" {
		return "", fmt.Errorf("topic required")
	}

	id := uuid.New().String()
	now := time.Now()

	query := `
		INSERT INTO function_pubsub_triggers (id, function_id, topic, enabled, created_at)
		VALUES (?, ?, ?, TRUE, ?)
	`
	if _, err := s.db.Exec(ctx, query, id, functionID, topic, now); err != nil {
		return "", fmt.Errorf("failed to add pubsub trigger: %w", err)
	}

	s.logger.Info("PubSub trigger added",
		zap.String("trigger_id", id),
		zap.String("function_id", functionID),
		zap.String("topic", topic),
	)

	return id, nil
}

// Remove deletes a trigger by ID.
func (s *PubSubTriggerStore) Remove(ctx context.Context, triggerID string) error {
	if triggerID == "" {
		return fmt.Errorf("trigger ID required")
	}

	query := `DELETE FROM function_pubsub_triggers WHERE id = ?`
	result, err := s.db.Exec(ctx, query, triggerID)
	if err != nil {
		return fmt.Errorf("failed to remove trigger: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("trigger not found: %s", triggerID)
	}

	s.logger.Info("PubSub trigger removed", zap.String("trigger_id", triggerID))
	return nil
}

// RemoveByFunction deletes all triggers for a function.
// Used during function re-deploy to clear old triggers.
func (s *PubSubTriggerStore) RemoveByFunction(ctx context.Context, functionID string) error {
	if functionID == "" {
		return fmt.Errorf("function ID required")
	}

	query := `DELETE FROM function_pubsub_triggers WHERE function_id = ?`
	if _, err := s.db.Exec(ctx, query, functionID); err != nil {
		return fmt.Errorf("failed to remove triggers for function: %w", err)
	}

	return nil
}

// ListByFunction returns all PubSub triggers for a function.
func (s *PubSubTriggerStore) ListByFunction(ctx context.Context, functionID string) ([]serverless.PubSubTrigger, error) {
	if functionID == "" {
		return nil, fmt.Errorf("function ID required")
	}

	query := `
		SELECT id, function_id, topic, enabled, created_at
		FROM function_pubsub_triggers
		WHERE function_id = ?
	`

	var rows []triggerRow
	if err := s.db.Query(ctx, &rows, query, functionID); err != nil {
		return nil, fmt.Errorf("failed to list triggers: %w", err)
	}

	triggers := make([]serverless.PubSubTrigger, len(rows))
	for i, row := range rows {
		triggers[i] = serverless.PubSubTrigger{
			ID:         row.ID,
			FunctionID: row.FunctionID,
			Topic:      row.Topic,
			Enabled:    row.Enabled,
		}
	}

	return triggers, nil
}

// GetByTopicAndNamespace returns all enabled triggers for a topic within a namespace.
// Only returns triggers for active functions.
func (s *PubSubTriggerStore) GetByTopicAndNamespace(ctx context.Context, topic, namespace string) ([]TriggerMatch, error) {
	if topic == "" || namespace == "" {
		return nil, nil
	}

	query := `
		SELECT t.id AS trigger_id, t.function_id AS function_id,
			f.name AS function_name, f.namespace AS namespace, t.topic AS topic
		FROM function_pubsub_triggers t
		JOIN functions f ON t.function_id = f.id
		WHERE t.topic = ? AND f.namespace = ? AND t.enabled = TRUE AND f.status = 'active'
	`

	var rows []triggerMatchRow
	if err := s.db.Query(ctx, &rows, query, topic, namespace); err != nil {
		return nil, fmt.Errorf("failed to query triggers for topic: %w", err)
	}

	matches := make([]TriggerMatch, len(rows))
	for i, row := range rows {
		matches[i] = TriggerMatch{
			TriggerID:    row.TriggerID,
			FunctionID:   row.FunctionID,
			FunctionName: row.FunctionName,
			Namespace:    row.Namespace,
			Topic:        row.Topic,
		}
	}

	return matches, nil
}

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
//
// Topic is the *resolved* topic that the published message was sent to,
// not the pattern stored in the trigger. This lets aggregating functions
// see which concrete topic each event came from.
//
// AggregationWindowMs > 0 indicates the dispatcher should buffer events
// instead of invoking the function per event.
type TriggerMatch struct {
	TriggerID    string
	FunctionID   string
	FunctionName string
	Namespace    string
	Topic        string
	// TopicPattern is the trigger's stored pattern (may be a glob).
	// Carried alongside the resolved Topic so callers like
	// PubSubDispatcher.DispatchLocalPublish can distinguish wildcard
	// matches from concrete-topic matches WITHOUT a second lookup
	// (used to avoid double-firing concrete triggers that already get
	// delivered via the libp2p subscribe-loopback path).
	TopicPattern            string
	AggregationWindowMs     int
	AggregationMaxBatchSize int
}

// triggerRow maps to the function_pubsub_triggers table for query scanning.
type triggerRow struct {
	ID                      string
	FunctionID              string
	TopicPattern            string
	Enabled                 bool
	CreatedAt               time.Time
	AggregationWindowMs     int
	AggregationMaxBatchSize int
}

// triggerMatchRow maps to the JOIN query result for scanning.
type triggerMatchRow struct {
	TriggerID               string
	FunctionID              string
	FunctionName            string
	Namespace               string
	TopicPattern            string
	AggregationWindowMs     int
	AggregationMaxBatchSize int
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
// `topicPattern` may be an exact topic or a SQLite GLOB pattern (e.g. "presence:*").
// Returns the trigger ID.
//
// For backward compatibility, aggregation defaults to disabled (windowMs=0).
// Use AddWithAggregation to opt in.
func (s *PubSubTriggerStore) Add(ctx context.Context, functionID, topicPattern string) (string, error) {
	return s.AddWithAggregation(ctx, functionID, topicPattern, 0, 0)
}

// AddWithAggregation registers a trigger with optional aggregation.
//   - aggregationWindowMs = 0 disables aggregation (per-event invocation, default).
//   - aggregationMaxBatchSize = 0 uses the default (100) when aggregation is enabled.
func (s *PubSubTriggerStore) AddWithAggregation(
	ctx context.Context,
	functionID, topicPattern string,
	aggregationWindowMs, aggregationMaxBatchSize int,
) (string, error) {
	if functionID == "" {
		return "", fmt.Errorf("function ID required")
	}
	if err := ValidatePattern(topicPattern); err != nil {
		return "", fmt.Errorf("invalid topic pattern: %w", err)
	}
	if aggregationWindowMs < 0 || aggregationWindowMs > 60_000 {
		return "", fmt.Errorf("aggregation_window_ms must be between 0 and 60000")
	}
	if aggregationMaxBatchSize < 0 || aggregationMaxBatchSize > 1000 {
		return "", fmt.Errorf("aggregation_max_batch_size must be between 0 and 1000")
	}
	if aggregationWindowMs > 0 && aggregationMaxBatchSize == 0 {
		aggregationMaxBatchSize = 100
	}

	id := uuid.New().String()
	now := time.Now()

	// Write both `topic` (legacy) and `topic_pattern` (new). Keeping `topic`
	// populated lets old binaries running concurrently during a rolling
	// upgrade continue reading triggers. A future migration drops `topic`.
	query := `
		INSERT INTO function_pubsub_triggers (id, function_id, topic, topic_pattern, enabled, created_at, aggregation_window_ms, aggregation_max_batch_size)
		VALUES (?, ?, ?, ?, TRUE, ?, ?, ?)
	`
	if _, err := s.db.Exec(ctx, query, id, functionID, topicPattern, topicPattern, now, aggregationWindowMs, aggregationMaxBatchSize); err != nil {
		return "", fmt.Errorf("failed to add pubsub trigger: %w", err)
	}

	s.logger.Info("PubSub trigger added",
		zap.String("trigger_id", id),
		zap.String("function_id", functionID),
		zap.String("topic_pattern", topicPattern),
		zap.Bool("wildcard", IsWildcard(topicPattern)),
		zap.Int("aggregation_window_ms", aggregationWindowMs),
		zap.Int("aggregation_max_batch_size", aggregationMaxBatchSize),
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
		SELECT id, function_id, topic_pattern, enabled, created_at, aggregation_window_ms, aggregation_max_batch_size
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
			ID:                      row.ID,
			FunctionID:              row.FunctionID,
			Topic:                   row.TopicPattern,
			Enabled:                 row.Enabled,
			AggregationWindowMs:     row.AggregationWindowMs,
			AggregationMaxBatchSize: row.AggregationMaxBatchSize,
		}
	}

	return triggers, nil
}

// DistinctTopicSubscription is a (namespace, topic_pattern) pair used by
// the dispatcher to know which libp2p pubsub topics to subscribe to.
// Wildcard patterns are flagged so the caller can skip subscribing (libp2p
// has no native wildcard support — see bugboard #282 implementation notes).
type DistinctTopicSubscription struct {
	Namespace    string
	TopicPattern string
	Wildcard     bool
}

// ListDistinctTopicPatterns returns the unique (namespace, topic_pattern)
// pairs across all enabled triggers attached to active functions. Used by
// PubSubDispatcher.Start to decide which libp2p pubsub topics to subscribe
// to so WASM-published events actually reach trigger handlers (bugboard
// #282 — dispatcher previously only fired from HTTP publishes, so WASM
// publishes from message-create silently dropped every handler invocation).
//
// The dispatcher subscribes to each NON-wildcard pattern at startup and on
// trigger add/remove. Wildcard patterns are returned with Wildcard=true so
// callers can log/skip them — handling those cross-node properly requires
// a different mechanism (per-namespace fan-out topic or publish-side hook)
// that's not in scope for this fix.
func (s *PubSubTriggerStore) ListDistinctTopicPatterns(ctx context.Context) ([]DistinctTopicSubscription, error) {
	query := `
		SELECT DISTINCT f.namespace AS namespace, t.topic_pattern AS topic_pattern
		FROM function_pubsub_triggers t
		JOIN functions f ON t.function_id = f.id
		WHERE t.enabled = TRUE AND f.status = 'active'
		ORDER BY f.namespace, t.topic_pattern
	`
	var rows []struct {
		Namespace    string
		TopicPattern string
	}
	if err := s.db.Query(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("ListDistinctTopicPatterns: %w", err)
	}
	out := make([]DistinctTopicSubscription, 0, len(rows))
	for _, r := range rows {
		out = append(out, DistinctTopicSubscription{
			Namespace:    r.Namespace,
			TopicPattern: r.TopicPattern,
			Wildcard:     IsWildcard(r.TopicPattern),
		})
	}
	return out, nil
}

// GetByTopicAndNamespace returns all enabled triggers whose topic_pattern
// matches `topic` within the namespace. Patterns are SQLite GLOB; the
// post-filter enforces stricter segment-aware semantics.
// Only triggers for active functions are returned.
func (s *PubSubTriggerStore) GetByTopicAndNamespace(ctx context.Context, topic, namespace string) ([]TriggerMatch, error) {
	if topic == "" || namespace == "" {
		return nil, nil
	}

	query := `
		SELECT t.id AS trigger_id, t.function_id AS function_id,
			f.name AS function_name, f.namespace AS namespace, t.topic_pattern AS topic_pattern,
			t.aggregation_window_ms AS aggregation_window_ms,
			t.aggregation_max_batch_size AS aggregation_max_batch_size
		FROM function_pubsub_triggers t
		JOIN functions f ON t.function_id = f.id
		WHERE ? GLOB t.topic_pattern AND f.namespace = ? AND t.enabled = TRUE AND f.status = 'active'
	`

	var rows []triggerMatchRow
	if err := s.db.Query(ctx, &rows, query, topic, namespace); err != nil {
		return nil, fmt.Errorf("failed to query triggers for topic: %w", err)
	}

	matches := make([]TriggerMatch, 0, len(rows))
	for _, row := range rows {
		// Post-filter to enforce strict segment boundaries on '*'.
		if !PatternMatches(row.TopicPattern, topic) {
			continue
		}
		matches = append(matches, TriggerMatch{
			TriggerID:               row.TriggerID,
			FunctionID:              row.FunctionID,
			FunctionName:            row.FunctionName,
			Namespace:               row.Namespace,
			Topic:                   topic, // resolved topic, not the pattern
			TopicPattern:            row.TopicPattern,
			AggregationWindowMs:     row.AggregationWindowMs,
			AggregationMaxBatchSize: row.AggregationMaxBatchSize,
		})
	}

	return matches, nil
}

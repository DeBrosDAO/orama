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

// FunctionStore handles database operations for function metadata.
type FunctionStore struct {
	db        rqlite.Client
	logger    *zap.Logger
	tableName string
}

// NewFunctionStore creates a new function store.
func NewFunctionStore(db rqlite.Client, logger *zap.Logger) *FunctionStore {
	return &FunctionStore{
		db:        db,
		logger:    logger,
		tableName: "functions",
	}
}

// Save inserts or updates a function in the database.
func (s *FunctionStore) Save(ctx context.Context, fn *FunctionDefinition, wasmCID string, existingFunc *Function) (*Function, error) {
	memoryLimit := fn.MemoryLimitMB
	if memoryLimit == 0 {
		memoryLimit = 64
	}
	timeout := fn.TimeoutSeconds
	if timeout == 0 {
		timeout = 30
	}
	retryDelay := fn.RetryDelaySeconds
	if retryDelay == 0 {
		retryDelay = 5
	}

	now := time.Now()
	id := uuid.New().String()
	version := 1

	if existingFunc != nil {
		id = existingFunc.ID
		version = existingFunc.Version + 1
	}

	query := `
		INSERT OR REPLACE INTO functions (
			id, name, namespace, version, wasm_cid,
			memory_limit_mb, timeout_seconds, is_public,
			retry_count, retry_delay_seconds, dlq_topic,
			status, created_at, updated_at, created_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(ctx, query,
		id, fn.Name, fn.Namespace, version, wasmCID,
		memoryLimit, timeout, fn.IsPublic,
		fn.RetryCount, retryDelay, fn.DLQTopic,
		string(FunctionStatusActive), now, now, fn.Namespace,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to save function: %w", err)
	}

	s.logger.Info("Function saved",
		zap.String("id", id),
		zap.String("name", fn.Name),
		zap.String("namespace", fn.Namespace),
		zap.String("wasm_cid", wasmCID),
		zap.Int("version", version),
		zap.Bool("updated", existingFunc != nil),
	)

	return &Function{
		ID:                id,
		Name:              fn.Name,
		Namespace:         fn.Namespace,
		Version:           version,
		WASMCID:           wasmCID,
		MemoryLimitMB:     memoryLimit,
		TimeoutSeconds:    timeout,
		IsPublic:          fn.IsPublic,
		RetryCount:        fn.RetryCount,
		RetryDelaySeconds: retryDelay,
		DLQTopic:          fn.DLQTopic,
		Status:            FunctionStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
		CreatedBy:         fn.Namespace,
	}, nil
}

// Get retrieves a function by name and optional version.
func (s *FunctionStore) Get(ctx context.Context, namespace, name string, version int) (*Function, error) {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)

	var query string
	var args []interface{}

	if version == 0 {
		query = `
			SELECT id, name, namespace, version, wasm_cid, source_cid,
				memory_limit_mb, timeout_seconds, is_public,
				retry_count, retry_delay_seconds, dlq_topic,
				status, created_at, updated_at, created_by
			FROM functions
			WHERE namespace = ? AND name = ? AND status = ?
			ORDER BY version DESC
			LIMIT 1
		`
		args = []interface{}{namespace, name, string(FunctionStatusActive)}
	} else {
		query = `
			SELECT id, name, namespace, version, wasm_cid, source_cid,
				memory_limit_mb, timeout_seconds, is_public,
				retry_count, retry_delay_seconds, dlq_topic,
				status, created_at, updated_at, created_by
			FROM functions
			WHERE namespace = ? AND name = ? AND version = ?
		`
		args = []interface{}{namespace, name, version}
	}

	var functions []functionRow
	if err := s.db.Query(ctx, &functions, query, args...); err != nil {
		return nil, fmt.Errorf("failed to query function: %w", err)
	}

	if len(functions) == 0 {
		if version == 0 {
			return nil, ErrFunctionNotFound
		}
		return nil, ErrVersionNotFound
	}

	return rowToFunction(&functions[0]), nil
}

// GetByID retrieves a function by its ID.
func (s *FunctionStore) GetByID(ctx context.Context, id string) (*Function, error) {
	query := `
		SELECT id, name, namespace, version, wasm_cid, source_cid,
			memory_limit_mb, timeout_seconds, is_public,
			retry_count, retry_delay_seconds, dlq_topic,
			status, created_at, updated_at, created_by
		FROM functions
		WHERE id = ?
	`

	var functions []functionRow
	if err := s.db.Query(ctx, &functions, query, id); err != nil {
		return nil, fmt.Errorf("failed to query function: %w", err)
	}

	if len(functions) == 0 {
		return nil, ErrFunctionNotFound
	}

	return rowToFunction(&functions[0]), nil
}

// GetByNameInternal retrieves a function by name regardless of status.
func (s *FunctionStore) GetByNameInternal(ctx context.Context, namespace, name string) (*Function, error) {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)

	query := `
		SELECT id, name, namespace, version, wasm_cid, source_cid,
			memory_limit_mb, timeout_seconds, is_public,
			retry_count, retry_delay_seconds, dlq_topic,
			status, created_at, updated_at, created_by
		FROM functions
		WHERE namespace = ? AND name = ?
		ORDER BY version DESC
		LIMIT 1
	`

	var functions []functionRow
	if err := s.db.Query(ctx, &functions, query, namespace, name); err != nil {
		return nil, fmt.Errorf("failed to query function: %w", err)
	}

	if len(functions) == 0 {
		return nil, ErrFunctionNotFound
	}

	return rowToFunction(&functions[0]), nil
}

// List returns all active functions for a namespace.
func (s *FunctionStore) List(ctx context.Context, namespace string) ([]*Function, error) {
	query := `
		SELECT f.id, f.name, f.namespace, f.version, f.wasm_cid, f.source_cid,
			f.memory_limit_mb, f.timeout_seconds, f.is_public,
			f.retry_count, f.retry_delay_seconds, f.dlq_topic,
			f.status, f.created_at, f.updated_at, f.created_by
		FROM functions f
		INNER JOIN (
			SELECT namespace, name, MAX(version) as max_version
			FROM functions
			WHERE namespace = ? AND status = ?
			GROUP BY namespace, name
		) latest ON f.namespace = latest.namespace
			AND f.name = latest.name
			AND f.version = latest.max_version
		ORDER BY f.name
	`

	var rows []functionRow
	if err := s.db.Query(ctx, &rows, query, namespace, string(FunctionStatusActive)); err != nil {
		return nil, fmt.Errorf("failed to list functions: %w", err)
	}

	functions := make([]*Function, len(rows))
	for i, row := range rows {
		functions[i] = rowToFunction(&row)
	}

	return functions, nil
}

// ListVersions returns all versions of a function.
func (s *FunctionStore) ListVersions(ctx context.Context, namespace, name string) ([]*Function, error) {
	query := `
		SELECT id, name, namespace, version, wasm_cid, source_cid,
			memory_limit_mb, timeout_seconds, is_public,
			retry_count, retry_delay_seconds, dlq_topic,
			status, created_at, updated_at, created_by
		FROM functions
		WHERE namespace = ? AND name = ?
		ORDER BY version DESC
	`

	var rows []functionRow
	if err := s.db.Query(ctx, &rows, query, namespace, name); err != nil {
		return nil, fmt.Errorf("failed to list versions: %w", err)
	}

	functions := make([]*Function, len(rows))
	for i, row := range rows {
		functions[i] = rowToFunction(&row)
	}

	return functions, nil
}

// Delete marks a function as inactive (soft delete).
func (s *FunctionStore) Delete(ctx context.Context, namespace, name string, version int) error {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)

	var query string
	var args []interface{}

	if version == 0 {
		query = `UPDATE functions SET status = ?, updated_at = ? WHERE namespace = ? AND name = ?`
		args = []interface{}{string(FunctionStatusInactive), time.Now(), namespace, name}
	} else {
		query = `UPDATE functions SET status = ?, updated_at = ? WHERE namespace = ? AND name = ? AND version = ?`
		args = []interface{}{string(FunctionStatusInactive), time.Now(), namespace, name, version}
	}

	result, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete function: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		if version == 0 {
			return ErrFunctionNotFound
		}
		return ErrVersionNotFound
	}

	s.logger.Info("Function deleted",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Int("version", version),
	)

	return nil
}

// SaveEnvVars saves environment variables for a function.
func (s *FunctionStore) SaveEnvVars(ctx context.Context, functionID string, envVars map[string]string) error {
	deleteQuery := `DELETE FROM function_env_vars WHERE function_id = ?`
	if _, err := s.db.Exec(ctx, deleteQuery, functionID); err != nil {
		return fmt.Errorf("failed to clear existing env vars: %w", err)
	}

	if len(envVars) == 0 {
		return nil
	}

	for key, value := range envVars {
		id := uuid.New().String()
		query := `INSERT INTO function_env_vars (id, function_id, key, value, created_at) VALUES (?, ?, ?, ?, ?)`
		if _, err := s.db.Exec(ctx, query, id, functionID, key, value, time.Now()); err != nil {
			return fmt.Errorf("failed to save env var '%s': %w", key, err)
		}
	}

	return nil
}

// GetEnvVars retrieves environment variables for a function.
func (s *FunctionStore) GetEnvVars(ctx context.Context, functionID string) (map[string]string, error) {
	query := `SELECT key, value FROM function_env_vars WHERE function_id = ?`

	var rows []envVarRow
	if err := s.db.Query(ctx, &rows, query, functionID); err != nil {
		return nil, fmt.Errorf("failed to query env vars: %w", err)
	}

	envVars := make(map[string]string, len(rows))
	for _, row := range rows {
		envVars[row.Key] = row.Value
	}

	return envVars, nil
}

// rowToFunction converts a database row to a Function struct.
func rowToFunction(row *functionRow) *Function {
	return &Function{
		ID:                row.ID,
		Name:              row.Name,
		Namespace:         row.Namespace,
		Version:           row.Version,
		WASMCID:           row.WASMCID,
		SourceCID:         row.SourceCID.String,
		MemoryLimitMB:     row.MemoryLimitMB,
		TimeoutSeconds:    row.TimeoutSeconds,
		IsPublic:          row.IsPublic,
		RetryCount:        row.RetryCount,
		RetryDelaySeconds: row.RetryDelaySeconds,
		DLQTopic:          row.DLQTopic.String,
		Status:            FunctionStatus(row.Status),
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
		CreatedBy:         row.CreatedBy,
	}
}

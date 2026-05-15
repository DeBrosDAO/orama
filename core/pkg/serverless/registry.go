package serverless

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Ensure Registry implements FunctionRegistry and InvocationLogger interfaces.
var _ FunctionRegistry = (*Registry)(nil)
var _ InvocationLogger = (*Registry)(nil)

// Registry manages function metadata in RQLite and bytecode in IPFS.
// It implements the FunctionRegistry interface.
type Registry struct {
	db         rqlite.Client
	ipfs       ipfs.IPFSClient
	ipfsAPIURL string
	logger     *zap.Logger
	tableName  string
}

// RegistryConfig holds configuration for the Registry.
type RegistryConfig struct {
	IPFSAPIURL string // IPFS API URL for content retrieval
}

// NewRegistry creates a new function registry.
func NewRegistry(db rqlite.Client, ipfsClient ipfs.IPFSClient, cfg RegistryConfig, logger *zap.Logger) *Registry {
	return &Registry{
		db:         db,
		ipfs:       ipfsClient,
		ipfsAPIURL: cfg.IPFSAPIURL,
		logger:     logger,
		tableName:  "functions",
	}
}

// Register deploys a new function or updates an existing one.
func (r *Registry) Register(ctx context.Context, fn *FunctionDefinition, wasmBytes []byte) (*Function, error) {
	if fn == nil {
		return nil, &ValidationError{Field: "definition", Message: "cannot be nil"}
	}
	fn.Name = strings.TrimSpace(fn.Name)
	fn.Namespace = strings.TrimSpace(fn.Namespace)

	if fn.Name == "" {
		return nil, &ValidationError{Field: "name", Message: "cannot be empty"}
	}
	if fn.Namespace == "" {
		return nil, &ValidationError{Field: "namespace", Message: "cannot be empty"}
	}
	if len(wasmBytes) == 0 {
		return nil, &ValidationError{Field: "wasmBytes", Message: "cannot be empty"}
	}

	// Check if function already exists (regardless of status) to get old metadata for invalidation
	oldFn, err := r.getByNameInternal(ctx, fn.Namespace, fn.Name)
	if err != nil && err != ErrFunctionNotFound {
		return nil, &DeployError{FunctionName: fn.Name, Cause: err}
	}

	// Upload WASM to IPFS
	wasmCID, err := r.uploadWASM(ctx, wasmBytes, fn.Name)
	if err != nil {
		return nil, &DeployError{FunctionName: fn.Name, Cause: err}
	}

	// Apply defaults
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

	if oldFn != nil {
		// Use existing ID and increment version
		id = oldFn.ID
		version = oldFn.Version + 1
	}

	// Use INSERT OR REPLACE to ensure we never hit UNIQUE constraint failures on (namespace, name).
	// This handles both new registrations and overwriting existing (even inactive) functions.
	query := `
		INSERT OR REPLACE INTO functions (
			id, name, namespace, version, wasm_cid,
			memory_limit_mb, timeout_seconds, is_public,
			retry_count, retry_delay_seconds, dlq_topic,
			status, created_at, updated_at, created_by,
			ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = r.db.Exec(ctx, query,
		id, fn.Name, fn.Namespace, version, wasmCID,
		memoryLimit, timeout, fn.IsPublic,
		fn.RetryCount, retryDelay, fn.DLQTopic,
		string(FunctionStatusActive), now, now, fn.Namespace,
		fn.WSPersistent, fn.WSIdleTimeoutSec, fn.WSMaxFrameBytes, fn.WSMaxInflightPerConn,
	)
	if err != nil {
		return nil, &DeployError{FunctionName: fn.Name, Cause: fmt.Errorf("failed to register function: %w", err)}
	}

	// Save environment variables
	if err := r.saveEnvVars(ctx, id, fn.EnvVars); err != nil {
		return nil, &DeployError{FunctionName: fn.Name, Cause: err}
	}

	r.logger.Info("Function registered",
		zap.String("id", id),
		zap.String("name", fn.Name),
		zap.String("namespace", fn.Namespace),
		zap.String("wasm_cid", wasmCID),
		zap.Int("version", version),
		zap.Bool("updated", oldFn != nil),
	)

	return oldFn, nil
}

// Get retrieves a function by name and optional version.
// If version is 0, returns the latest version.
func (r *Registry) Get(ctx context.Context, namespace, name string, version int) (*Function, error) {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)

	var query string
	var args []interface{}

	if version == 0 {
		// Get latest version
		query = `
			SELECT id, name, namespace, version, wasm_cid, source_cid,
				memory_limit_mb, timeout_seconds, is_public,
				retry_count, retry_delay_seconds, dlq_topic,
				status, created_at, updated_at, created_by,
				ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn
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
				status, created_at, updated_at, created_by,
				ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn
			FROM functions
			WHERE namespace = ? AND name = ? AND version = ?
		`
		args = []interface{}{namespace, name, version}
	}

	var functions []functionRow
	if err := r.db.Query(ctx, &functions, query, args...); err != nil {
		return nil, fmt.Errorf("failed to query function: %w", err)
	}

	if len(functions) == 0 {
		if version == 0 {
			return nil, ErrFunctionNotFound
		}
		return nil, ErrVersionNotFound
	}

	return r.rowToFunction(&functions[0]), nil
}

// List returns all functions for a namespace.
func (r *Registry) List(ctx context.Context, namespace string) ([]*Function, error) {
	// Get latest version of each function in the namespace
	query := `
		SELECT f.id, f.name, f.namespace, f.version, f.wasm_cid, f.source_cid,
			f.memory_limit_mb, f.timeout_seconds, f.is_public,
			f.retry_count, f.retry_delay_seconds, f.dlq_topic,
			f.status, f.created_at, f.updated_at, f.created_by,
			f.ws_persistent, f.ws_idle_timeout_sec, f.ws_max_frame_bytes, f.ws_max_inflight_per_conn
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
	if err := r.db.Query(ctx, &rows, query, namespace, string(FunctionStatusActive)); err != nil {
		return nil, fmt.Errorf("failed to list functions: %w", err)
	}

	functions := make([]*Function, len(rows))
	for i, row := range rows {
		functions[i] = r.rowToFunction(&row)
	}

	return functions, nil
}

// Delete removes a function. If version is 0, removes all versions.
func (r *Registry) Delete(ctx context.Context, namespace, name string, version int) error {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)

	var query string
	var args []interface{}

	if version == 0 {
		// Mark all versions as inactive (soft delete)
		query = `UPDATE functions SET status = ?, updated_at = ? WHERE namespace = ? AND name = ?`
		args = []interface{}{string(FunctionStatusInactive), time.Now(), namespace, name}
	} else {
		query = `UPDATE functions SET status = ?, updated_at = ? WHERE namespace = ? AND name = ? AND version = ?`
		args = []interface{}{string(FunctionStatusInactive), time.Now(), namespace, name, version}
	}

	result, err := r.db.Exec(ctx, query, args...)
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

	r.logger.Info("Function deleted",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Int("version", version),
	)

	return nil
}

// GetWASMBytes retrieves the compiled WASM bytecode for a function.
func (r *Registry) GetWASMBytes(ctx context.Context, wasmCID string) ([]byte, error) {
	if wasmCID == "" {
		return nil, &ValidationError{Field: "wasmCID", Message: "cannot be empty"}
	}

	reader, err := r.ipfs.Get(ctx, wasmCID, r.ipfsAPIURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get WASM from IPFS: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read WASM data: %w", err)
	}

	return data, nil
}

// GetEnvVars retrieves environment variables for a function.
func (r *Registry) GetEnvVars(ctx context.Context, functionID string) (map[string]string, error) {
	query := `SELECT key, value FROM function_env_vars WHERE function_id = ?`

	var rows []envVarRow
	if err := r.db.Query(ctx, &rows, query, functionID); err != nil {
		return nil, fmt.Errorf("failed to query env vars: %w", err)
	}

	envVars := make(map[string]string, len(rows))
	for _, row := range rows {
		envVars[row.Key] = row.Value
	}

	return envVars, nil
}

// GetByID retrieves a function by its ID.
func (r *Registry) GetByID(ctx context.Context, id string) (*Function, error) {
	query := `
		SELECT id, name, namespace, version, wasm_cid, source_cid,
			memory_limit_mb, timeout_seconds, is_public,
			retry_count, retry_delay_seconds, dlq_topic,
			status, created_at, updated_at, created_by,
			ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn
		FROM functions
		WHERE id = ?
	`

	var functions []functionRow
	if err := r.db.Query(ctx, &functions, query, id); err != nil {
		return nil, fmt.Errorf("failed to query function: %w", err)
	}

	if len(functions) == 0 {
		return nil, ErrFunctionNotFound
	}

	return r.rowToFunction(&functions[0]), nil
}

// ListVersions returns all versions of a function.
func (r *Registry) ListVersions(ctx context.Context, namespace, name string) ([]*Function, error) {
	query := `
		SELECT id, name, namespace, version, wasm_cid, source_cid,
			memory_limit_mb, timeout_seconds, is_public,
			retry_count, retry_delay_seconds, dlq_topic,
			status, created_at, updated_at, created_by,
			ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn
		FROM functions
		WHERE namespace = ? AND name = ?
		ORDER BY version DESC
	`

	var rows []functionRow
	if err := r.db.Query(ctx, &rows, query, namespace, name); err != nil {
		return nil, fmt.Errorf("failed to list versions: %w", err)
	}

	functions := make([]*Function, len(rows))
	for i, row := range rows {
		functions[i] = r.rowToFunction(&row)
	}

	return functions, nil
}

// Log records a function invocation and its logs to the database.
func (r *Registry) Log(ctx context.Context, inv *InvocationRecord) error {
	if inv == nil {
		return nil
	}

	// Insert invocation record
	invQuery := `
		INSERT INTO function_invocations (
			id, function_id, request_id, trigger_type, caller_wallet,
			input_size, output_size, started_at, completed_at,
			duration_ms, status, error_message, memory_used_mb
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := r.db.Exec(ctx, invQuery,
		inv.ID, inv.FunctionID, inv.RequestID, string(inv.TriggerType), inv.CallerWallet,
		inv.InputSize, inv.OutputSize, inv.StartedAt, inv.CompletedAt,
		inv.DurationMS, string(inv.Status), inv.ErrorMessage, inv.MemoryUsedMB,
	)
	if err != nil {
		return fmt.Errorf("failed to insert invocation record: %w", err)
	}

	// Insert logs if any
	if len(inv.Logs) > 0 {
		for _, entry := range inv.Logs {
			logID := uuid.New().String()
			logQuery := `
				INSERT INTO function_logs (
					id, function_id, invocation_id, level, message, timestamp
				) VALUES (?, ?, ?, ?, ?, ?)
			`
			_, err := r.db.Exec(ctx, logQuery,
				logID, inv.FunctionID, inv.ID, entry.Level, entry.Message, entry.Timestamp,
			)
			if err != nil {
				r.logger.Warn("Failed to insert function log", zap.Error(err))
				// Continue with other logs
			}
		}
	}

	return nil
}

// GetLogs retrieves logs for a function.
func (r *Registry) GetLogs(ctx context.Context, namespace, name string, limit int) ([]LogEntry, error) {
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

	if err := r.db.Query(ctx, &results, query, namespace, name, limit); err != nil {
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
// record. This is the right answer to "what happened when this function
// was invoked" — `orama function logs <name>` consumes this view.
//
// Always populated when the function has been invoked at least once.
// Two queries (invocations + nested WASM logs) batched into one map.
func (r *Registry) GetInvocations(ctx context.Context, namespace, name string, limit int) ([]Invocation, error) {
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
	if err := r.db.Query(ctx, &rows, invQuery, namespace, name, limit); err != nil {
		return nil, fmt.Errorf("failed to query invocations: %w", err)
	}
	if len(rows) == 0 {
		return []Invocation{}, nil
	}

	// Batched fetch of nested WASM logs.
	invIDs := make([]interface{}, len(rows))
	for i, r := range rows {
		invIDs[i] = r.ID
	}
	placeholders := strings.Repeat("?,", len(invIDs))
	placeholders = placeholders[:len(placeholders)-1]
	logsQuery := `
		SELECT invocation_id, level, message, timestamp
		FROM function_logs
		WHERE invocation_id IN (` + placeholders + `)
		ORDER BY timestamp ASC
	`
	var logRows []struct {
		InvocationID string    `db:"invocation_id"`
		Level        string    `db:"level"`
		Message      string    `db:"message"`
		Timestamp    time.Time `db:"timestamp"`
	}
	logsByInv := map[string][]LogEntry{}
	if err := r.db.Query(ctx, &logRows, logsQuery, invIDs...); err != nil {
		// Don't fail the whole call — invocation summary is still useful.
		r.logger.Warn("failed to fetch nested WASM logs; returning invocations without them",
			zap.Error(err))
	} else {
		for _, lr := range logRows {
			logsByInv[lr.InvocationID] = append(logsByInv[lr.InvocationID], LogEntry{
				Level:     lr.Level,
				Message:   lr.Message,
				Timestamp: lr.Timestamp,
			})
		}
	}

	out := make([]Invocation, len(rows))
	for i, row := range rows {
		out[i] = Invocation{
			ID:           row.ID,
			RequestID:    row.RequestID,
			TriggerType:  row.TriggerType,
			CallerWallet: row.CallerWallet,
			InputSize:    row.InputSize,
			OutputSize:   row.OutputSize,
			StartedAt:    row.StartedAt,
			CompletedAt:  row.CompletedAt,
			DurationMS:   row.DurationMS,
			Status:       row.Status,
			ErrorMessage: row.ErrorMessage,
			MemoryUsedMB: row.MemoryUsedMB,
			WASMLogs:     logsByInv[row.ID],
		}
	}
	return out, nil
}

// -----------------------------------------------------------------------------
// Private helpers
// -----------------------------------------------------------------------------

// defaultWASMReplicationFactor is the IPFS Cluster replication factor for WASM binaries.
const defaultWASMReplicationFactor = 3

// uploadWASM uploads WASM bytecode to IPFS and pins it for cluster-wide replication.
func (r *Registry) uploadWASM(ctx context.Context, wasmBytes []byte, name string) (string, error) {
	reader := bytes.NewReader(wasmBytes)
	resp, err := r.ipfs.Add(ctx, reader, name+".wasm")
	if err != nil {
		return "", fmt.Errorf("failed to upload WASM to IPFS: %w", err)
	}

	// Pin the CID across cluster peers so the binary survives node failures.
	if _, err := r.ipfs.Pin(ctx, resp.Cid, name+".wasm", defaultWASMReplicationFactor); err != nil {
		r.logger.Warn("Failed to pin WASM binary — content may not be replicated",
			zap.String("cid", resp.Cid),
			zap.String("function", name),
			zap.Error(err),
		)
	}

	return resp.Cid, nil
}

// getByNameInternal retrieves a function by name regardless of status.
func (r *Registry) getByNameInternal(ctx context.Context, namespace, name string) (*Function, error) {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)

	query := `
		SELECT id, name, namespace, version, wasm_cid, source_cid,
			memory_limit_mb, timeout_seconds, is_public,
			retry_count, retry_delay_seconds, dlq_topic,
			status, created_at, updated_at, created_by,
			ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn
		FROM functions
		WHERE namespace = ? AND name = ?
		ORDER BY version DESC
		LIMIT 1
	`

	var functions []functionRow
	if err := r.db.Query(ctx, &functions, query, namespace, name); err != nil {
		return nil, fmt.Errorf("failed to query function: %w", err)
	}

	if len(functions) == 0 {
		return nil, ErrFunctionNotFound
	}

	return r.rowToFunction(&functions[0]), nil
}

// saveEnvVars saves environment variables for a function.
func (r *Registry) saveEnvVars(ctx context.Context, functionID string, envVars map[string]string) error {
	// Clear existing env vars first
	deleteQuery := `DELETE FROM function_env_vars WHERE function_id = ?`
	if _, err := r.db.Exec(ctx, deleteQuery, functionID); err != nil {
		return fmt.Errorf("failed to clear existing env vars: %w", err)
	}

	if len(envVars) == 0 {
		return nil
	}

	for key, value := range envVars {
		id := uuid.New().String()
		query := `INSERT INTO function_env_vars (id, function_id, key, value, created_at) VALUES (?, ?, ?, ?, ?)`
		if _, err := r.db.Exec(ctx, query, id, functionID, key, value, time.Now()); err != nil {
			return fmt.Errorf("failed to save env var '%s': %w", key, err)
		}
	}

	return nil
}

// rowToFunction converts a database row to a Function struct.
func (r *Registry) rowToFunction(row *functionRow) *Function {
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

		// WS persistent-instance fields (#240/#249 follow-up). Without
		// these the WS handler's `if fn.WSPersistent` branch never
		// fires and persistent functions silently run as per-frame
		// stateless. See functionRow doc above for full history.
		WSPersistent:         row.WSPersistent,
		WSIdleTimeoutSec:     row.WSIdleTimeoutSec,
		WSMaxFrameBytes:      row.WSMaxFrameBytes,
		WSMaxInflightPerConn: row.WSMaxInflightPerConn,
	}
}

// -----------------------------------------------------------------------------
// Database row types (internal)
// -----------------------------------------------------------------------------

type functionRow struct {
	ID                string         `db:"id"`
	Name              string         `db:"name"`
	Namespace         string         `db:"namespace"`
	Version           int            `db:"version"`
	WASMCID           string         `db:"wasm_cid"`
	SourceCID         sql.NullString `db:"source_cid"`
	MemoryLimitMB     int            `db:"memory_limit_mb"`
	TimeoutSeconds    int            `db:"timeout_seconds"`
	IsPublic          bool           `db:"is_public"`
	RetryCount        int            `db:"retry_count"`
	RetryDelaySeconds int            `db:"retry_delay_seconds"`
	DLQTopic          sql.NullString `db:"dlq_topic"`
	Status            string         `db:"status"`
	CreatedAt         time.Time      `db:"created_at"`
	UpdatedAt         time.Time      `db:"updated_at"`
	CreatedBy         string         `db:"created_by"`

	// WS persistent-instance metadata (#240/#249 follow-up).
	//
	// Pre-fix history: these columns existed in the schema (migration
	// 011) and Register() at line 110+ wrote them, but every read path
	// (Get, List, GetByID, GetByNameInternal) omitted them from the
	// SELECT and functionRow had no fields for them. Result:
	// `fn.WSPersistent` was always the zero value (false) regardless
	// of what the DB said. Every WS function silently ran in
	// per-frame stateless mode — not the persistent mode the
	// `ws_persistent: true` config asks for.
	//
	// AnChat's rpc-router was the canary: it relies on per-connection
	// instance state (request_id ↔ reply correlation, persistent
	// subscription bookkeeping) that the stateless model destroys
	// every frame. Symptom: gateway-side function invocations succeed
	// (telemetry envelope `{request_id, status, duration_ms}` reaches
	// the client) but the function's own `ws_send` frames don't carry
	// the per-connection state the function expects. End-user impact
	// was every RPC timing out at 15 s.
	WSPersistent         bool `db:"ws_persistent"`
	WSIdleTimeoutSec     int  `db:"ws_idle_timeout_sec"`
	WSMaxFrameBytes      int  `db:"ws_max_frame_bytes"`
	WSMaxInflightPerConn int  `db:"ws_max_inflight_per_conn"`
}

type envVarRow struct {
	Key   string `db:"key"`
	Value string `db:"value"`
}

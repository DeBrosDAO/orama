package serverless

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Ensure Registry implements FunctionRegistry interface.
var _ FunctionRegistry = (*Registry)(nil)

// Registry manages function metadata in RQLite and bytecode in IPFS.
// It implements the FunctionRegistry interface.
type Registry struct {
	db          rqlite.Client
	ipfs        ipfs.IPFSClient
	ipfsAPIURL  string
	logger      *zap.Logger
	tableName   string
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

// Register deploys a new function or creates a new version.
func (r *Registry) Register(ctx context.Context, fn *FunctionDefinition, wasmBytes []byte) error {
	if fn == nil {
		return &ValidationError{Field: "definition", Message: "cannot be nil"}
	}
	if fn.Name == "" {
		return &ValidationError{Field: "name", Message: "cannot be empty"}
	}
	if fn.Namespace == "" {
		return &ValidationError{Field: "namespace", Message: "cannot be empty"}
	}
	if len(wasmBytes) == 0 {
		return &ValidationError{Field: "wasmBytes", Message: "cannot be empty"}
	}

	// Upload WASM to IPFS
	wasmCID, err := r.uploadWASM(ctx, wasmBytes, fn.Name)
	if err != nil {
		return &DeployError{FunctionName: fn.Name, Cause: err}
	}

	// Determine version (auto-increment if not specified)
	version := fn.Version
	if version == 0 {
		latestVersion, err := r.getLatestVersion(ctx, fn.Namespace, fn.Name)
		if err != nil && err != ErrFunctionNotFound {
			return &DeployError{FunctionName: fn.Name, Cause: err}
		}
		version = latestVersion + 1
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

	// Generate ID
	id := uuid.New().String()

	// Insert function record
	query := `
		INSERT INTO functions (
			id, name, namespace, version, wasm_cid, 
			memory_limit_mb, timeout_seconds, is_public,
			retry_count, retry_delay_seconds, dlq_topic,
			status, created_at, updated_at, created_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	now := time.Now()
	_, err = r.db.Exec(ctx, query,
		id, fn.Name, fn.Namespace, version, wasmCID,
		memoryLimit, timeout, fn.IsPublic,
		fn.RetryCount, retryDelay, fn.DLQTopic,
		string(FunctionStatusActive), now, now, fn.Namespace, // created_by = namespace for now
	)
	if err != nil {
		return &DeployError{FunctionName: fn.Name, Cause: fmt.Errorf("failed to insert function: %w", err)}
	}

	// Insert environment variables
	if err := r.saveEnvVars(ctx, id, fn.EnvVars); err != nil {
		return &DeployError{FunctionName: fn.Name, Cause: err}
	}

	r.logger.Info("Function registered",
		zap.String("id", id),
		zap.String("name", fn.Name),
		zap.String("namespace", fn.Namespace),
		zap.Int("version", version),
		zap.String("wasm_cid", wasmCID),
	)

	return nil
}

// Get retrieves a function by name and optional version.
// If version is 0, returns the latest version.
func (r *Registry) Get(ctx context.Context, namespace, name string, version int) (*Function, error) {
	var query string
	var args []interface{}

	if version == 0 {
		// Get latest version
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
			status, created_at, updated_at, created_by
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
			status, created_at, updated_at, created_by
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

// -----------------------------------------------------------------------------
// Private helpers
// -----------------------------------------------------------------------------

// uploadWASM uploads WASM bytecode to IPFS and returns the CID.
func (r *Registry) uploadWASM(ctx context.Context, wasmBytes []byte, name string) (string, error) {
	reader := bytes.NewReader(wasmBytes)
	resp, err := r.ipfs.Add(ctx, reader, name+".wasm")
	if err != nil {
		return "", fmt.Errorf("failed to upload WASM to IPFS: %w", err)
	}
	return resp.Cid, nil
}

// getLatestVersion returns the latest version number for a function.
func (r *Registry) getLatestVersion(ctx context.Context, namespace, name string) (int, error) {
	query := `SELECT MAX(version) FROM functions WHERE namespace = ? AND name = ?`

	var maxVersion sql.NullInt64
	var results []struct {
		MaxVersion sql.NullInt64 `db:"max(version)"`
	}

	if err := r.db.Query(ctx, &results, query, namespace, name); err != nil {
		return 0, err
	}

	if len(results) == 0 || !results[0].MaxVersion.Valid {
		return 0, ErrFunctionNotFound
	}

	maxVersion = results[0].MaxVersion
	return int(maxVersion.Int64), nil
}

// saveEnvVars saves environment variables for a function.
func (r *Registry) saveEnvVars(ctx context.Context, functionID string, envVars map[string]string) error {
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
}

type envVarRow struct {
	Key   string `db:"key"`
	Value string `db:"value"`
}


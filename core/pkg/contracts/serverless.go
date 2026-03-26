package contracts

import (
	"context"
	"time"
)

// FunctionExecutor handles the execution of WebAssembly serverless functions.
// Manages compilation, caching, and runtime execution of WASM modules.
type FunctionExecutor interface {
	// Execute runs a function with the given input and returns the output.
	// fn contains the function metadata, input is the function's input data,
	// and invCtx provides context about the invocation (caller, trigger type, etc.).
	Execute(ctx context.Context, fn *Function, input []byte, invCtx *InvocationContext) ([]byte, error)

	// Precompile compiles a WASM module and caches it for faster execution.
	// wasmCID is the content identifier, wasmBytes is the raw WASM bytecode.
	// Precompiling reduces cold-start latency for subsequent invocations.
	Precompile(ctx context.Context, wasmCID string, wasmBytes []byte) error

	// Invalidate removes a compiled module from the cache.
	// Call this when a function is updated or deleted.
	Invalidate(wasmCID string)
}

// FunctionRegistry manages function metadata and bytecode storage.
// Responsible for CRUD operations on function definitions.
type FunctionRegistry interface {
	// Register deploys a new function or updates an existing one.
	// fn contains the function definition, wasmBytes is the compiled WASM code.
	// Returns the old function definition if it was updated, or nil for new registrations.
	Register(ctx context.Context, fn *FunctionDefinition, wasmBytes []byte) (*Function, error)

	// Get retrieves a function by name and optional version.
	// If version is 0, returns the latest active version.
	// Returns an error if the function is not found.
	Get(ctx context.Context, namespace, name string, version int) (*Function, error)

	// List returns all active functions in a namespace.
	// Returns only the latest version of each function.
	List(ctx context.Context, namespace string) ([]*Function, error)

	// Delete marks a function as inactive (soft delete).
	// If version is 0, marks all versions as inactive.
	Delete(ctx context.Context, namespace, name string, version int) error

	// GetWASMBytes retrieves the compiled WASM bytecode for a function.
	// wasmCID is the content identifier returned during registration.
	GetWASMBytes(ctx context.Context, wasmCID string) ([]byte, error)

	// GetLogs retrieves execution logs for a function.
	// limit constrains the number of log entries returned.
	GetLogs(ctx context.Context, namespace, name string, limit int) ([]LogEntry, error)
}

// Function represents a deployed serverless function with its metadata.
type Function struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Namespace         string         `json:"namespace"`
	Version           int            `json:"version"`
	WASMCID           string         `json:"wasm_cid"`
	SourceCID         string         `json:"source_cid,omitempty"`
	MemoryLimitMB     int            `json:"memory_limit_mb"`
	TimeoutSeconds    int            `json:"timeout_seconds"`
	IsPublic          bool           `json:"is_public"`
	RetryCount        int            `json:"retry_count"`
	RetryDelaySeconds int            `json:"retry_delay_seconds"`
	DLQTopic          string         `json:"dlq_topic,omitempty"`
	Status            FunctionStatus `json:"status"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	CreatedBy         string         `json:"created_by"`
}

// FunctionDefinition contains the configuration for deploying a function.
type FunctionDefinition struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	Version           int               `json:"version,omitempty"`
	MemoryLimitMB     int               `json:"memory_limit_mb,omitempty"`
	TimeoutSeconds    int               `json:"timeout_seconds,omitempty"`
	IsPublic          bool              `json:"is_public,omitempty"`
	RetryCount        int               `json:"retry_count,omitempty"`
	RetryDelaySeconds int               `json:"retry_delay_seconds,omitempty"`
	DLQTopic          string            `json:"dlq_topic,omitempty"`
	EnvVars           map[string]string `json:"env_vars,omitempty"`
}

// InvocationContext provides context for a function invocation.
type InvocationContext struct {
	RequestID    string            `json:"request_id"`
	FunctionID   string            `json:"function_id"`
	FunctionName string            `json:"function_name"`
	Namespace    string            `json:"namespace"`
	CallerWallet string            `json:"caller_wallet,omitempty"`
	TriggerType  TriggerType       `json:"trigger_type"`
	WSClientID   string            `json:"ws_client_id,omitempty"`
	EnvVars      map[string]string `json:"env_vars,omitempty"`
}

// LogEntry represents a log message from a function execution.
type LogEntry struct {
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// FunctionStatus represents the current state of a deployed function.
type FunctionStatus string

const (
	FunctionStatusActive   FunctionStatus = "active"
	FunctionStatusInactive FunctionStatus = "inactive"
	FunctionStatusError    FunctionStatus = "error"
)

// TriggerType identifies the type of event that triggered a function invocation.
type TriggerType string

const (
	TriggerTypeHTTP      TriggerType = "http"
	TriggerTypeWebSocket TriggerType = "websocket"
	TriggerTypeCron      TriggerType = "cron"
	TriggerTypeDatabase  TriggerType = "database"
	TriggerTypePubSub    TriggerType = "pubsub"
	TriggerTypeTimer     TriggerType = "timer"
	TriggerTypeJob       TriggerType = "job"
)

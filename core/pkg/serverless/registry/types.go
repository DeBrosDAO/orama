package registry

import (
	"context"
	"database/sql"
	"time"
)

// RegistryConfig holds configuration for the Registry.
type RegistryConfig struct {
	IPFSAPIURL string
}

// FunctionStatus represents the current state of a deployed function.
type FunctionStatus string

const (
	FunctionStatusActive   FunctionStatus = "active"
	FunctionStatusInactive FunctionStatus = "inactive"
	FunctionStatusError    FunctionStatus = "error"
)

// FunctionDefinition contains the configuration for deploying a function.
type FunctionDefinition struct {
	Name              string
	Namespace         string
	Version           int
	MemoryLimitMB     int
	TimeoutSeconds    int
	IsPublic          bool
	RetryCount        int
	RetryDelaySeconds int
	DLQTopic          string
	EnvVars           map[string]string

	// Persistent WebSocket settings — see plan 06_PERSISTENT_WS_FUNCTIONS.md
	WSPersistent         bool
	WSIdleTimeoutSec     int
	WSMaxFrameBytes      int
	WSMaxInflightPerConn int
}

// Function represents a deployed serverless function.
type Function struct {
	ID                string
	Name              string
	Namespace         string
	Version           int
	WASMCID           string
	SourceCID         string
	MemoryLimitMB     int
	TimeoutSeconds    int
	IsPublic          bool
	RetryCount        int
	RetryDelaySeconds int
	DLQTopic          string
	Status            FunctionStatus
	CreatedAt         time.Time
	UpdatedAt         time.Time
	CreatedBy         string

	// Persistent WebSocket settings.
	WSPersistent         bool
	WSIdleTimeoutSec     int
	WSMaxFrameBytes      int
	WSMaxInflightPerConn int
}

// LogEntry represents a log message emitted from inside a WASM function
// via the log_info / log_error host calls.
type LogEntry struct {
	Level     string
	Message   string
	Timestamp time.Time
}

// Invocation is a record of one function invocation, returned by
// GetInvocations. It always populates regardless of whether the function
// emitted any log_info/log_error calls — the WASM-emitted entries are
// nested under WASMLogs (which may be empty).
//
// This is the right answer to "what happened on this invocation" — the
// CLI's `function logs` and dashboard log views consume this. The
// older GetLogs(LogEntry) returns ONLY WASM-emitted entries, which is
// usually empty and confused users (bug #211).
type Invocation struct {
	ID           string     `json:"id"`
	RequestID    string     `json:"request_id"`
	TriggerType  string     `json:"trigger_type"`
	CallerWallet string     `json:"caller_wallet,omitempty"`
	InputSize    int        `json:"input_size"`
	OutputSize   int        `json:"output_size"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  time.Time  `json:"completed_at"`
	DurationMS   int64      `json:"duration_ms"`
	Status       string     `json:"status"`
	ErrorMessage string     `json:"error_message,omitempty"`
	MemoryUsedMB float64    `json:"memory_used_mb,omitempty"`
	WASMLogs     []LogEntry `json:"wasm_logs,omitempty"`
}

// FunctionRegistry interface
type FunctionRegistry interface {
	Register(ctx context.Context, fn *FunctionDefinition, wasmBytes []byte) (*Function, error)
	Get(ctx context.Context, namespace, name string, version int) (*Function, error)
	List(ctx context.Context, namespace string) ([]*Function, error)
	Delete(ctx context.Context, namespace, name string, version int) error

	// SetEnabled flips a function's status between active and inactive
	// across all versions without redeploying. Plan 11.5 — pause a
	// misbehaving function during incident response.
	SetEnabled(ctx context.Context, namespace, name string, enabled bool) error

	GetWASMBytes(ctx context.Context, wasmCID string) ([]byte, error)

	// GetLogs returns ONLY WASM-emitted log entries (rows in function_logs).
	// This is rarely useful on its own — most functions don't emit any.
	// Prefer GetInvocations for the complete invocation-history view.
	GetLogs(ctx context.Context, namespace, name string, limit int) ([]LogEntry, error)

	// GetInvocations returns invocation history (always populated when the
	// function has been invoked at least once) with any associated WASM
	// log entries nested per record. Sorted by started_at DESC.
	GetInvocations(ctx context.Context, namespace, name string, limit int) ([]Invocation, error)
}

// Error types
var ErrFunctionNotFound = &NotFoundError{Resource: "function"}
var ErrVersionNotFound = &NotFoundError{Resource: "version"}

type NotFoundError struct {
	Resource string
}

func (e *NotFoundError) Error() string {
	return e.Resource + " not found"
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return "validation error: " + e.Field + " " + e.Message
}

type DeployError struct {
	FunctionName string
	Cause        error
}

func (e *DeployError) Error() string {
	return "failed to deploy function " + e.FunctionName + ": " + e.Cause.Error()
}

func (e *DeployError) Unwrap() error {
	return e.Cause
}

// Database row types (internal)
type functionRow struct {
	ID                   string
	Name                 string
	Namespace            string
	Version              int
	WASMCID              string
	SourceCID            sql.NullString
	MemoryLimitMB        int
	TimeoutSeconds       int
	IsPublic             bool
	RetryCount           int
	RetryDelaySeconds    int
	DLQTopic             sql.NullString
	Status               string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	CreatedBy            string
	WSPersistent         bool
	WSIdleTimeoutSec     int
	WSMaxFrameBytes      int
	WSMaxInflightPerConn int
}

type envVarRow struct {
	Key   string
	Value string
}

type InvocationRecordData struct {
	ID           string
	FunctionID   string
	RequestID    string
	TriggerType  string
	CallerWallet string
	InputSize    int
	OutputSize   int
	StartedAt    time.Time
	CompletedAt  time.Time
	DurationMS   int64
	Status       string
	ErrorMessage string
	MemoryUsedMB float64
	Logs         []LogData
}

type LogData struct {
	Level     string
	Message   string
	Timestamp time.Time
}

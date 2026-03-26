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
}

// LogEntry represents a log message from a function.
type LogEntry struct {
	Level     string
	Message   string
	Timestamp time.Time
}

// FunctionRegistry interface
type FunctionRegistry interface {
	Register(ctx context.Context, fn *FunctionDefinition, wasmBytes []byte) (*Function, error)
	Get(ctx context.Context, namespace, name string, version int) (*Function, error)
	List(ctx context.Context, namespace string) ([]*Function, error)
	Delete(ctx context.Context, namespace, name string, version int) error
	GetWASMBytes(ctx context.Context, wasmCID string) ([]byte, error)
	GetLogs(ctx context.Context, namespace, name string, limit int) ([]LogEntry, error)
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
	ID                string
	Name              string
	Namespace         string
	Version           int
	WASMCID           string
	SourceCID         sql.NullString
	MemoryLimitMB     int
	TimeoutSeconds    int
	IsPublic          bool
	RetryCount        int
	RetryDelaySeconds int
	DLQTopic          sql.NullString
	Status            string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	CreatedBy         string
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

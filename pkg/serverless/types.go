// Package serverless provides a WASM-based serverless function engine for the Orama Network.
// It enables users to deploy and execute Go functions (compiled to WASM) across all nodes,
// with support for HTTP/WebSocket triggers, cron jobs, database triggers, pub/sub triggers,
// one-time timers, retries with DLQ, and background jobs.
package serverless

import (
	"context"
	"io"
	"time"
)

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

// JobStatus represents the current state of a background job.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
)

// InvocationStatus represents the result of a function invocation.
type InvocationStatus string

const (
	InvocationStatusSuccess InvocationStatus = "success"
	InvocationStatusError   InvocationStatus = "error"
	InvocationStatusTimeout InvocationStatus = "timeout"
)

// DBOperation represents the type of database operation that triggered a function.
type DBOperation string

const (
	DBOperationInsert DBOperation = "INSERT"
	DBOperationUpdate DBOperation = "UPDATE"
	DBOperationDelete DBOperation = "DELETE"
)

// -----------------------------------------------------------------------------
// Core Interfaces (following Interface Segregation Principle)
// -----------------------------------------------------------------------------

// FunctionRegistry manages function metadata and bytecode storage.
// Responsible for CRUD operations on function definitions.
type FunctionRegistry interface {
	// Register deploys a new function or updates an existing one.
	// Returns the old function definition if it was updated, or nil if it was a new registration.
	Register(ctx context.Context, fn *FunctionDefinition, wasmBytes []byte) (*Function, error)

	// Get retrieves a function by name and optional version.
	// If version is 0, returns the latest version.
	Get(ctx context.Context, namespace, name string, version int) (*Function, error)

	// List returns all functions for a namespace.
	List(ctx context.Context, namespace string) ([]*Function, error)

	// Delete removes a function. If version is 0, removes all versions.
	Delete(ctx context.Context, namespace, name string, version int) error

	// GetWASMBytes retrieves the compiled WASM bytecode for a function.
	GetWASMBytes(ctx context.Context, wasmCID string) ([]byte, error)

	// GetLogs retrieves logs for a function.
	GetLogs(ctx context.Context, namespace, name string, limit int) ([]LogEntry, error)
}

// FunctionExecutor handles the actual execution of WASM functions.
type FunctionExecutor interface {
	// Execute runs a function with the given input and returns the output.
	Execute(ctx context.Context, fn *Function, input []byte, invCtx *InvocationContext) ([]byte, error)

	// Precompile compiles a WASM module and caches it for faster execution.
	Precompile(ctx context.Context, wasmCID string, wasmBytes []byte) error

	// Invalidate removes a compiled module from the cache.
	Invalidate(wasmCID string)
}

// SecretsManager handles secure storage and retrieval of secrets.
type SecretsManager interface {
	// Set stores an encrypted secret.
	Set(ctx context.Context, namespace, name, value string) error

	// Get retrieves a decrypted secret.
	Get(ctx context.Context, namespace, name string) (string, error)

	// List returns all secret names for a namespace (not values).
	List(ctx context.Context, namespace string) ([]string, error)

	// Delete removes a secret.
	Delete(ctx context.Context, namespace, name string) error
}

// TriggerManager manages function triggers (cron, database, pubsub, timer).
type TriggerManager interface {
	// AddCronTrigger adds a cron-based trigger to a function.
	AddCronTrigger(ctx context.Context, functionID, cronExpr string) error

	// AddDBTrigger adds a database trigger to a function.
	AddDBTrigger(ctx context.Context, functionID, tableName string, operation DBOperation, condition string) error

	// AddPubSubTrigger adds a pubsub trigger to a function.
	AddPubSubTrigger(ctx context.Context, functionID, topic string) error

	// ScheduleOnce schedules a one-time execution.
	ScheduleOnce(ctx context.Context, functionID string, runAt time.Time, payload []byte) (string, error)

	// RemoveTrigger removes a trigger by ID.
	RemoveTrigger(ctx context.Context, triggerID string) error
}

// JobManager manages background job execution.
type JobManager interface {
	// Enqueue adds a job to the queue for background execution.
	Enqueue(ctx context.Context, functionID string, payload []byte) (string, error)

	// GetStatus retrieves the current status of a job.
	GetStatus(ctx context.Context, jobID string) (*Job, error)

	// List returns jobs for a function.
	List(ctx context.Context, functionID string, limit int) ([]*Job, error)

	// Cancel attempts to cancel a pending or running job.
	Cancel(ctx context.Context, jobID string) error
}

// WebSocketManager manages WebSocket connections for function streaming.
type WebSocketManager interface {
	// Register registers a new WebSocket connection.
	Register(clientID string, conn WebSocketConn)

	// Unregister removes a WebSocket connection.
	Unregister(clientID string)

	// Send sends data to a specific client.
	Send(clientID string, data []byte) error

	// Broadcast sends data to all clients subscribed to a topic.
	Broadcast(topic string, data []byte) error

	// Subscribe adds a client to a topic.
	Subscribe(clientID, topic string)

	// Unsubscribe removes a client from a topic.
	Unsubscribe(clientID, topic string)
}

// WebSocketConn abstracts a WebSocket connection for testability.
type WebSocketConn interface {
	WriteMessage(messageType int, data []byte) error
	ReadMessage() (messageType int, p []byte, err error)
	Close() error
}

// -----------------------------------------------------------------------------
// Data Types
// -----------------------------------------------------------------------------

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
	CronExpressions   []string          `json:"cron_expressions,omitempty"`
	DBTriggers        []DBTriggerConfig `json:"db_triggers,omitempty"`
	PubSubTopics      []string          `json:"pubsub_topics,omitempty"`
}

// DBTriggerConfig defines a database trigger configuration.
type DBTriggerConfig struct {
	Table     string      `json:"table"`
	Operation DBOperation `json:"operation"`
	Condition string      `json:"condition,omitempty"`
}

// Function represents a deployed serverless function.
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

// InvocationResult represents the result of a function invocation.
type InvocationResult struct {
	RequestID  string           `json:"request_id"`
	Output     []byte           `json:"output,omitempty"`
	Status     InvocationStatus `json:"status"`
	Error      string           `json:"error,omitempty"`
	DurationMS int64            `json:"duration_ms"`
	Logs       []LogEntry       `json:"logs,omitempty"`
}

// LogEntry represents a log message from a function.
type LogEntry struct {
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// Job represents a background job.
type Job struct {
	ID          string     `json:"id"`
	FunctionID  string     `json:"function_id"`
	Payload     []byte     `json:"payload,omitempty"`
	Status      JobStatus  `json:"status"`
	Progress    int        `json:"progress"`
	Result      []byte     `json:"result,omitempty"`
	Error       string     `json:"error,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CronTrigger represents a cron-based trigger.
type CronTrigger struct {
	ID             string     `json:"id"`
	FunctionID     string     `json:"function_id"`
	CronExpression string     `json:"cron_expression"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	Enabled        bool       `json:"enabled"`
}

// DBTrigger represents a database trigger.
type DBTrigger struct {
	ID         string      `json:"id"`
	FunctionID string      `json:"function_id"`
	TableName  string      `json:"table_name"`
	Operation  DBOperation `json:"operation"`
	Condition  string      `json:"condition,omitempty"`
	Enabled    bool        `json:"enabled"`
}

// PubSubTrigger represents a pubsub trigger.
type PubSubTrigger struct {
	ID         string `json:"id"`
	FunctionID string `json:"function_id"`
	Topic      string `json:"topic"`
	Enabled    bool   `json:"enabled"`
}

// Timer represents a one-time scheduled execution.
type Timer struct {
	ID         string    `json:"id"`
	FunctionID string    `json:"function_id"`
	RunAt      time.Time `json:"run_at"`
	Payload    []byte    `json:"payload,omitempty"`
	Status     JobStatus `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// DBChangeEvent is passed to functions triggered by database changes.
type DBChangeEvent struct {
	Table     string                 `json:"table"`
	Operation DBOperation            `json:"operation"`
	Row       map[string]interface{} `json:"row"`
	OldRow    map[string]interface{} `json:"old_row,omitempty"`
}

// -----------------------------------------------------------------------------
// Host Function Types (passed to WASM functions)
// -----------------------------------------------------------------------------

// HostServices provides access to Orama services from within WASM functions.
// This interface is implemented by the host and exposed to WASM modules.
type HostServices interface {
	// Database operations
	DBQuery(ctx context.Context, query string, args []interface{}) ([]byte, error)
	DBExecute(ctx context.Context, query string, args []interface{}) (int64, error)

	// Cache operations
	CacheGet(ctx context.Context, key string) ([]byte, error)
	CacheSet(ctx context.Context, key string, value []byte, ttlSeconds int64) error
	CacheDelete(ctx context.Context, key string) error
	CacheIncr(ctx context.Context, key string) (int64, error)
	CacheIncrBy(ctx context.Context, key string, delta int64) (int64, error)

	// Storage operations
	StoragePut(ctx context.Context, data []byte) (string, error)
	StorageGet(ctx context.Context, cid string) ([]byte, error)

	// PubSub operations
	PubSubPublish(ctx context.Context, topic string, data []byte) error

	// WebSocket operations (only valid in WS context)
	WSSend(ctx context.Context, clientID string, data []byte) error
	WSBroadcast(ctx context.Context, topic string, data []byte) error

	// HTTP operations
	HTTPFetch(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, error)

	// Context operations
	GetEnv(ctx context.Context, key string) (string, error)
	GetSecret(ctx context.Context, name string) (string, error)
	GetRequestID(ctx context.Context) string
	GetCallerWallet(ctx context.Context) string

	// Job operations
	EnqueueBackground(ctx context.Context, functionName string, payload []byte) (string, error)
	ScheduleOnce(ctx context.Context, functionName string, runAt time.Time, payload []byte) (string, error)

	// Logging
	LogInfo(ctx context.Context, message string)
	LogError(ctx context.Context, message string)
}

// -----------------------------------------------------------------------------
// Deployment Types
// -----------------------------------------------------------------------------

// DeployRequest represents a request to deploy a function.
type DeployRequest struct {
	Definition *FunctionDefinition `json:"definition"`
	Source     io.Reader           `json:"-"`       // Go source code or WASM bytes
	IsWASM     bool                `json:"is_wasm"` // True if Source contains WASM bytes
}

// DeployResult represents the result of a deployment.
type DeployResult struct {
	Function *Function `json:"function"`
	WASMCID  string    `json:"wasm_cid"`
	Triggers []string  `json:"triggers,omitempty"`
}

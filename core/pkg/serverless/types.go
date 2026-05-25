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

	// GetLogs returns WASM-emitted log entries (function_logs rows). Often
	// empty because most functions don't call log_info / log_error. Use
	// GetInvocations for the always-populated invocation-history view.
	GetLogs(ctx context.Context, namespace, name string, limit int) ([]LogEntry, error)

	// GetInvocations returns invocation history for a function in reverse
	// chronological order, with any associated WASM log entries nested
	// per record. Always populated when the function has been invoked.
	GetInvocations(ctx context.Context, namespace, name string, limit int) ([]Invocation, error)
}

// Invocation is the record of one function invocation as seen by
// `orama function logs`. Mirrors registry.Invocation; defined here at the
// public package boundary so callers don't need to import the inner package.
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

	// Persistent WebSocket settings — see plan 06_PERSISTENT_WS_FUNCTIONS.md
	// When WSPersistent is true, the function exports ws_open/ws_frame/ws_close
	// instead of using the default per-frame stateless model.
	WSPersistent         bool `json:"ws_persistent,omitempty"`
	WSIdleTimeoutSec     int  `json:"ws_idle_timeout_sec,omitempty"`     // 0 = no idle timeout
	WSMaxFrameBytes      int  `json:"ws_max_frame_bytes,omitempty"`      // 0 = use default 256 KB
	WSMaxInflightPerConn int  `json:"ws_max_inflight_per_conn,omitempty"` // 0 = use default 64
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

	// Persistent WebSocket settings — see plan 06_PERSISTENT_WS_FUNCTIONS.md
	WSPersistent         bool `json:"ws_persistent,omitempty"`
	WSIdleTimeoutSec     int  `json:"ws_idle_timeout_sec,omitempty"`
	WSMaxFrameBytes      int  `json:"ws_max_frame_bytes,omitempty"`
	WSMaxInflightPerConn int  `json:"ws_max_inflight_per_conn,omitempty"`
}

// InvocationContext provides context for a function invocation.
type InvocationContext struct {
	RequestID    string            `json:"request_id"`
	FunctionID   string            `json:"function_id"`
	FunctionName string            `json:"function_name"`
	Namespace    string            `json:"namespace"`
	CallerWallet string            `json:"caller_wallet,omitempty"`
	// CallerIP is the source IP of the request, populated by HTTP/WS handlers.
	// Used by the multi-tier rate limiter as a fallback bucket for anonymous
	// (no-wallet) callers.
	CallerIP    string            `json:"caller_ip,omitempty"`
	TriggerType TriggerType       `json:"trigger_type"`
	WSClientID  string            `json:"ws_client_id,omitempty"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
	// CallerClaims holds custom JWT claims set on the caller's token (beyond
	// the standard sub/namespace fields). Read via host fn `get_caller_claim`.
	// Populated by auth handlers from JWTClaims.Custom; empty for non-JWT auth.
	CallerClaims map[string]string `json:"caller_claims,omitempty"`

	// CallerJWTSubject is the `sub` claim of the Bearer JWT, if any.
	// EXPLICITLY captured from the JWT independent of the API-key-vs-JWT
	// wallet-resolution heuristic — so functions that must bind on the
	// JWT-signed identity (signup flows) can do so reliably even when the
	// caller also presents an API key. Empty string when the request was
	// not JWT-authenticated. Bug #215.
	CallerJWTSubject string `json:"caller_jwt_subject,omitempty"`

	// TriggerDepth is the recursion-depth bucket for trigger-driven
	// invocations. 0 means a top-level (HTTP/WS/cron) invocation; each
	// PubSub-trigger-driven invocation increments it. The host-fn
	// wildcard-publish path (`oh.PubSubPublish` → DispatchLocalPublish)
	// reads this and refuses to fire wildcards once depth ≥
	// maxTriggerDepth, preventing local-only recursion loops a function
	// could create by publishing topics that match its own wildcard
	// trigger (bugboard #93 follow-up).
	TriggerDepth int `json:"trigger_depth,omitempty"`
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
//
// Topic may be an exact topic name or a SQLite GLOB pattern (e.g.
// "presence:*"). See pkg/serverless/triggers/pattern.go for matching rules.
//
// AggregationWindowMs > 0 enables event buffering: the dispatcher accumulates
// events for at most that many milliseconds (or until AggregationMaxBatchSize
// events have been collected, whichever comes first), then invokes the
// function once with a batched payload of type BatchedPubSubEvent.
type PubSubTrigger struct {
	ID                      string `json:"id"`
	FunctionID              string `json:"function_id"`
	Topic                   string `json:"topic"`
	Enabled                 bool   `json:"enabled"`
	AggregationWindowMs     int    `json:"aggregation_window_ms,omitempty"`
	AggregationMaxBatchSize int    `json:"aggregation_max_batch_size,omitempty"`
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

	// DBExecuteV2 is the typed equivalent of DBExecute that returns BOTH the
	// rows-affected count AND a JSON envelope. The legacy DBExecute returns
	// only uint32(rows) — collapsing real errors into "0 rows affected"
	// (bug #218). New code should call DBExecuteV2 to detect real failures.
	//
	// Output JSON shape:
	//   {"rows_affected": 1, "last_insert_id": 42, "error": ""}     // success
	//   {"rows_affected": 0, "last_insert_id": 0,  "error": "..."}  // failure
	//
	// Returns a Go error only on host-side validation failures (no DB,
	// bad JSON args). SQL execution errors are encoded in the JSON.
	DBExecuteV2(ctx context.Context, query string, args []interface{}) ([]byte, error)

	// DBQueryV2 is the typed equivalent of DBQuery — returns a JSON envelope
	// distinguishing "empty result" from "query failed".
	//
	// Output JSON shape:
	//   {"rows": [...], "error": ""}     // success (rows may be [])
	//   {"rows": [],     "error": "..."}  // failure
	DBQueryV2(ctx context.Context, query string, args []interface{}) ([]byte, error)

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
	PubSubPublishBatch(ctx context.Context, msgsJSON []byte) error

	// Push notifications. Sends to all of `userID`'s registered devices in
	// the function's namespace. `msgJSON` is the JSON-encoded PushSendArgs
	// shape (see hostfunctions.PushSend). Returns nil if push is not
	// configured (silent no-op) so functions can be portable across
	// namespaces with/without push enabled.
	PushSend(ctx context.Context, userID string, msgJSON []byte) error

	// DBTransaction executes a batch of SQL statements atomically via the
	// native RQLite transaction endpoint. opsJSON is the JSON-encoded
	// {"ops": [{"kind":"exec"|"query","sql":"...","args":[...]}]} shape.
	// Returns the JSON-encoded BatchResult; the boolean inside the result
	// (committed) tells the caller whether the writes landed.
	//
	// Returns an error only on setup/validation failures (no DB, bad JSON,
	// too many ops). A rollback is reported via committed=false in the
	// returned JSON, NOT as a Go error.
	DBTransaction(ctx context.Context, opsJSON []byte) ([]byte, error)

	// DBQueryBatch runs N SELECT statements in ONE round-trip to the leader
	// (via RQLite's /db/query bulk endpoint). All queries see the same
	// committed snapshot. opsJSON shape: {"ops":[{"sql":"...","args":[...]}, ...]}.
	// Returns JSON {"results":[{"rows":[...], "error":""}, ...]} with one
	// entry per input op, in the same order. Per-query errors are surfaced
	// in the per-op `error` field; the call only returns a Go error on
	// transport/validation failures.
	//
	// Use this for read-heavy functions that gather state from many tables
	// before doing work — e.g. anchat's message-create reads auth +
	// participants + devices (7-10 SELECTs) before writing. Empirically on
	// devnet's cross-region cluster: 10 sequential DBQuery = ~3.5s; one
	// DBQueryBatch with 10 statements = ~340ms. See bugboard #270.
	DBQueryBatch(ctx context.Context, opsJSON []byte) ([]byte, error)

	// PushSendV2 dispatches a push notification with PER-DEVICE result
	// reporting. Returns JSON-encoded push.SendDetailedResult:
	//
	//   {
	//     "ok": false,
	//     "devices_attempted": 2,
	//     "devices_succeeded": 1,
	//     "results": [
	//       {"device_id":"ios-A", "provider":"apns", "success":true},
	//       {"device_id":"ios-B", "provider":"apns", "success":false,
	//        "http_status":410, "reason":"Unregistered",
	//        "message":"...", "unregistered":true}
	//     ]
	//   }
	//
	// Unlike the legacy PushSend (which returns success/fail and discards
	// every provider's HTTP status), this lets WASM callers auto-clean
	// stale tokens, retry transient failures, and surface real reasons.
	// Bugboard #348.
	//
	// Returns a Go error only on setup failures (no manager, invalid JSON,
	// no namespace in invocation context). A per-device failure goes into
	// the JSON `results[]` array, NOT as a Go error — callers parse the
	// envelope. Same shape as DBTransaction's "structured per-op result".
	PushSendV2(ctx context.Context, userID string, msgJSON []byte) ([]byte, error)

	// ExecAndPublish runs ops atomically (like DBTransaction) and, ONLY
	// if the batch commits, publishes data to the named topic with any
	// occurrence of the literal string "{{seq}}" replaced by the assigned
	// per-namespace sequence number.
	//
	// Subscribers can use the seq to detect cross-node replication-lag
	// gaps ("I expected seq N+1, got N+3, must have missed two").
	//
	// Returns the JSON-encoded result with extra fields: seq, published,
	// publish_error (in addition to the embedded BatchResult shape).
	// Rollback or publish failure is reported in the JSON, NOT as Go error.
	ExecAndPublish(ctx context.Context, opsJSON []byte, topic string, dataTemplate []byte) ([]byte, error)

	// WSPubSubBridge wires a WebSocket client directly to a PubSub topic
	// in the function's namespace. The gateway then auto-forwards every
	// matching libp2p message to that client's WS without invoking this
	// function per event. Idempotent.
	//
	// The function's namespace must match the client's namespace (set at
	// WS upgrade time) — namespaces are server-trusted; functions cannot
	// bridge clients in another namespace's topic.
	WSPubSubBridge(ctx context.Context, clientID, topic string) error

	// WSPubSubUnbridge removes a previously-established bridge. Idempotent.
	// Auto-cleaned on WS disconnect, so functions don't have to call this
	// in OnClose unless they want to dynamically unsubscribe.
	WSPubSubUnbridge(ctx context.Context, clientID, topic string) error

	// WebSocket operations (only valid in WS context)
	WSSend(ctx context.Context, clientID string, data []byte) error
	WSBroadcast(ctx context.Context, topic string, data []byte) error

	// FunctionInvoke synchronously invokes another function in the same
	// namespace from inside a function (e.g. a persistent rpc-router
	// dispatching client RPCs to per-op handlers). The caller's wallet,
	// JWT claims, and WS client ID are inherited so the invoked function
	// sees the same authenticated identity as the outer call.
	//
	// `name` is the target function name; `payload` is the raw input bytes
	// to feed the function (typically JSON). Returns the function's output
	// bytes on success. Errors (not found, unauthorized, runtime) are
	// returned as Go errors and the caller should surface them as
	// rpc_error to the client.
	FunctionInvoke(ctx context.Context, name string, payload []byte) ([]byte, error)

	// HTTP operations
	HTTPFetch(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, error)

	// Context operations
	GetEnv(ctx context.Context, key string) (string, error)
	GetSecret(ctx context.Context, name string) (string, error)
	GetRequestID(ctx context.Context) string
	GetCallerWallet(ctx context.Context) string
	// GetWSClientID returns the WebSocket client ID when the function was
	// invoked via a WS connection, or empty string otherwise.
	GetWSClientID(ctx context.Context) string
	// GetCallerClaim returns a custom JWT claim's value, or empty if missing
	// or the request was not JWT-authenticated.
	GetCallerClaim(ctx context.Context, name string) string
	// GetCallerJWTSubject returns the JWT `sub` claim independent of the
	// API-key-vs-JWT wallet-resolution heuristic. Empty when the request
	// was not JWT-authenticated. Use this when a function must bind on
	// the JWT-signed identity (e.g. signup-time wallet ownership checks)
	// and the caller may ALSO present an API key. Bug #215.
	GetCallerJWTSubject(ctx context.Context) string

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

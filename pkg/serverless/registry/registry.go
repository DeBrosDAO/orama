// Package registry manages function metadata in RQLite and bytecode in IPFS.
package registry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// Ensure Registry implements FunctionRegistry interface.
var _ FunctionRegistry = (*Registry)(nil)

// Registry coordinates between function storage, IPFS storage, and logging.
type Registry struct {
	functionStore    *FunctionStore
	ipfsStore        *IPFSStore
	invocationLogger *InvocationLogger
	logger           *zap.Logger
}

// NewRegistry creates a new function registry.
func NewRegistry(db rqlite.Client, ipfsClient ipfs.IPFSClient, cfg RegistryConfig, logger *zap.Logger) *Registry {
	return &Registry{
		functionStore:    NewFunctionStore(db, logger),
		ipfsStore:        NewIPFSStore(ipfsClient, cfg.IPFSAPIURL, logger),
		invocationLogger: NewInvocationLogger(db, logger),
		logger:           logger,
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

	oldFn, err := r.functionStore.GetByNameInternal(ctx, fn.Namespace, fn.Name)
	if err != nil && err != ErrFunctionNotFound {
		return nil, &DeployError{FunctionName: fn.Name, Cause: err}
	}

	wasmCID, err := r.ipfsStore.Upload(ctx, wasmBytes, fn.Name)
	if err != nil {
		return nil, &DeployError{FunctionName: fn.Name, Cause: err}
	}

	savedFunc, err := r.functionStore.Save(ctx, fn, wasmCID, oldFn)
	if err != nil {
		return nil, &DeployError{FunctionName: fn.Name, Cause: err}
	}

	if err := r.functionStore.SaveEnvVars(ctx, savedFunc.ID, fn.EnvVars); err != nil {
		return nil, &DeployError{FunctionName: fn.Name, Cause: err}
	}

	r.logger.Info("Function registered",
		zap.String("id", savedFunc.ID),
		zap.String("name", fn.Name),
		zap.String("namespace", fn.Namespace),
		zap.String("wasm_cid", wasmCID),
		zap.Int("version", savedFunc.Version),
		zap.Bool("updated", oldFn != nil),
	)

	return oldFn, nil
}

// Get retrieves a function by name and optional version.
func (r *Registry) Get(ctx context.Context, namespace, name string, version int) (*Function, error) {
	return r.functionStore.Get(ctx, namespace, name, version)
}

// List returns all functions for a namespace.
func (r *Registry) List(ctx context.Context, namespace string) ([]*Function, error) {
	return r.functionStore.List(ctx, namespace)
}

// Delete removes a function. If version is 0, removes all versions.
func (r *Registry) Delete(ctx context.Context, namespace, name string, version int) error {
	return r.functionStore.Delete(ctx, namespace, name, version)
}

// GetWASMBytes retrieves the compiled WASM bytecode for a function.
func (r *Registry) GetWASMBytes(ctx context.Context, wasmCID string) ([]byte, error) {
	if wasmCID == "" {
		return nil, &ValidationError{Field: "wasmCID", Message: "cannot be empty"}
	}
	return r.ipfsStore.Get(ctx, wasmCID)
}

// GetLogs retrieves logs for a function.
func (r *Registry) GetLogs(ctx context.Context, namespace, name string, limit int) ([]LogEntry, error) {
	return r.invocationLogger.GetLogs(ctx, namespace, name, limit)
}

// GetEnvVars retrieves environment variables for a function.
func (r *Registry) GetEnvVars(ctx context.Context, functionID string) (map[string]string, error) {
	return r.functionStore.GetEnvVars(ctx, functionID)
}

// GetByID retrieves a function by its ID.
func (r *Registry) GetByID(ctx context.Context, id string) (*Function, error) {
	return r.functionStore.GetByID(ctx, id)
}

// ListVersions returns all versions of a function.
func (r *Registry) ListVersions(ctx context.Context, namespace, name string) ([]*Function, error) {
	return r.functionStore.ListVersions(ctx, namespace, name)
}

// LogInvocation records a function invocation and its logs to the database.
func (r *Registry) LogInvocation(ctx context.Context,
	id, functionID, requestID string,
	triggerType interface{},
	callerWallet string,
	inputSize, outputSize int,
	startedAt, completedAt interface{},
	durationMS int64,
	status interface{},
	errorMessage string,
	memoryUsedMB float64,
	logs []LogEntry) error {

	var startTime, completeTime time.Time
	if t, ok := startedAt.(time.Time); ok {
		startTime = t
	}
	if t, ok := completedAt.(time.Time); ok {
		completeTime = t
	}

	data := &InvocationRecordData{
		ID:           id,
		FunctionID:   functionID,
		RequestID:    requestID,
		TriggerType:  fmt.Sprintf("%v", triggerType),
		CallerWallet: callerWallet,
		InputSize:    inputSize,
		OutputSize:   outputSize,
		StartedAt:    startTime,
		CompletedAt:  completeTime,
		DurationMS:   durationMS,
		Status:       fmt.Sprintf("%v", status),
		ErrorMessage: errorMessage,
		MemoryUsedMB: memoryUsedMB,
	}

	data.Logs = make([]LogData, len(logs))
	for i, log := range logs {
		data.Logs[i] = LogData{
			Level:     log.Level,
			Message:   log.Message,
			Timestamp: log.Timestamp,
		}
	}

	return r.invocationLogger.Log(ctx, data)
}

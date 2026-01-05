package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Invoker handles function invocation with retry logic and DLQ support.
// It wraps the Engine to provide higher-level invocation semantics.
type Invoker struct {
	engine       *Engine
	registry     FunctionRegistry
	hostServices HostServices
	logger       *zap.Logger
}

// NewInvoker creates a new function invoker.
func NewInvoker(engine *Engine, registry FunctionRegistry, hostServices HostServices, logger *zap.Logger) *Invoker {
	return &Invoker{
		engine:       engine,
		registry:     registry,
		hostServices: hostServices,
		logger:       logger,
	}
}

// InvokeRequest contains the parameters for invoking a function.
type InvokeRequest struct {
	Namespace    string      `json:"namespace"`
	FunctionName string      `json:"function_name"`
	Version      int         `json:"version,omitempty"` // 0 = latest
	Input        []byte      `json:"input"`
	TriggerType  TriggerType `json:"trigger_type"`
	CallerWallet string      `json:"caller_wallet,omitempty"`
	WSClientID   string      `json:"ws_client_id,omitempty"`
}

// InvokeResponse contains the result of a function invocation.
type InvokeResponse struct {
	RequestID  string           `json:"request_id"`
	Output     []byte           `json:"output,omitempty"`
	Status     InvocationStatus `json:"status"`
	Error      string           `json:"error,omitempty"`
	DurationMS int64            `json:"duration_ms"`
	Retries    int              `json:"retries,omitempty"`
}

// Invoke executes a function with automatic retry logic.
func (i *Invoker) Invoke(ctx context.Context, req *InvokeRequest) (*InvokeResponse, error) {
	if req == nil {
		return nil, &ValidationError{Field: "request", Message: "cannot be nil"}
	}
	if req.FunctionName == "" {
		return nil, &ValidationError{Field: "function_name", Message: "cannot be empty"}
	}
	if req.Namespace == "" {
		return nil, &ValidationError{Field: "namespace", Message: "cannot be empty"}
	}

	requestID := uuid.New().String()
	startTime := time.Now()

	// Get function from registry
	fn, err := i.registry.Get(ctx, req.Namespace, req.FunctionName, req.Version)
	if err != nil {
		return &InvokeResponse{
			RequestID:  requestID,
			Status:     InvocationStatusError,
			Error:      err.Error(),
			DurationMS: time.Since(startTime).Milliseconds(),
		}, err
	}

	// Check authorization
	authorized, err := i.CanInvoke(ctx, req.Namespace, req.FunctionName, req.CallerWallet)
	if err != nil || !authorized {
		return &InvokeResponse{
			RequestID:  requestID,
			Status:     InvocationStatusError,
			Error:      "unauthorized",
			DurationMS: time.Since(startTime).Milliseconds(),
		}, ErrUnauthorized
	}

	// Get environment variables
	envVars, err := i.getEnvVars(ctx, fn.ID)
	if err != nil {
		i.logger.Warn("Failed to get env vars", zap.Error(err))
		envVars = make(map[string]string)
	}

	// Build invocation context
	invCtx := &InvocationContext{
		RequestID:    requestID,
		FunctionID:   fn.ID,
		FunctionName: fn.Name,
		Namespace:    fn.Namespace,
		CallerWallet: req.CallerWallet,
		TriggerType:  req.TriggerType,
		WSClientID:   req.WSClientID,
		EnvVars:      envVars,
	}

	// Execute with retry logic
	output, retries, err := i.executeWithRetry(ctx, fn, req.Input, invCtx)

	response := &InvokeResponse{
		RequestID:  requestID,
		Output:     output,
		DurationMS: time.Since(startTime).Milliseconds(),
		Retries:    retries,
	}

	if err != nil {
		response.Status = InvocationStatusError
		response.Error = err.Error()

		// Check if it's a timeout
		if ctx.Err() == context.DeadlineExceeded {
			response.Status = InvocationStatusTimeout
		}

		return response, err
	}

	response.Status = InvocationStatusSuccess
	return response, nil
}

// InvokeByID invokes a function by its ID.
func (i *Invoker) InvokeByID(ctx context.Context, functionID string, input []byte, invCtx *InvocationContext) (*InvokeResponse, error) {
	// Get function from registry by ID
	fn, err := i.getByID(ctx, functionID)
	if err != nil {
		return nil, err
	}

	if invCtx == nil {
		invCtx = &InvocationContext{
			RequestID:    uuid.New().String(),
			FunctionID:   fn.ID,
			FunctionName: fn.Name,
			Namespace:    fn.Namespace,
			TriggerType:  TriggerTypeHTTP,
		}
	}

	startTime := time.Now()
	output, retries, err := i.executeWithRetry(ctx, fn, input, invCtx)

	response := &InvokeResponse{
		RequestID:  invCtx.RequestID,
		Output:     output,
		DurationMS: time.Since(startTime).Milliseconds(),
		Retries:    retries,
	}

	if err != nil {
		response.Status = InvocationStatusError
		response.Error = err.Error()
		return response, err
	}

	response.Status = InvocationStatusSuccess
	return response, nil
}

// InvalidateCache removes a compiled module from the engine's cache.
func (i *Invoker) InvalidateCache(wasmCID string) {
	i.engine.Invalidate(wasmCID)
}

// executeWithRetry executes a function with retry logic and DLQ.
func (i *Invoker) executeWithRetry(ctx context.Context, fn *Function, input []byte, invCtx *InvocationContext) ([]byte, int, error) {
	var lastErr error
	var output []byte

	maxAttempts := fn.RetryCount + 1 // Initial attempt + retries
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Check if context is cancelled
		if ctx.Err() != nil {
			return nil, attempt, ctx.Err()
		}

		// Execute the function
		output, lastErr = i.engine.Execute(ctx, fn, input, invCtx)
		if lastErr == nil {
			return output, attempt, nil
		}

		i.logger.Warn("Function execution failed",
			zap.String("function", fn.Name),
			zap.String("request_id", invCtx.RequestID),
			zap.Int("attempt", attempt+1),
			zap.Int("max_attempts", maxAttempts),
			zap.Error(lastErr),
		)

		// Don't retry on certain errors
		if !i.isRetryable(lastErr) {
			break
		}

		// Don't wait after the last attempt
		if attempt < maxAttempts-1 {
			delay := i.calculateBackoff(fn.RetryDelaySeconds, attempt)
			select {
			case <-ctx.Done():
				return nil, attempt + 1, ctx.Err()
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	// All retries exhausted - send to DLQ if configured
	if fn.DLQTopic != "" {
		i.sendToDLQ(ctx, fn, input, invCtx, lastErr)
	}

	return nil, maxAttempts - 1, lastErr
}

// isRetryable determines if an error should trigger a retry.
func (i *Invoker) isRetryable(err error) bool {
	// Don't retry validation errors or not-found errors
	if IsNotFound(err) {
		return false
	}

	// Don't retry resource exhaustion (rate limits, memory)
	if IsResourceExhausted(err) {
		return false
	}

	// Retry service unavailable errors
	if IsServiceUnavailable(err) {
		return true
	}

	// Retry execution errors (could be transient)
	var execErr *ExecutionError
	if ok := errorAs(err, &execErr); ok {
		return true
	}

	// Default to retryable for unknown errors
	return true
}

// calculateBackoff calculates the delay before the next retry attempt.
// Uses exponential backoff with jitter.
func (i *Invoker) calculateBackoff(baseDelaySeconds, attempt int) time.Duration {
	if baseDelaySeconds <= 0 {
		baseDelaySeconds = 5
	}

	// Exponential backoff: delay * 2^attempt
	delay := time.Duration(baseDelaySeconds) * time.Second
	for j := 0; j < attempt; j++ {
		delay *= 2
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
			break
		}
	}

	return delay
}

// sendToDLQ sends a failed invocation to the dead letter queue.
func (i *Invoker) sendToDLQ(ctx context.Context, fn *Function, input []byte, invCtx *InvocationContext, err error) {
	dlqMessage := DLQMessage{
		FunctionID:   fn.ID,
		FunctionName: fn.Name,
		Namespace:    fn.Namespace,
		RequestID:    invCtx.RequestID,
		Input:        input,
		Error:        err.Error(),
		FailedAt:     time.Now(),
		TriggerType:  invCtx.TriggerType,
		CallerWallet: invCtx.CallerWallet,
	}

	data, marshalErr := json.Marshal(dlqMessage)
	if marshalErr != nil {
		i.logger.Error("Failed to marshal DLQ message",
			zap.Error(marshalErr),
			zap.String("function", fn.Name),
		)
		return
	}

	// Publish to DLQ topic via host services
	if err := i.hostServices.PubSubPublish(ctx, fn.DLQTopic, data); err != nil {
		i.logger.Error("Failed to send to DLQ",
			zap.Error(err),
			zap.String("function", fn.Name),
			zap.String("dlq_topic", fn.DLQTopic),
		)
	} else {
		i.logger.Info("Sent failed invocation to DLQ",
			zap.String("function", fn.Name),
			zap.String("dlq_topic", fn.DLQTopic),
			zap.String("request_id", invCtx.RequestID),
		)
	}
}

// getEnvVars retrieves environment variables for a function.
func (i *Invoker) getEnvVars(ctx context.Context, functionID string) (map[string]string, error) {
	// Type assert to get extended registry methods
	if reg, ok := i.registry.(*Registry); ok {
		return reg.GetEnvVars(ctx, functionID)
	}
	return nil, nil
}

// getByID retrieves a function by ID.
func (i *Invoker) getByID(ctx context.Context, functionID string) (*Function, error) {
	// Type assert to get extended registry methods
	if reg, ok := i.registry.(*Registry); ok {
		return reg.GetByID(ctx, functionID)
	}
	return nil, ErrFunctionNotFound
}

// DLQMessage represents a message sent to the dead letter queue.
type DLQMessage struct {
	FunctionID   string      `json:"function_id"`
	FunctionName string      `json:"function_name"`
	Namespace    string      `json:"namespace"`
	RequestID    string      `json:"request_id"`
	Input        []byte      `json:"input"`
	Error        string      `json:"error"`
	FailedAt     time.Time   `json:"failed_at"`
	TriggerType  TriggerType `json:"trigger_type"`
	CallerWallet string      `json:"caller_wallet,omitempty"`
}

// errorAs is a helper to avoid import of errors package.
func errorAs(err error, target interface{}) bool {
	if err == nil {
		return false
	}
	// Simple type assertion for our custom error types
	switch t := target.(type) {
	case **ExecutionError:
		if e, ok := err.(*ExecutionError); ok {
			*t = e
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// Batch Invocation (for future use)
// -----------------------------------------------------------------------------

// BatchInvokeRequest contains parameters for batch invocation.
type BatchInvokeRequest struct {
	Requests []*InvokeRequest `json:"requests"`
}

// BatchInvokeResponse contains results of batch invocation.
type BatchInvokeResponse struct {
	Responses []*InvokeResponse `json:"responses"`
	Duration  time.Duration     `json:"duration"`
}

// BatchInvoke executes multiple functions in parallel.
func (i *Invoker) BatchInvoke(ctx context.Context, req *BatchInvokeRequest) (*BatchInvokeResponse, error) {
	if req == nil || len(req.Requests) == 0 {
		return nil, &ValidationError{Field: "requests", Message: "cannot be empty"}
	}

	startTime := time.Now()
	responses := make([]*InvokeResponse, len(req.Requests))

	// For simplicity, execute sequentially for now
	// TODO: Implement parallel execution with goroutines and semaphore
	for idx, invReq := range req.Requests {
		resp, err := i.Invoke(ctx, invReq)
		if err != nil && resp == nil {
			responses[idx] = &InvokeResponse{
				RequestID: uuid.New().String(),
				Status:    InvocationStatusError,
				Error:     err.Error(),
			}
		} else {
			responses[idx] = resp
		}
	}

	return &BatchInvokeResponse{
		Responses: responses,
		Duration:  time.Since(startTime),
	}, nil
}

// -----------------------------------------------------------------------------
// Public Invocation Helpers
// -----------------------------------------------------------------------------

// CanInvoke checks if a caller is authorized to invoke a function.
func (i *Invoker) CanInvoke(ctx context.Context, namespace, functionName string, callerWallet string) (bool, error) {
	fn, err := i.registry.Get(ctx, namespace, functionName, 0)
	if err != nil {
		return false, err
	}

	// Public functions can be invoked by anyone
	if fn.IsPublic {
		return true, nil
	}

	// Non-public functions require the caller to be in the same namespace
	// (simplified authorization - can be extended)
	if callerWallet == "" {
		return false, nil
	}

	// For now, just check if caller wallet matches namespace
	// In production, you'd check group membership, roles, etc.
	return callerWallet == namespace || fn.CreatedBy == callerWallet, nil
}

// GetFunctionInfo returns basic info about a function for invocation.
func (i *Invoker) GetFunctionInfo(ctx context.Context, namespace, functionName string, version int) (*Function, error) {
	return i.registry.Get(ctx, namespace, functionName, version)
}

// ValidateInput performs basic input validation.
func (i *Invoker) ValidateInput(input []byte, maxSize int) error {
	if maxSize > 0 && len(input) > maxSize {
		return &ValidationError{
			Field:   "input",
			Message: fmt.Sprintf("exceeds maximum size of %d bytes", maxSize),
		}
	}
	return nil
}

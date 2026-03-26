package serverless

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// EnsureInvocationContext creates a default context if none is provided.
func EnsureInvocationContext(ctx *InvocationContext, fn *Function) *InvocationContext {
	if ctx != nil {
		return ctx
	}

	return &InvocationContext{
		RequestID:    uuid.New().String(),
		FunctionID:   fn.ID,
		FunctionName: fn.Name,
		Namespace:    fn.Namespace,
		TriggerType:  TriggerTypeHTTP,
	}
}

// CreateTimeoutContext creates a context with timeout based on function configuration.
func CreateTimeoutContext(ctx context.Context, fn *Function, maxTimeout int) (context.Context, context.CancelFunc) {
	timeout := time.Duration(fn.TimeoutSeconds) * time.Second
	if timeout > time.Duration(maxTimeout)*time.Second {
		timeout = time.Duration(maxTimeout) * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

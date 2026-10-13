package hostfunctions

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// LogInfo logs an info message into the per-invocation LogBuffer attached to
// ctx (see log_buffer.go), and to the gateway's own log either way.
func (h *HostFunctions) LogInfo(ctx context.Context, message string) {
	entry := serverless.LogEntry{
		Level:     "info",
		Message:   message,
		Timestamp: time.Now(),
	}
	// The per-invocation buffer, or nowhere. A shared slice was how one
	// invocation's log lines ended up in another's record (bugboard #108); a
	// call with no buffer is one outside any invocation, and there is no
	// record for it to belong to.
	if buf := serverless.LogBufferFromCtx(ctx); buf != nil {
		buf.Append(entry)
	}

	h.logger.Info(message,
		zap.String("request_id", h.GetRequestID(ctx)),
		zap.String("level", "function"),
	)
}

// LogError logs an error message. See LogInfo.
func (h *HostFunctions) LogError(ctx context.Context, message string) {
	entry := serverless.LogEntry{
		Level:     "error",
		Message:   message,
		Timestamp: time.Now(),
	}
	// The per-invocation buffer, or nowhere. A shared slice was how one
	// invocation's log lines ended up in another's record (bugboard #108); a
	// call with no buffer is one outside any invocation, and there is no
	// record for it to belong to.
	if buf := serverless.LogBufferFromCtx(ctx); buf != nil {
		buf.Append(entry)
	}

	h.logger.Error(message,
		zap.String("request_id", h.GetRequestID(ctx)),
		zap.String("level", "function"),
	)
}

// EnqueueBackground queues a function for background execution.
func (h *HostFunctions) EnqueueBackground(ctx context.Context, functionName string, payload []byte) (string, error) {
	// This will be implemented when JobManager is integrated
	// For now, return an error indicating it's not yet available
	return "", &serverless.HostFunctionError{Function: "enqueue_background", Cause: fmt.Errorf("background jobs not yet implemented")}
}

// ScheduleOnce schedules a function to run once at a specific time.
func (h *HostFunctions) ScheduleOnce(ctx context.Context, functionName string, runAt time.Time, payload []byte) (string, error) {
	// This will be implemented when Scheduler is integrated
	return "", &serverless.HostFunctionError{Function: "schedule_once", Cause: fmt.Errorf("timers not yet implemented")}
}

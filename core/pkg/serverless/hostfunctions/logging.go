package hostfunctions

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// LogInfo logs an info message. Writes to the per-invocation LogBuffer
// attached to ctx (see log_buffer.go); falls back to the legacy
// HostFunctions singleton slice when no buffer is on ctx (test paths
// that haven't migrated).
//
// Bugboard #108 fix: previously this always wrote to the singleton
// `h.logs`, causing cross-contamination between concurrent invocations
// (push-fanout's invocation record captured rpc-router's log lines).
func (h *HostFunctions) LogInfo(ctx context.Context, message string) {
	entry := serverless.LogEntry{
		Level:     "info",
		Message:   message,
		Timestamp: time.Now(),
	}
	if buf := serverless.LogBufferFromCtx(ctx); buf != nil {
		buf.Append(entry)
	} else {
		h.logsLock.Lock()
		h.logs = append(h.logs, entry)
		h.logsLock.Unlock()
	}

	h.logger.Info(message,
		zap.String("request_id", h.GetRequestID(ctx)),
		zap.String("level", "function"),
	)
}

// LogError logs an error message. See LogInfo for the per-invocation
// LogBuffer / singleton fallback semantics — same code path, same
// bugboard #108 rationale.
func (h *HostFunctions) LogError(ctx context.Context, message string) {
	entry := serverless.LogEntry{
		Level:     "error",
		Message:   message,
		Timestamp: time.Now(),
	}
	if buf := serverless.LogBufferFromCtx(ctx); buf != nil {
		buf.Append(entry)
	} else {
		h.logsLock.Lock()
		h.logs = append(h.logs, entry)
		h.logsLock.Unlock()
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

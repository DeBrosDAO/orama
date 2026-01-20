package hostfunctions

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// LogInfo logs an info message.
func (h *HostFunctions) LogInfo(ctx context.Context, message string) {
	h.logsLock.Lock()
	defer h.logsLock.Unlock()

	h.logs = append(h.logs, serverless.LogEntry{
		Level:     "info",
		Message:   message,
		Timestamp: time.Now(),
	})

	h.logger.Info(message,
		zap.String("request_id", h.GetRequestID(ctx)),
		zap.String("level", "function"),
	)
}

// LogError logs an error message.
func (h *HostFunctions) LogError(ctx context.Context, message string) {
	h.logsLock.Lock()
	defer h.logsLock.Unlock()

	h.logs = append(h.logs, serverless.LogEntry{
		Level:     "error",
		Message:   message,
		Timestamp: time.Now(),
	})

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

package serverless

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// TestClassifyInvokeError verifies the invoke-error → (status, code, retryable)
// mapping shared by the HTTP and WS paths. The key behavior (bugboard #137): a
// cold-WASM fetch timeout is a TRANSIENT infra failure (FUNCTION_UNAVAILABLE,
// retryable) — distinct from a genuine function error (not retryable).
func TestClassifyInvokeError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantStatus    int
		wantCode      httputil.RPCErrorCode
		wantRetryable bool
	}{
		{
			name:          "cold WASM fetch timeout is retryable FUNCTION_UNAVAILABLE",
			err:           serverless.ErrWASMFetchTimeout,
			wantStatus:    http.StatusServiceUnavailable,
			wantCode:      httputil.ErrCodeFunctionUnavailable,
			wantRetryable: true,
		},
		{
			name:          "wrapped cold WASM fetch timeout still classifies as transient",
			err:           fmt.Errorf("failed to fetch WASM: %w", serverless.ErrWASMFetchTimeout),
			wantStatus:    http.StatusServiceUnavailable,
			wantCode:      httputil.ErrCodeFunctionUnavailable,
			wantRetryable: true,
		},
		{
			// Mirrors the real production chain: engine.go wraps the fetch error
			// in *ExecutionError{Cause: ...}. errors.Is must still see through it.
			name:          "ExecutionError wrapping cold-WASM timeout classifies transient",
			err:           &serverless.ExecutionError{FunctionName: "fn", Cause: fmt.Errorf("failed to fetch WASM: %w", serverless.ErrWASMFetchTimeout)},
			wantStatus:    http.StatusServiceUnavailable,
			wantCode:      httputil.ErrCodeFunctionUnavailable,
			wantRetryable: true,
		},
		{
			name:          "generic function error is non-retryable FUNCTION_EXECUTION_FAILED",
			err:           errors.New("panic: index out of range"),
			wantStatus:    http.StatusInternalServerError,
			wantCode:      httputil.ErrCodeFunctionExecution,
			wantRetryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code, retryable := classifyInvokeError(tt.err)
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
			if retryable != tt.wantRetryable {
				t.Errorf("retryable = %v, want %v", retryable, tt.wantRetryable)
			}
		})
	}
}

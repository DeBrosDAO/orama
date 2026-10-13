package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestIsCacheUnreachable(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"nil":                {nil, false},
		"read timeout":       {fmt.Errorf("get: %w", timeoutErr{}), true},
		"connection refused": {&net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		"deadline exceeded":  {fmt.Errorf("put: %w", context.DeadlineExceeded), true},
		"eof":                {io.EOF, true},
		"key too large":      {errors.New("key too large"), false},
	} {
		if got := isCacheUnreachable(tc.err); got != tc.want {
			t.Errorf("%s: isCacheUnreachable = %v, want %v", name, got, tc.want)
		}
	}
}

func TestPutFailure_unreachableIs503(t *testing.T) {
	status, msg := putFailure(fmt.Errorf("put: %w", timeoutErr{}))
	if status != http.StatusServiceUnavailable || msg != cacheUnavailableMessage {
		t.Fatalf("putFailure = %d %q, want 503 %q", status, msg, cacheUnavailableMessage)
	}
	if status, _ := putFailure(errors.New("boom")); status != http.StatusInternalServerError {
		t.Fatalf("an unrelated error answered %d, want 500", status)
	}
}

func TestWriteUnavailable_carriesRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	writeUnavailable(rec)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

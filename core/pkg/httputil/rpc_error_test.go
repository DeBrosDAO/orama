package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) RPCErrorEnvelope {
	t.Helper()
	var env RPCErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env
}

func TestWriteRPCError_canonical_shape(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteRPCError(rec, http.StatusBadRequest,
		ErrCodeValidationFailed, "username required")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	env := decode(t, rec)
	if env.OK {
		t.Error("ok must be false on error envelope")
	}
	if env.Error == nil {
		t.Fatal("error must be non-nil")
	}
	if env.Error.Code != ErrCodeValidationFailed {
		t.Errorf("code = %s, want %s", env.Error.Code, ErrCodeValidationFailed)
	}
	if env.Error.Message != "username required" {
		t.Errorf("message = %q", env.Error.Message)
	}
	if env.Error.Retryable {
		t.Error("validation errors should not default to retryable")
	}
}

// The contract: NEVER return an empty error.message. Bug #212 was clients
// seeing "RPC failed" because the gateway omitted the message field. This
// test locks in the fallback path.
func TestWriteRPCError_empty_message_is_filled_with_default(t *testing.T) {
	cases := []struct {
		code        RPCErrorCode
		wantInclude string
	}{
		{ErrCodeValidationFailed, "validation"},
		{ErrCodeNotFound, "not found"},
		{ErrCodeRateLimited, "rate limit"},
		{ErrCodeInternal, "internal"},
		{ErrCodeFunctionExecution, "function execution"},
	}
	for _, c := range cases {
		t.Run(string(c.code), func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteRPCError(rec, http.StatusInternalServerError, c.code, "")
			env := decode(t, rec)
			if env.Error.Message == "" {
				t.Errorf("message must NEVER be empty even when caller passed empty string")
			}
			if !strings.Contains(strings.ToLower(env.Error.Message), c.wantInclude) {
				t.Errorf("message %q should include %q", env.Error.Message, c.wantInclude)
			}
		})
	}
}

func TestWriteRPCError_empty_code_falls_back_to_internal(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteRPCError(rec, http.StatusInternalServerError, "", "")
	env := decode(t, rec)
	if env.Error.Code != ErrCodeInternal {
		t.Errorf("empty code should fall back to INTERNAL, got %s", env.Error.Code)
	}
	if env.Error.Message == "" {
		t.Error("default message must be populated")
	}
}

func TestWriteRPCError_with_retry_after_sets_header_and_retryable(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteRPCError(rec, http.StatusTooManyRequests,
		ErrCodeRateLimited, "wallet over cap",
		WithRetryAfter(2.5))

	if got := rec.Header().Get("Retry-After"); got != "2.5" {
		t.Errorf("Retry-After header = %q, want 2.5", got)
	}
	env := decode(t, rec)
	if !env.Error.Retryable {
		t.Error("WithRetryAfter must imply Retryable=true")
	}
	if env.Error.RetryAfter != 2.5 {
		t.Errorf("retry_after = %v, want 2.5", env.Error.RetryAfter)
	}
}

func TestWriteRPCError_with_request_id(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteRPCError(rec, http.StatusBadRequest,
		ErrCodeValidationFailed, "bad input",
		WithRequestID("req-abc"))
	env := decode(t, rec)
	if env.Error.RequestID != "req-abc" {
		t.Errorf("request_id = %q, want req-abc", env.Error.RequestID)
	}
}

func TestWriteRPCError_default_retryable_for_transient_codes(t *testing.T) {
	cases := []struct {
		code RPCErrorCode
		want bool
	}{
		{ErrCodeRateLimited, true},
		{ErrCodeServiceUnavailable, true},
		{ErrCodeTimeout, true},
		{ErrCodeFunctionUnavailable, true},  // transient cold-WASM infra failure
		{ErrCodeFunctionExecution, false},   // genuine function error
		{ErrCodeValidationFailed, false},
		{ErrCodeNotFound, false},
		{ErrCodeForbidden, false},
		{ErrCodeInternal, false},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		WriteRPCError(rec, http.StatusInternalServerError, c.code, "")
		env := decode(t, rec)
		if env.Error.Retryable != c.want {
			t.Errorf("%s: retryable=%v, want %v", c.code, env.Error.Retryable, c.want)
		}
	}
}

func TestFormatFloat(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1.0, "1"},
		{1.5, "1.5"},
		{2.45, "2.5"}, // rounds to 1 decimal
		{-1.0, "0"},   // negative clamps to 0
	}
	for _, c := range cases {
		if got := formatFloat(c.in); got != c.want {
			t.Errorf("formatFloat(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

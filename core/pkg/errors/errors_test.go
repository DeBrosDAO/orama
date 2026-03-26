package errors

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestValidationError(t *testing.T) {
	tests := []struct {
		name          string
		field         string
		message       string
		value         interface{}
		expectedError string
	}{
		{
			name:          "with field",
			field:         "email",
			message:       "invalid email format",
			value:         "not-an-email",
			expectedError: "validation error: email: invalid email format",
		},
		{
			name:          "without field",
			field:         "",
			message:       "invalid input",
			value:         nil,
			expectedError: "validation error: invalid input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewValidationError(tt.field, tt.message, tt.value)
			if err.Error() != tt.expectedError {
				t.Errorf("Expected error %q, got %q", tt.expectedError, err.Error())
			}
			if err.Code() != CodeValidation {
				t.Errorf("Expected code %q, got %q", CodeValidation, err.Code())
			}
			if err.Field != tt.field {
				t.Errorf("Expected field %q, got %q", tt.field, err.Field)
			}
		})
	}
}

func TestNotFoundError(t *testing.T) {
	tests := []struct {
		name          string
		resource      string
		id            string
		expectedError string
	}{
		{
			name:          "with ID",
			resource:      "user",
			id:            "123",
			expectedError: "user with ID '123' not found",
		},
		{
			name:          "without ID",
			resource:      "user",
			id:            "",
			expectedError: "user not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewNotFoundError(tt.resource, tt.id)
			if err.Error() != tt.expectedError {
				t.Errorf("Expected error %q, got %q", tt.expectedError, err.Error())
			}
			if err.Code() != CodeNotFound {
				t.Errorf("Expected code %q, got %q", CodeNotFound, err.Code())
			}
			if err.Resource != tt.resource {
				t.Errorf("Expected resource %q, got %q", tt.resource, err.Resource)
			}
		})
	}
}

func TestUnauthorizedError(t *testing.T) {
	t.Run("default message", func(t *testing.T) {
		err := NewUnauthorizedError("")
		if err.Message() != "authentication required" {
			t.Errorf("Expected message 'authentication required', got %q", err.Message())
		}
		if err.Code() != CodeUnauthorized {
			t.Errorf("Expected code %q, got %q", CodeUnauthorized, err.Code())
		}
	})

	t.Run("custom message", func(t *testing.T) {
		err := NewUnauthorizedError("invalid token")
		if err.Message() != "invalid token" {
			t.Errorf("Expected message 'invalid token', got %q", err.Message())
		}
	})

	t.Run("with realm", func(t *testing.T) {
		err := NewUnauthorizedError("").WithRealm("api")
		if err.Realm != "api" {
			t.Errorf("Expected realm 'api', got %q", err.Realm)
		}
	})
}

func TestForbiddenError(t *testing.T) {
	tests := []struct {
		name          string
		resource      string
		action        string
		expectedMsg   string
	}{
		{
			name:        "with resource and action",
			resource:    "function",
			action:      "delete",
			expectedMsg: "forbidden: cannot delete function",
		},
		{
			name:        "without details",
			resource:    "",
			action:      "",
			expectedMsg: "forbidden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewForbiddenError(tt.resource, tt.action)
			if err.Message() != tt.expectedMsg {
				t.Errorf("Expected message %q, got %q", tt.expectedMsg, err.Message())
			}
			if err.Code() != CodeForbidden {
				t.Errorf("Expected code %q, got %q", CodeForbidden, err.Code())
			}
		})
	}
}

func TestConflictError(t *testing.T) {
	tests := []struct {
		name          string
		resource      string
		field         string
		value         string
		expectedMsg   string
	}{
		{
			name:        "with field",
			resource:    "user",
			field:       "email",
			value:       "test@example.com",
			expectedMsg: "user with email='test@example.com' already exists",
		},
		{
			name:        "without field",
			resource:    "user",
			field:       "",
			value:       "",
			expectedMsg: "user already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewConflictError(tt.resource, tt.field, tt.value)
			if err.Message() != tt.expectedMsg {
				t.Errorf("Expected message %q, got %q", tt.expectedMsg, err.Message())
			}
			if err.Code() != CodeConflict {
				t.Errorf("Expected code %q, got %q", CodeConflict, err.Code())
			}
		})
	}
}

func TestInternalError(t *testing.T) {
	t.Run("with cause", func(t *testing.T) {
		cause := errors.New("database connection failed")
		err := NewInternalError("failed to save user", cause)

		if err.Message() != "failed to save user" {
			t.Errorf("Expected message 'failed to save user', got %q", err.Message())
		}
		if err.Unwrap() != cause {
			t.Errorf("Expected cause to be preserved")
		}
		if !strings.Contains(err.Error(), "database connection failed") {
			t.Errorf("Expected error to contain cause: %q", err.Error())
		}
	})

	t.Run("with operation", func(t *testing.T) {
		err := NewInternalError("operation failed", nil).WithOperation("saveUser")
		if err.Operation != "saveUser" {
			t.Errorf("Expected operation 'saveUser', got %q", err.Operation)
		}
	})
}

func TestServiceError(t *testing.T) {
	cause := errors.New("connection refused")
	err := NewServiceError("rqlite", "database unavailable", 503, cause)

	if err.Service != "rqlite" {
		t.Errorf("Expected service 'rqlite', got %q", err.Service)
	}
	if err.StatusCode != 503 {
		t.Errorf("Expected status code 503, got %d", err.StatusCode)
	}
	if err.Unwrap() != cause {
		t.Errorf("Expected cause to be preserved")
	}
}

func TestTimeoutError(t *testing.T) {
	err := NewTimeoutError("function execution", "30s")

	if err.Operation != "function execution" {
		t.Errorf("Expected operation 'function execution', got %q", err.Operation)
	}
	if err.Duration != "30s" {
		t.Errorf("Expected duration '30s', got %q", err.Duration)
	}
	if !strings.Contains(err.Message(), "timeout") {
		t.Errorf("Expected message to contain 'timeout': %q", err.Message())
	}
}

func TestRateLimitError(t *testing.T) {
	err := NewRateLimitError(100, 60)

	if err.Limit != 100 {
		t.Errorf("Expected limit 100, got %d", err.Limit)
	}
	if err.RetryAfter != 60 {
		t.Errorf("Expected retry after 60, got %d", err.RetryAfter)
	}
	if err.Code() != CodeRateLimit {
		t.Errorf("Expected code %q, got %q", CodeRateLimit, err.Code())
	}
}

func TestWrap(t *testing.T) {
	t.Run("wrap standard error", func(t *testing.T) {
		original := errors.New("original error")
		wrapped := Wrap(original, "additional context")

		if !strings.Contains(wrapped.Error(), "additional context") {
			t.Errorf("Expected wrapped error to contain context: %q", wrapped.Error())
		}
		if !errors.Is(wrapped, original) {
			t.Errorf("Expected wrapped error to preserve original error")
		}
	})

	t.Run("wrap custom error", func(t *testing.T) {
		original := NewNotFoundError("user", "123")
		wrapped := Wrap(original, "failed to fetch user")

		if !strings.Contains(wrapped.Error(), "failed to fetch user") {
			t.Errorf("Expected wrapped error to contain new context: %q", wrapped.Error())
		}
		if errors.Unwrap(wrapped) != original {
			t.Errorf("Expected wrapped error to preserve original error")
		}
	})

	t.Run("wrap nil error", func(t *testing.T) {
		wrapped := Wrap(nil, "context")
		if wrapped != nil {
			t.Errorf("Expected Wrap(nil) to return nil, got %v", wrapped)
		}
	})
}

func TestWrapf(t *testing.T) {
	original := errors.New("connection failed")
	wrapped := Wrapf(original, "failed to connect to %s:%d", "localhost", 5432)

	expected := "failed to connect to localhost:5432"
	if !strings.Contains(wrapped.Error(), expected) {
		t.Errorf("Expected wrapped error to contain %q, got %q", expected, wrapped.Error())
	}
}

func TestErrorChaining(t *testing.T) {
	// Create a chain of errors
	root := errors.New("root cause")
	level1 := Wrap(root, "level 1")
	level2 := Wrap(level1, "level 2")
	level3 := Wrap(level2, "level 3")

	// Test unwrapping
	if !errors.Is(level3, root) {
		t.Errorf("Expected error chain to preserve root cause")
	}

	// Test that we can unwrap multiple levels
	unwrapped := errors.Unwrap(level3)
	if unwrapped != level2 {
		t.Errorf("Expected first unwrap to return level2")
	}

	unwrapped = errors.Unwrap(unwrapped)
	if unwrapped != level1 {
		t.Errorf("Expected second unwrap to return level1")
	}
}

func TestStackTrace(t *testing.T) {
	err := NewInternalError("test error", nil)

	if len(err.Stack()) == 0 {
		t.Errorf("Expected stack trace to be captured")
	}

	trace := err.StackTrace()
	if trace == "" {
		t.Errorf("Expected stack trace string to be non-empty")
	}

	// Stack trace should contain this test function
	if !strings.Contains(trace, "TestStackTrace") {
		t.Errorf("Expected stack trace to contain test function name: %s", trace)
	}
}

func TestNew(t *testing.T) {
	err := New("test error")

	if err.Error() != "test error" {
		t.Errorf("Expected error message 'test error', got %q", err.Error())
	}

	// Check that it implements our Error interface
	var customErr Error
	if !errors.As(err, &customErr) {
		t.Errorf("Expected New() to return an Error interface")
	}
}

func TestNewf(t *testing.T) {
	err := Newf("error code: %d, message: %s", 404, "not found")

	expected := "error code: 404, message: not found"
	if err.Error() != expected {
		t.Errorf("Expected error message %q, got %q", expected, err.Error())
	}
}

func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrNotFound", ErrNotFound},
		{"ErrUnauthorized", ErrUnauthorized},
		{"ErrForbidden", ErrForbidden},
		{"ErrConflict", ErrConflict},
		{"ErrInvalidInput", ErrInvalidInput},
		{"ErrTimeout", ErrTimeout},
		{"ErrServiceUnavailable", ErrServiceUnavailable},
		{"ErrInternal", ErrInternal},
		{"ErrTooManyRequests", ErrTooManyRequests},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := fmt.Errorf("wrapped: %w", tt.err)
			if !errors.Is(wrapped, tt.err) {
				t.Errorf("Expected errors.Is to work with sentinel error")
			}
		})
	}
}

func BenchmarkNewValidationError(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = NewValidationError("field", "message", "value")
	}
}

func BenchmarkWrap(b *testing.B) {
	err := errors.New("original error")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Wrap(err, "wrapped")
	}
}

func BenchmarkStackTrace(b *testing.B) {
	err := NewInternalError("test", nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = err.StackTrace()
	}
}

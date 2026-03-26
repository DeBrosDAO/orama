package errors

import (
	"errors"
	"testing"
)

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "NotFoundError",
			err:      NewNotFoundError("user", "123"),
			expected: true,
		},
		{
			name:     "sentinel ErrNotFound",
			err:      ErrNotFound,
			expected: true,
		},
		{
			name:     "wrapped NotFoundError",
			err:      Wrap(NewNotFoundError("user", "123"), "context"),
			expected: true,
		},
		{
			name:     "wrapped sentinel",
			err:      Wrap(ErrNotFound, "context"),
			expected: true,
		},
		{
			name:     "other error",
			err:      NewInternalError("internal", nil),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNotFound(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsValidation(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "ValidationError",
			err:      NewValidationError("field", "invalid", nil),
			expected: true,
		},
		{
			name:     "wrapped ValidationError",
			err:      Wrap(NewValidationError("field", "invalid", nil), "context"),
			expected: true,
		},
		{
			name:     "other error",
			err:      NewNotFoundError("user", "123"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidation(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsUnauthorized(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "UnauthorizedError",
			err:      NewUnauthorizedError("invalid token"),
			expected: true,
		},
		{
			name:     "sentinel ErrUnauthorized",
			err:      ErrUnauthorized,
			expected: true,
		},
		{
			name:     "wrapped UnauthorizedError",
			err:      Wrap(NewUnauthorizedError("invalid token"), "context"),
			expected: true,
		},
		{
			name:     "other error",
			err:      NewForbiddenError("resource", "action"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsUnauthorized(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsForbidden(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "ForbiddenError",
			err:      NewForbiddenError("resource", "action"),
			expected: true,
		},
		{
			name:     "sentinel ErrForbidden",
			err:      ErrForbidden,
			expected: true,
		},
		{
			name:     "wrapped ForbiddenError",
			err:      Wrap(NewForbiddenError("resource", "action"), "context"),
			expected: true,
		},
		{
			name:     "other error",
			err:      NewUnauthorizedError("invalid token"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsForbidden(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsConflict(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "ConflictError",
			err:      NewConflictError("user", "email", "test@example.com"),
			expected: true,
		},
		{
			name:     "sentinel ErrConflict",
			err:      ErrConflict,
			expected: true,
		},
		{
			name:     "wrapped ConflictError",
			err:      Wrap(NewConflictError("user", "email", "test@example.com"), "context"),
			expected: true,
		},
		{
			name:     "other error",
			err:      NewNotFoundError("user", "123"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsConflict(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsTimeout(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "TimeoutError",
			err:      NewTimeoutError("operation", "30s"),
			expected: true,
		},
		{
			name:     "sentinel ErrTimeout",
			err:      ErrTimeout,
			expected: true,
		},
		{
			name:     "wrapped TimeoutError",
			err:      Wrap(NewTimeoutError("operation", "30s"), "context"),
			expected: true,
		},
		{
			name:     "other error",
			err:      NewInternalError("internal", nil),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTimeout(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsRateLimit(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "RateLimitError",
			err:      NewRateLimitError(100, 60),
			expected: true,
		},
		{
			name:     "sentinel ErrTooManyRequests",
			err:      ErrTooManyRequests,
			expected: true,
		},
		{
			name:     "wrapped RateLimitError",
			err:      Wrap(NewRateLimitError(100, 60), "context"),
			expected: true,
		},
		{
			name:     "other error",
			err:      NewTimeoutError("operation", "30s"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRateLimit(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsServiceUnavailable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "ServiceError",
			err:      NewServiceError("rqlite", "unavailable", 503, nil),
			expected: true,
		},
		{
			name:     "sentinel ErrServiceUnavailable",
			err:      ErrServiceUnavailable,
			expected: true,
		},
		{
			name:     "wrapped ServiceError",
			err:      Wrap(NewServiceError("rqlite", "unavailable", 503, nil), "context"),
			expected: true,
		},
		{
			name:     "other error",
			err:      NewTimeoutError("operation", "30s"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsServiceUnavailable(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsInternal(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "InternalError",
			err:      NewInternalError("internal error", nil),
			expected: true,
		},
		{
			name:     "sentinel ErrInternal",
			err:      ErrInternal,
			expected: true,
		},
		{
			name:     "wrapped InternalError",
			err:      Wrap(NewInternalError("internal error", nil), "context"),
			expected: true,
		},
		{
			name:     "other error",
			err:      NewNotFoundError("user", "123"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsInternal(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "timeout error",
			err:      NewTimeoutError("operation", "30s"),
			expected: true,
		},
		{
			name:     "service unavailable error",
			err:      NewServiceError("rqlite", "unavailable", 503, nil),
			expected: true,
		},
		{
			name:     "not found error",
			err:      NewNotFoundError("user", "123"),
			expected: false,
		},
		{
			name:     "validation error",
			err:      NewValidationError("field", "invalid", nil),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldRetry(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestGetErrorCode(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectedCode string
	}{
		{
			name:         "nil error",
			err:          nil,
			expectedCode: CodeOK,
		},
		{
			name:         "validation error",
			err:          NewValidationError("field", "invalid", nil),
			expectedCode: CodeValidation,
		},
		{
			name:         "not found error",
			err:          NewNotFoundError("user", "123"),
			expectedCode: CodeNotFound,
		},
		{
			name:         "unauthorized error",
			err:          NewUnauthorizedError("invalid token"),
			expectedCode: CodeUnauthorized,
		},
		{
			name:         "forbidden error",
			err:          NewForbiddenError("resource", "action"),
			expectedCode: CodeForbidden,
		},
		{
			name:         "conflict error",
			err:          NewConflictError("user", "email", "test@example.com"),
			expectedCode: CodeConflict,
		},
		{
			name:         "timeout error",
			err:          NewTimeoutError("operation", "30s"),
			expectedCode: CodeTimeout,
		},
		{
			name:         "rate limit error",
			err:          NewRateLimitError(100, 60),
			expectedCode: CodeRateLimit,
		},
		{
			name:         "service error",
			err:          NewServiceError("rqlite", "unavailable", 503, nil),
			expectedCode: CodeServiceUnavailable,
		},
		{
			name:         "internal error",
			err:          NewInternalError("internal", nil),
			expectedCode: CodeInternal,
		},
		{
			name:         "sentinel ErrNotFound",
			err:          ErrNotFound,
			expectedCode: CodeNotFound,
		},
		{
			name:         "standard error",
			err:          errors.New("generic error"),
			expectedCode: CodeInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := GetErrorCode(tt.err)
			if code != tt.expectedCode {
				t.Errorf("Expected code %s, got %s", tt.expectedCode, code)
			}
		})
	}
}

func TestGetErrorMessage(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		expectedMessage string
	}{
		{
			name:            "nil error",
			err:             nil,
			expectedMessage: "",
		},
		{
			name:            "validation error",
			err:             NewValidationError("field", "invalid format", nil),
			expectedMessage: "invalid format",
		},
		{
			name:            "not found error",
			err:             NewNotFoundError("user", "123"),
			expectedMessage: "user not found",
		},
		{
			name:            "standard error",
			err:             errors.New("generic error"),
			expectedMessage: "generic error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := GetErrorMessage(tt.err)
			if message != tt.expectedMessage {
				t.Errorf("Expected message %q, got %q", tt.expectedMessage, message)
			}
		})
	}
}

func TestCause(t *testing.T) {
	t.Run("unwrap error chain", func(t *testing.T) {
		root := errors.New("root cause")
		level1 := Wrap(root, "level 1")
		level2 := Wrap(level1, "level 2")
		level3 := Wrap(level2, "level 3")

		cause := Cause(level3)
		if cause != root {
			t.Errorf("Expected to find root cause, got %v", cause)
		}
	})

	t.Run("error without cause", func(t *testing.T) {
		err := errors.New("standalone error")
		cause := Cause(err)
		if cause != err {
			t.Errorf("Expected to return same error, got %v", cause)
		}
	})

	t.Run("custom error with cause", func(t *testing.T) {
		root := errors.New("database error")
		wrapped := NewInternalError("failed to save", root)

		cause := Cause(wrapped)
		if cause != root {
			t.Errorf("Expected to find root cause, got %v", cause)
		}
	})
}

func BenchmarkIsNotFound(b *testing.B) {
	err := NewNotFoundError("user", "123")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = IsNotFound(err)
	}
}

func BenchmarkShouldRetry(b *testing.B) {
	err := NewTimeoutError("operation", "30s")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ShouldRetry(err)
	}
}

func BenchmarkGetErrorCode(b *testing.B) {
	err := NewValidationError("field", "invalid", nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = GetErrorCode(err)
	}
}

func BenchmarkCause(b *testing.B) {
	root := errors.New("root")
	wrapped := Wrap(Wrap(Wrap(root, "l1"), "l2"), "l3")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Cause(wrapped)
	}
}

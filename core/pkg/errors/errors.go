package errors

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// Common sentinel errors for quick checks
var (
	// ErrNotFound is returned when a resource is not found.
	ErrNotFound = errors.New("not found")

	// ErrUnauthorized is returned when authentication fails or is missing.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden is returned when the user lacks permission for an action.
	ErrForbidden = errors.New("forbidden")

	// ErrConflict is returned when a resource already exists.
	ErrConflict = errors.New("resource already exists")

	// ErrInvalidInput is returned when request input is invalid.
	ErrInvalidInput = errors.New("invalid input")

	// ErrTimeout is returned when an operation times out.
	ErrTimeout = errors.New("operation timeout")

	// ErrServiceUnavailable is returned when a required service is unavailable.
	ErrServiceUnavailable = errors.New("service unavailable")

	// ErrInternal is returned when an internal error occurs.
	ErrInternal = errors.New("internal error")

	// ErrTooManyRequests is returned when rate limit is exceeded.
	ErrTooManyRequests = errors.New("too many requests")
)

// Error is the base interface for all custom errors in the system.
// It extends the standard error interface with additional context.
type Error interface {
	error
	// Code returns the error code
	Code() string
	// Message returns the human-readable error message
	Message() string
	// Unwrap returns the underlying cause
	Unwrap() error
}

// BaseError provides a foundation for all typed errors.
type BaseError struct {
	code    string
	message string
	cause   error
	stack   []uintptr
}

// Error implements the error interface.
func (e *BaseError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	}
	return e.message
}

// Code returns the error code.
func (e *BaseError) Code() string {
	return e.code
}

// Message returns the error message.
func (e *BaseError) Message() string {
	return e.message
}

// Unwrap returns the underlying cause.
func (e *BaseError) Unwrap() error {
	return e.cause
}

// Stack returns the captured stack trace.
func (e *BaseError) Stack() []uintptr {
	return e.stack
}

// captureStack captures the current stack trace.
func captureStack(skip int) []uintptr {
	const maxDepth = 32
	stack := make([]uintptr, maxDepth)
	n := runtime.Callers(skip+2, stack)
	return stack[:n]
}

// StackTrace returns a formatted stack trace string.
func (e *BaseError) StackTrace() string {
	if len(e.stack) == 0 {
		return ""
	}

	var buf strings.Builder
	frames := runtime.CallersFrames(e.stack)
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.File, "runtime/") {
			fmt.Fprintf(&buf, "%s\n\t%s:%d\n", frame.Function, frame.File, frame.Line)
		}
		if !more {
			break
		}
	}
	return buf.String()
}

// ValidationError represents an input validation error.
type ValidationError struct {
	*BaseError
	Field string
	Value interface{}
}

// NewValidationError creates a new validation error.
func NewValidationError(field, message string, value interface{}) *ValidationError {
	return &ValidationError{
		BaseError: &BaseError{
			code:    CodeValidation,
			message: message,
			stack:   captureStack(1),
		},
		Field: field,
		Value: value,
	}
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("validation error: %s: %s", e.Field, e.message)
	}
	return fmt.Sprintf("validation error: %s", e.message)
}

// NotFoundError represents a resource not found error.
type NotFoundError struct {
	*BaseError
	Resource string
	ID       string
}

// NewNotFoundError creates a new not found error.
func NewNotFoundError(resource, id string) *NotFoundError {
	return &NotFoundError{
		BaseError: &BaseError{
			code:    CodeNotFound,
			message: fmt.Sprintf("%s not found", resource),
			stack:   captureStack(1),
		},
		Resource: resource,
		ID:       id,
	}
}

// Error implements the error interface.
func (e *NotFoundError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("%s with ID '%s' not found", e.Resource, e.ID)
	}
	return fmt.Sprintf("%s not found", e.Resource)
}

// UnauthorizedError represents an authentication error.
type UnauthorizedError struct {
	*BaseError
	Realm string
}

// NewUnauthorizedError creates a new unauthorized error.
func NewUnauthorizedError(message string) *UnauthorizedError {
	if message == "" {
		message = "authentication required"
	}
	return &UnauthorizedError{
		BaseError: &BaseError{
			code:    CodeUnauthorized,
			message: message,
			stack:   captureStack(1),
		},
	}
}

// WithRealm sets the authentication realm.
func (e *UnauthorizedError) WithRealm(realm string) *UnauthorizedError {
	e.Realm = realm
	return e
}

// ForbiddenError represents an authorization error.
type ForbiddenError struct {
	*BaseError
	Resource string
	Action   string
}

// NewForbiddenError creates a new forbidden error.
func NewForbiddenError(resource, action string) *ForbiddenError {
	message := "forbidden"
	if resource != "" && action != "" {
		message = fmt.Sprintf("forbidden: cannot %s %s", action, resource)
	}
	return &ForbiddenError{
		BaseError: &BaseError{
			code:    CodeForbidden,
			message: message,
			stack:   captureStack(1),
		},
		Resource: resource,
		Action:   action,
	}
}

// ConflictError represents a resource conflict error.
type ConflictError struct {
	*BaseError
	Resource string
	Field    string
	Value    string
}

// NewConflictError creates a new conflict error.
func NewConflictError(resource, field, value string) *ConflictError {
	message := fmt.Sprintf("%s already exists", resource)
	if field != "" {
		message = fmt.Sprintf("%s with %s='%s' already exists", resource, field, value)
	}
	return &ConflictError{
		BaseError: &BaseError{
			code:    CodeConflict,
			message: message,
			stack:   captureStack(1),
		},
		Resource: resource,
		Field:    field,
		Value:    value,
	}
}

// InternalError represents an internal server error.
type InternalError struct {
	*BaseError
	Operation string
}

// NewInternalError creates a new internal error.
func NewInternalError(message string, cause error) *InternalError {
	if message == "" {
		message = "internal error"
	}
	return &InternalError{
		BaseError: &BaseError{
			code:    CodeInternal,
			message: message,
			cause:   cause,
			stack:   captureStack(1),
		},
	}
}

// WithOperation sets the operation context.
func (e *InternalError) WithOperation(op string) *InternalError {
	e.Operation = op
	return e
}

// ServiceError represents a downstream service error.
type ServiceError struct {
	*BaseError
	Service    string
	StatusCode int
}

// NewServiceError creates a new service error.
func NewServiceError(service, message string, statusCode int, cause error) *ServiceError {
	if message == "" {
		message = fmt.Sprintf("%s service error", service)
	}
	return &ServiceError{
		BaseError: &BaseError{
			code:    CodeServiceUnavailable,
			message: message,
			cause:   cause,
			stack:   captureStack(1),
		},
		Service:    service,
		StatusCode: statusCode,
	}
}

// TimeoutError represents a timeout error.
type TimeoutError struct {
	*BaseError
	Operation string
	Duration  string
}

// NewTimeoutError creates a new timeout error.
func NewTimeoutError(operation, duration string) *TimeoutError {
	message := "operation timeout"
	if operation != "" {
		message = fmt.Sprintf("%s timeout", operation)
	}
	return &TimeoutError{
		BaseError: &BaseError{
			code:    CodeTimeout,
			message: message,
			stack:   captureStack(1),
		},
		Operation: operation,
		Duration:  duration,
	}
}

// RateLimitError represents a rate limiting error.
type RateLimitError struct {
	*BaseError
	Limit      int
	RetryAfter int // seconds
}

// NewRateLimitError creates a new rate limit error.
func NewRateLimitError(limit, retryAfter int) *RateLimitError {
	return &RateLimitError{
		BaseError: &BaseError{
			code:    CodeRateLimit,
			message: "rate limit exceeded",
			stack:   captureStack(1),
		},
		Limit:      limit,
		RetryAfter: retryAfter,
	}
}

// Wrap wraps an error with additional context.
// If the error is already one of our custom types, it preserves the type
// and adds the cause chain. Otherwise, it creates an InternalError.
func Wrap(err error, message string) error {
	if err == nil {
		return nil
	}

	// If it's already our error type, wrap it
	if e, ok := err.(Error); ok {
		return &BaseError{
			code:    e.Code(),
			message: message,
			cause:   err,
			stack:   captureStack(1),
		}
	}

	// Otherwise create an internal error
	return &InternalError{
		BaseError: &BaseError{
			code:    CodeInternal,
			message: message,
			cause:   err,
			stack:   captureStack(1),
		},
	}
}

// Wrapf wraps an error with a formatted message.
func Wrapf(err error, format string, args ...interface{}) error {
	return Wrap(err, fmt.Sprintf(format, args...))
}

// New creates a new error with a message.
func New(message string) error {
	return &BaseError{
		code:    CodeInternal,
		message: message,
		stack:   captureStack(1),
	}
}

// Newf creates a new error with a formatted message.
func Newf(format string, args ...interface{}) error {
	return New(fmt.Sprintf(format, args...))
}

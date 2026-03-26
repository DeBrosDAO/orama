package errors

import "errors"

// IsNotFound checks if an error indicates a resource was not found.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}

	var notFoundErr *NotFoundError
	return errors.As(err, &notFoundErr) || errors.Is(err, ErrNotFound)
}

// IsValidation checks if an error is a validation error.
func IsValidation(err error) bool {
	if err == nil {
		return false
	}

	var validationErr *ValidationError
	return errors.As(err, &validationErr)
}

// IsUnauthorized checks if an error indicates lack of authentication.
func IsUnauthorized(err error) bool {
	if err == nil {
		return false
	}

	var unauthorizedErr *UnauthorizedError
	return errors.As(err, &unauthorizedErr) || errors.Is(err, ErrUnauthorized)
}

// IsForbidden checks if an error indicates lack of authorization.
func IsForbidden(err error) bool {
	if err == nil {
		return false
	}

	var forbiddenErr *ForbiddenError
	return errors.As(err, &forbiddenErr) || errors.Is(err, ErrForbidden)
}

// IsConflict checks if an error indicates a resource conflict.
func IsConflict(err error) bool {
	if err == nil {
		return false
	}

	var conflictErr *ConflictError
	return errors.As(err, &conflictErr) || errors.Is(err, ErrConflict)
}

// IsTimeout checks if an error indicates a timeout.
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}

	var timeoutErr *TimeoutError
	return errors.As(err, &timeoutErr) || errors.Is(err, ErrTimeout)
}

// IsRateLimit checks if an error indicates rate limiting.
func IsRateLimit(err error) bool {
	if err == nil {
		return false
	}

	var rateLimitErr *RateLimitError
	return errors.As(err, &rateLimitErr) || errors.Is(err, ErrTooManyRequests)
}

// IsServiceUnavailable checks if an error indicates a service is unavailable.
func IsServiceUnavailable(err error) bool {
	if err == nil {
		return false
	}

	var serviceErr *ServiceError
	return errors.As(err, &serviceErr) || errors.Is(err, ErrServiceUnavailable)
}

// IsInternal checks if an error is an internal error.
func IsInternal(err error) bool {
	if err == nil {
		return false
	}

	var internalErr *InternalError
	return errors.As(err, &internalErr) || errors.Is(err, ErrInternal)
}

// ShouldRetry checks if an operation should be retried based on the error.
func ShouldRetry(err error) bool {
	if err == nil {
		return false
	}

	// Check if it's a retryable error type
	if IsTimeout(err) || IsServiceUnavailable(err) {
		return true
	}

	// Check the error code
	var customErr Error
	if errors.As(err, &customErr) {
		return IsRetryable(customErr.Code())
	}

	return false
}

// GetErrorCode extracts the error code from an error.
func GetErrorCode(err error) string {
	if err == nil {
		return CodeOK
	}

	var customErr Error
	if errors.As(err, &customErr) {
		return customErr.Code()
	}

	// Try to infer from sentinel errors
	switch {
	case IsNotFound(err):
		return CodeNotFound
	case IsUnauthorized(err):
		return CodeUnauthorized
	case IsForbidden(err):
		return CodeForbidden
	case IsConflict(err):
		return CodeConflict
	case IsTimeout(err):
		return CodeTimeout
	case IsRateLimit(err):
		return CodeRateLimit
	case IsServiceUnavailable(err):
		return CodeServiceUnavailable
	default:
		return CodeInternal
	}
}

// GetErrorMessage extracts a human-readable message from an error.
func GetErrorMessage(err error) string {
	if err == nil {
		return ""
	}

	var customErr Error
	if errors.As(err, &customErr) {
		return customErr.Message()
	}

	return err.Error()
}

// Cause returns the underlying cause of an error.
// It unwraps the error chain until it finds the root cause.
func Cause(err error) error {
	for {
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		underlying := unwrapper.Unwrap()
		if underlying == nil {
			return err
		}
		err = underlying
	}
}

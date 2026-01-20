package errors

import (
	"encoding/json"
	"errors"
	"net/http"
)

// HTTPError represents an HTTP error response.
type HTTPError struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
	TraceID string            `json:"trace_id,omitempty"`
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return e.Message
}

// StatusCode returns the HTTP status code for an error.
// It maps error codes to appropriate HTTP status codes.
func StatusCode(err error) int {
	if err == nil {
		return http.StatusOK
	}

	// Check if it's our custom error type
	var customErr Error
	if errors.As(err, &customErr) {
		return codeToHTTPStatus(customErr.Code())
	}

	// Check for specific error types
	var (
		validationErr  *ValidationError
		notFoundErr    *NotFoundError
		unauthorizedErr *UnauthorizedError
		forbiddenErr   *ForbiddenError
		conflictErr    *ConflictError
		timeoutErr     *TimeoutError
		rateLimitErr   *RateLimitError
		serviceErr     *ServiceError
	)

	switch {
	case errors.As(err, &validationErr):
		return http.StatusBadRequest
	case errors.As(err, &notFoundErr):
		return http.StatusNotFound
	case errors.As(err, &unauthorizedErr):
		return http.StatusUnauthorized
	case errors.As(err, &forbiddenErr):
		return http.StatusForbidden
	case errors.As(err, &conflictErr):
		return http.StatusConflict
	case errors.As(err, &timeoutErr):
		return http.StatusRequestTimeout
	case errors.As(err, &rateLimitErr):
		return http.StatusTooManyRequests
	case errors.As(err, &serviceErr):
		return http.StatusServiceUnavailable
	}

	// Check sentinel errors
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	case errors.Is(err, ErrInvalidInput):
		return http.StatusBadRequest
	case errors.Is(err, ErrTimeout):
		return http.StatusRequestTimeout
	case errors.Is(err, ErrServiceUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrTooManyRequests):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrInternal):
		return http.StatusInternalServerError
	}

	// Default to internal server error
	return http.StatusInternalServerError
}

// codeToHTTPStatus maps error codes to HTTP status codes.
func codeToHTTPStatus(code string) int {
	switch code {
	case CodeOK:
		return http.StatusOK
	case CodeCancelled:
		return 499 // Client Closed Request
	case CodeUnknown, CodeInternal:
		return http.StatusInternalServerError
	case CodeInvalidArgument, CodeValidation, CodeFailedPrecondition:
		return http.StatusBadRequest
	case CodeDeadlineExceeded, CodeTimeout:
		return http.StatusRequestTimeout
	case CodeNotFound:
		return http.StatusNotFound
	case CodeAlreadyExists, CodeConflict:
		return http.StatusConflict
	case CodePermissionDenied, CodeForbidden:
		return http.StatusForbidden
	case CodeResourceExhausted, CodeRateLimit:
		return http.StatusTooManyRequests
	case CodeAborted:
		return http.StatusConflict
	case CodeOutOfRange:
		return http.StatusBadRequest
	case CodeUnimplemented:
		return http.StatusNotImplemented
	case CodeUnavailable, CodeServiceUnavailable:
		return http.StatusServiceUnavailable
	case CodeDataLoss, CodeDatabaseError, CodeStorageError:
		return http.StatusInternalServerError
	case CodeUnauthenticated, CodeUnauthorized, CodeAuthError:
		return http.StatusUnauthorized
	case CodeCacheError, CodeNetworkError, CodeExecutionError,
		CodeCompilationError, CodeConfigError, CodeCryptoError,
		CodeSerializationError:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// ToHTTPError converts an error to an HTTPError.
func ToHTTPError(err error, traceID string) *HTTPError {
	if err == nil {
		return &HTTPError{
			Status:  http.StatusOK,
			Code:    CodeOK,
			Message: "success",
			TraceID: traceID,
		}
	}

	httpErr := &HTTPError{
		Status:  StatusCode(err),
		TraceID: traceID,
		Details: make(map[string]string),
	}

	// Extract details from custom error types
	var customErr Error
	if errors.As(err, &customErr) {
		httpErr.Code = customErr.Code()
		httpErr.Message = customErr.Message()
	} else {
		httpErr.Code = CodeInternal
		httpErr.Message = err.Error()
	}

	// Add type-specific details
	var (
		validationErr  *ValidationError
		notFoundErr    *NotFoundError
		unauthorizedErr *UnauthorizedError
		forbiddenErr   *ForbiddenError
		conflictErr    *ConflictError
		timeoutErr     *TimeoutError
		rateLimitErr   *RateLimitError
		serviceErr     *ServiceError
		internalErr    *InternalError
	)

	switch {
	case errors.As(err, &validationErr):
		if validationErr.Field != "" {
			httpErr.Details["field"] = validationErr.Field
		}
	case errors.As(err, &notFoundErr):
		if notFoundErr.Resource != "" {
			httpErr.Details["resource"] = notFoundErr.Resource
		}
		if notFoundErr.ID != "" {
			httpErr.Details["id"] = notFoundErr.ID
		}
	case errors.As(err, &unauthorizedErr):
		if unauthorizedErr.Realm != "" {
			httpErr.Details["realm"] = unauthorizedErr.Realm
		}
	case errors.As(err, &forbiddenErr):
		if forbiddenErr.Resource != "" {
			httpErr.Details["resource"] = forbiddenErr.Resource
		}
		if forbiddenErr.Action != "" {
			httpErr.Details["action"] = forbiddenErr.Action
		}
	case errors.As(err, &conflictErr):
		if conflictErr.Resource != "" {
			httpErr.Details["resource"] = conflictErr.Resource
		}
		if conflictErr.Field != "" {
			httpErr.Details["field"] = conflictErr.Field
		}
	case errors.As(err, &timeoutErr):
		if timeoutErr.Operation != "" {
			httpErr.Details["operation"] = timeoutErr.Operation
		}
		if timeoutErr.Duration != "" {
			httpErr.Details["duration"] = timeoutErr.Duration
		}
	case errors.As(err, &rateLimitErr):
		if rateLimitErr.RetryAfter > 0 {
			httpErr.Details["retry_after"] = string(rune(rateLimitErr.RetryAfter))
		}
	case errors.As(err, &serviceErr):
		if serviceErr.Service != "" {
			httpErr.Details["service"] = serviceErr.Service
		}
	case errors.As(err, &internalErr):
		if internalErr.Operation != "" {
			httpErr.Details["operation"] = internalErr.Operation
		}
	}

	return httpErr
}

// WriteHTTPError writes an error response to an http.ResponseWriter.
func WriteHTTPError(w http.ResponseWriter, err error, traceID string) {
	httpErr := ToHTTPError(err, traceID)
	w.Header().Set("Content-Type", "application/json")

	// Add retry-after header for rate limit errors
	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) && rateLimitErr.RetryAfter > 0 {
		w.Header().Set("Retry-After", string(rune(rateLimitErr.RetryAfter)))
	}

	// Add WWW-Authenticate header for unauthorized errors
	var unauthorizedErr *UnauthorizedError
	if errors.As(err, &unauthorizedErr) && unauthorizedErr.Realm != "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+unauthorizedErr.Realm+`"`)
	}

	w.WriteHeader(httpErr.Status)
	json.NewEncoder(w).Encode(httpErr)
}

// HTTPStatusToCode converts an HTTP status code to an error code.
func HTTPStatusToCode(status int) string {
	switch status {
	case http.StatusOK:
		return CodeOK
	case http.StatusBadRequest:
		return CodeInvalidArgument
	case http.StatusUnauthorized:
		return CodeUnauthenticated
	case http.StatusForbidden:
		return CodePermissionDenied
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeAlreadyExists
	case http.StatusRequestTimeout:
		return CodeDeadlineExceeded
	case http.StatusTooManyRequests:
		return CodeResourceExhausted
	case http.StatusNotImplemented:
		return CodeUnimplemented
	case http.StatusServiceUnavailable:
		return CodeUnavailable
	case http.StatusInternalServerError:
		return CodeInternal
	default:
		if status >= 400 && status < 500 {
			return CodeInvalidArgument
		}
		return CodeInternal
	}
}

package errors

// Error codes for categorizing errors.
// These codes map to HTTP status codes and gRPC codes where applicable.
const (
	// CodeOK indicates success (not an error).
	CodeOK = "OK"

	// CodeCancelled indicates the operation was cancelled.
	CodeCancelled = "CANCELLED"

	// CodeUnknown indicates an unknown error occurred.
	CodeUnknown = "UNKNOWN"

	// CodeInvalidArgument indicates client specified an invalid argument.
	CodeInvalidArgument = "INVALID_ARGUMENT"

	// CodeDeadlineExceeded indicates operation deadline was exceeded.
	CodeDeadlineExceeded = "DEADLINE_EXCEEDED"

	// CodeNotFound indicates a resource was not found.
	CodeNotFound = "NOT_FOUND"

	// CodeAlreadyExists indicates attempting to create a resource that already exists.
	CodeAlreadyExists = "ALREADY_EXISTS"

	// CodePermissionDenied indicates the caller doesn't have permission.
	CodePermissionDenied = "PERMISSION_DENIED"

	// CodeResourceExhausted indicates a resource has been exhausted.
	CodeResourceExhausted = "RESOURCE_EXHAUSTED"

	// CodeFailedPrecondition indicates operation was rejected because the system
	// is not in a required state.
	CodeFailedPrecondition = "FAILED_PRECONDITION"

	// CodeAborted indicates the operation was aborted.
	CodeAborted = "ABORTED"

	// CodeOutOfRange indicates operation attempted past valid range.
	CodeOutOfRange = "OUT_OF_RANGE"

	// CodeUnimplemented indicates operation is not implemented or not supported.
	CodeUnimplemented = "UNIMPLEMENTED"

	// CodeInternal indicates internal errors.
	CodeInternal = "INTERNAL"

	// CodeUnavailable indicates the service is currently unavailable.
	CodeUnavailable = "UNAVAILABLE"

	// CodeDataLoss indicates unrecoverable data loss or corruption.
	CodeDataLoss = "DATA_LOSS"

	// CodeUnauthenticated indicates the request does not have valid authentication.
	CodeUnauthenticated = "UNAUTHENTICATED"

	// Domain-specific error codes

	// CodeValidation indicates input validation failed.
	CodeValidation = "VALIDATION_ERROR"

	// CodeUnauthorized indicates authentication is required or failed.
	CodeUnauthorized = "UNAUTHORIZED"

	// CodeForbidden indicates the authenticated user lacks permission.
	CodeForbidden = "FORBIDDEN"

	// CodeConflict indicates a resource conflict (e.g., duplicate key).
	CodeConflict = "CONFLICT"

	// CodeTimeout indicates an operation timed out.
	CodeTimeout = "TIMEOUT"

	// CodeRateLimit indicates rate limit was exceeded.
	CodeRateLimit = "RATE_LIMIT_EXCEEDED"

	// CodeServiceUnavailable indicates a downstream service is unavailable.
	CodeServiceUnavailable = "SERVICE_UNAVAILABLE"

	// CodeDatabaseError indicates a database operation failed.
	CodeDatabaseError = "DATABASE_ERROR"

	// CodeCacheError indicates a cache operation failed.
	CodeCacheError = "CACHE_ERROR"

	// CodeStorageError indicates a storage operation failed.
	CodeStorageError = "STORAGE_ERROR"

	// CodeNetworkError indicates a network operation failed.
	CodeNetworkError = "NETWORK_ERROR"

	// CodeExecutionError indicates a WASM or function execution failed.
	CodeExecutionError = "EXECUTION_ERROR"

	// CodeCompilationError indicates WASM compilation failed.
	CodeCompilationError = "COMPILATION_ERROR"

	// CodeConfigError indicates a configuration error.
	CodeConfigError = "CONFIG_ERROR"

	// CodeAuthError indicates an authentication/authorization error.
	CodeAuthError = "AUTH_ERROR"

	// CodeCryptoError indicates a cryptographic operation failed.
	CodeCryptoError = "CRYPTO_ERROR"

	// CodeSerializationError indicates serialization/deserialization failed.
	CodeSerializationError = "SERIALIZATION_ERROR"
)

// ErrorCategory represents a high-level error category.
type ErrorCategory string

const (
	// CategoryClient indicates a client-side error (4xx).
	CategoryClient ErrorCategory = "CLIENT_ERROR"

	// CategoryServer indicates a server-side error (5xx).
	CategoryServer ErrorCategory = "SERVER_ERROR"

	// CategoryNetwork indicates a network-related error.
	CategoryNetwork ErrorCategory = "NETWORK_ERROR"

	// CategoryTimeout indicates a timeout error.
	CategoryTimeout ErrorCategory = "TIMEOUT_ERROR"

	// CategoryValidation indicates a validation error.
	CategoryValidation ErrorCategory = "VALIDATION_ERROR"

	// CategoryAuth indicates an authentication/authorization error.
	CategoryAuth ErrorCategory = "AUTH_ERROR"
)

// GetCategory returns the category for an error code.
func GetCategory(code string) ErrorCategory {
	switch code {
	case CodeInvalidArgument, CodeValidation, CodeNotFound,
		CodeConflict, CodeAlreadyExists, CodeOutOfRange:
		return CategoryClient

	case CodeUnauthorized, CodeUnauthenticated,
		CodeForbidden, CodePermissionDenied, CodeAuthError:
		return CategoryAuth

	case CodeTimeout, CodeDeadlineExceeded:
		return CategoryTimeout

	case CodeNetworkError, CodeServiceUnavailable, CodeUnavailable:
		return CategoryNetwork

	default:
		return CategoryServer
	}
}

// IsRetryable returns true if an error with the given code should be retried.
func IsRetryable(code string) bool {
	switch code {
	case CodeTimeout, CodeDeadlineExceeded,
		CodeServiceUnavailable, CodeUnavailable,
		CodeResourceExhausted, CodeAborted,
		CodeNetworkError, CodeDatabaseError,
		CodeCacheError, CodeStorageError:
		return true
	default:
		return false
	}
}

// IsClientError returns true if the error is a client error (4xx).
func IsClientError(code string) bool {
	return GetCategory(code) == CategoryClient
}

// IsServerError returns true if the error is a server error (5xx).
func IsServerError(code string) bool {
	return GetCategory(code) == CategoryServer
}

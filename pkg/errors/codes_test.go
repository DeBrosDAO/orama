package errors

import "testing"

func TestGetCategory(t *testing.T) {
	tests := []struct {
		code             string
		expectedCategory ErrorCategory
	}{
		// Client errors
		{CodeInvalidArgument, CategoryClient},
		{CodeValidation, CategoryClient},
		{CodeNotFound, CategoryClient},
		{CodeConflict, CategoryClient},
		{CodeAlreadyExists, CategoryClient},
		{CodeOutOfRange, CategoryClient},

		// Auth errors
		{CodeUnauthorized, CategoryAuth},
		{CodeUnauthenticated, CategoryAuth},
		{CodeForbidden, CategoryAuth},
		{CodePermissionDenied, CategoryAuth},
		{CodeAuthError, CategoryAuth},

		// Timeout errors
		{CodeTimeout, CategoryTimeout},
		{CodeDeadlineExceeded, CategoryTimeout},

		// Network errors
		{CodeNetworkError, CategoryNetwork},
		{CodeServiceUnavailable, CategoryNetwork},
		{CodeUnavailable, CategoryNetwork},

		// Server errors
		{CodeInternal, CategoryServer},
		{CodeUnknown, CategoryServer},
		{CodeDatabaseError, CategoryServer},
		{CodeCacheError, CategoryServer},
		{CodeStorageError, CategoryServer},
		{CodeExecutionError, CategoryServer},
		{CodeCompilationError, CategoryServer},
		{CodeConfigError, CategoryServer},
		{CodeCryptoError, CategoryServer},
		{CodeSerializationError, CategoryServer},
		{CodeDataLoss, CategoryServer},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			category := GetCategory(tt.code)
			if category != tt.expectedCategory {
				t.Errorf("Code %s: expected category %s, got %s", tt.code, tt.expectedCategory, category)
			}
		})
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		code     string
		expected bool
	}{
		// Retryable errors
		{CodeTimeout, true},
		{CodeDeadlineExceeded, true},
		{CodeServiceUnavailable, true},
		{CodeUnavailable, true},
		{CodeResourceExhausted, true},
		{CodeAborted, true},
		{CodeNetworkError, true},
		{CodeDatabaseError, true},
		{CodeCacheError, true},
		{CodeStorageError, true},

		// Non-retryable errors
		{CodeInvalidArgument, false},
		{CodeValidation, false},
		{CodeNotFound, false},
		{CodeUnauthorized, false},
		{CodeForbidden, false},
		{CodeConflict, false},
		{CodeInternal, false},
		{CodeAuthError, false},
		{CodeExecutionError, false},
		{CodeCompilationError, false},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			result := IsRetryable(tt.code)
			if result != tt.expected {
				t.Errorf("Code %s: expected retryable=%v, got %v", tt.code, tt.expected, result)
			}
		})
	}
}

func TestIsClientError(t *testing.T) {
	tests := []struct {
		code     string
		expected bool
	}{
		{CodeInvalidArgument, true},
		{CodeValidation, true},
		{CodeNotFound, true},
		{CodeConflict, true},
		{CodeInternal, false},
		{CodeUnauthorized, false}, // Auth category, not client
		{CodeTimeout, false},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			result := IsClientError(tt.code)
			if result != tt.expected {
				t.Errorf("Code %s: expected client error=%v, got %v", tt.code, tt.expected, result)
			}
		})
	}
}

func TestIsServerError(t *testing.T) {
	tests := []struct {
		code     string
		expected bool
	}{
		{CodeInternal, true},
		{CodeUnknown, true},
		{CodeDatabaseError, true},
		{CodeCacheError, true},
		{CodeStorageError, true},
		{CodeExecutionError, true},
		{CodeInvalidArgument, false},
		{CodeNotFound, false},
		{CodeUnauthorized, false},
		{CodeTimeout, false},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			result := IsServerError(tt.code)
			if result != tt.expected {
				t.Errorf("Code %s: expected server error=%v, got %v", tt.code, tt.expected, result)
			}
		})
	}
}

func TestErrorCategoryConsistency(t *testing.T) {
	// Test that IsClientError and IsServerError are mutually exclusive
	allCodes := []string{
		CodeOK, CodeCancelled, CodeUnknown, CodeInvalidArgument,
		CodeDeadlineExceeded, CodeNotFound, CodeAlreadyExists,
		CodePermissionDenied, CodeResourceExhausted, CodeFailedPrecondition,
		CodeAborted, CodeOutOfRange, CodeUnimplemented, CodeInternal,
		CodeUnavailable, CodeDataLoss, CodeUnauthenticated,
		CodeValidation, CodeUnauthorized, CodeForbidden, CodeConflict,
		CodeTimeout, CodeRateLimit, CodeServiceUnavailable,
		CodeDatabaseError, CodeCacheError, CodeStorageError,
		CodeNetworkError, CodeExecutionError, CodeCompilationError,
		CodeConfigError, CodeAuthError, CodeCryptoError,
		CodeSerializationError,
	}

	for _, code := range allCodes {
		t.Run(code, func(t *testing.T) {
			isClient := IsClientError(code)
			isServer := IsServerError(code)

			// They shouldn't both be true
			if isClient && isServer {
				t.Errorf("Code %s is both client and server error", code)
			}

			// Get category to ensure it's one of the valid ones
			category := GetCategory(code)
			validCategories := []ErrorCategory{
				CategoryClient, CategoryServer, CategoryNetwork,
				CategoryTimeout, CategoryValidation, CategoryAuth,
			}

			found := false
			for _, valid := range validCategories {
				if category == valid {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Code %s has invalid category: %s", code, category)
			}
		})
	}
}

func BenchmarkGetCategory(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = GetCategory(CodeValidation)
	}
}

func BenchmarkIsRetryable(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = IsRetryable(CodeTimeout)
	}
}

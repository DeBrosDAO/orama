package errors_test

import (
	"fmt"
	"net/http/httptest"

	"github.com/DeBrosOfficial/network/pkg/errors"
)

// Example demonstrates creating and using validation errors.
func ExampleNewValidationError() {
	err := errors.NewValidationError("email", "invalid email format", "not-an-email")
	fmt.Println(err.Error())
	fmt.Println("Code:", err.Code())
	// Output:
	// validation error: email: invalid email format
	// Code: VALIDATION_ERROR
}

// Example demonstrates creating and using not found errors.
func ExampleNewNotFoundError() {
	err := errors.NewNotFoundError("user", "123")
	fmt.Println(err.Error())
	fmt.Println("HTTP Status:", errors.StatusCode(err))
	// Output:
	// user with ID '123' not found
	// HTTP Status: 404
}

// Example demonstrates wrapping errors with context.
func ExampleWrap() {
	originalErr := errors.NewNotFoundError("user", "123")
	wrappedErr := errors.Wrap(originalErr, "failed to fetch user profile")

	fmt.Println(wrappedErr.Error())
	fmt.Println("Is NotFound:", errors.IsNotFound(wrappedErr))
	// Output:
	// failed to fetch user profile: user with ID '123' not found
	// Is NotFound: true
}

// Example demonstrates checking error types.
func ExampleIsNotFound() {
	err := errors.NewNotFoundError("user", "123")

	if errors.IsNotFound(err) {
		fmt.Println("User not found")
	}
	// Output:
	// User not found
}

// Example demonstrates checking if an error should be retried.
func ExampleShouldRetry() {
	timeoutErr := errors.NewTimeoutError("database query", "5s")
	notFoundErr := errors.NewNotFoundError("user", "123")

	fmt.Println("Timeout should retry:", errors.ShouldRetry(timeoutErr))
	fmt.Println("Not found should retry:", errors.ShouldRetry(notFoundErr))
	// Output:
	// Timeout should retry: true
	// Not found should retry: false
}

// Example demonstrates converting errors to HTTP responses.
func ExampleToHTTPError() {
	err := errors.NewNotFoundError("user", "123")
	httpErr := errors.ToHTTPError(err, "trace-abc-123")

	fmt.Println("Status:", httpErr.Status)
	fmt.Println("Code:", httpErr.Code)
	fmt.Println("Message:", httpErr.Message)
	fmt.Println("Resource:", httpErr.Details["resource"])
	// Output:
	// Status: 404
	// Code: NOT_FOUND
	// Message: user not found
	// Resource: user
}

// Example demonstrates writing HTTP error responses.
func ExampleWriteHTTPError() {
	err := errors.NewValidationError("email", "invalid format", "bad-email")

	// Create a test response recorder
	w := httptest.NewRecorder()

	// Write the error response
	errors.WriteHTTPError(w, err, "trace-xyz")

	fmt.Println("Status Code:", w.Code)
	fmt.Println("Content-Type:", w.Header().Get("Content-Type"))
	// Output:
	// Status Code: 400
	// Content-Type: application/json
}

// Example demonstrates using error categories.
func ExampleGetCategory() {
	code := errors.CodeNotFound
	category := errors.GetCategory(code)

	fmt.Println("Category:", category)
	fmt.Println("Is Client Error:", errors.IsClientError(code))
	fmt.Println("Is Server Error:", errors.IsServerError(code))
	// Output:
	// Category: CLIENT_ERROR
	// Is Client Error: true
	// Is Server Error: false
}

// Example demonstrates creating service errors.
func ExampleNewServiceError() {
	err := errors.NewServiceError("rqlite", "database unavailable", 503, nil)

	fmt.Println(err.Error())
	fmt.Println("Should Retry:", errors.ShouldRetry(err))
	// Output:
	// database unavailable
	// Should Retry: true
}

// Example demonstrates creating internal errors with context.
func ExampleNewInternalError() {
	dbErr := fmt.Errorf("connection refused")
	err := errors.NewInternalError("failed to save user", dbErr).WithOperation("saveUser")

	fmt.Println("Message:", err.Message())
	fmt.Println("Operation:", err.Operation)
	// Output:
	// Message: failed to save user
	// Operation: saveUser
}

// Example demonstrates HTTP status code mapping.
func ExampleStatusCode() {
	tests := []error{
		errors.NewValidationError("field", "invalid", nil),
		errors.NewNotFoundError("user", "123"),
		errors.NewUnauthorizedError("invalid token"),
		errors.NewForbiddenError("resource", "delete"),
		errors.NewTimeoutError("operation", "30s"),
	}

	for _, err := range tests {
		fmt.Printf("%s -> %d\n", errors.GetErrorCode(err), errors.StatusCode(err))
	}
	// Output:
	// VALIDATION_ERROR -> 400
	// NOT_FOUND -> 404
	// UNAUTHORIZED -> 401
	// FORBIDDEN -> 403
	// TIMEOUT -> 408
}

// Example demonstrates getting the root cause of an error chain.
func ExampleCause() {
	root := fmt.Errorf("database connection failed")
	level1 := errors.Wrap(root, "failed to fetch user")
	level2 := errors.Wrap(level1, "API request failed")

	cause := errors.Cause(level2)
	fmt.Println(cause.Error())
	// Output:
	// database connection failed
}

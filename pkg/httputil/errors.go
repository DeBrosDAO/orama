package httputil

import (
	"fmt"
	"net/http"
	"strings"
)

// HTTPError represents a structured HTTP error with a status code and message.
type HTTPError struct {
	Code    int
	Message string
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Code, e.Message)
}

// NewHTTPError creates a new HTTP error with the given code and message.
func NewHTTPError(code int, message string) *HTTPError {
	return &HTTPError{Code: code, Message: message}
}

// Common HTTP errors
var (
	ErrBadRequest          = NewHTTPError(http.StatusBadRequest, "bad request")
	ErrUnauthorized        = NewHTTPError(http.StatusUnauthorized, "unauthorized")
	ErrForbidden           = NewHTTPError(http.StatusForbidden, "forbidden")
	ErrNotFound            = NewHTTPError(http.StatusNotFound, "not found")
	ErrMethodNotAllowed    = NewHTTPError(http.StatusMethodNotAllowed, "method not allowed")
	ErrConflict            = NewHTTPError(http.StatusConflict, "conflict")
	ErrInternalServerError = NewHTTPError(http.StatusInternalServerError, "internal server error")
	ErrServiceUnavailable  = NewHTTPError(http.StatusServiceUnavailable, "service unavailable")
)

// WriteHTTPError writes an HTTPError to the response.
func WriteHTTPError(w http.ResponseWriter, err *HTTPError) {
	WriteError(w, err.Code, err.Message)
}

// CheckMethod validates that the request method matches the expected method.
// If it doesn't match, it writes a 405 Method Not Allowed error and returns false.
func CheckMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	return true
}

// CheckMethodOneOf validates that the request method is one of the allowed methods.
// If it doesn't match any, it writes a 405 Method Not Allowed error and returns false.
func CheckMethodOneOf(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, m := range methods {
		if r.Method == m {
			return true
		}
	}
	WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

// RequireNotEmpty checks if a string value is empty after trimming whitespace.
// If empty, it writes a 400 Bad Request error with the field name and returns false.
func RequireNotEmpty(w http.ResponseWriter, value, fieldName string) bool {
	if strings.TrimSpace(value) == "" {
		WriteError(w, http.StatusBadRequest, fmt.Sprintf("%s is required", fieldName))
		return false
	}
	return true
}

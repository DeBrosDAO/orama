package client

import (
	"errors"
	"fmt"
)

// Common client errors
var (
	// ErrNotConnected indicates the client is not connected to the network
	ErrNotConnected = errors.New("client not connected")

	// ErrAuthRequired indicates authentication is required for the operation
	ErrAuthRequired = errors.New("authentication required")

	// ErrNoHost indicates no LibP2P host is available
	ErrNoHost = errors.New("no host available")

	// ErrInvalidConfig indicates the client configuration is invalid
	ErrInvalidConfig = errors.New("invalid configuration")

	// ErrNamespaceMismatch indicates a namespace mismatch
	ErrNamespaceMismatch = errors.New("namespace mismatch")
)

// ClientError represents a client-specific error with additional context
type ClientError struct {
	Op      string // Operation that failed
	Message string // Error message
	Err     error  // Underlying error
}

func (e *ClientError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Message)
}

func (e *ClientError) Unwrap() error {
	return e.Err
}

// NewClientError creates a new ClientError
func NewClientError(op, message string, err error) *ClientError {
	return &ClientError{
		Op:      op,
		Message: message,
		Err:     err,
	}
}

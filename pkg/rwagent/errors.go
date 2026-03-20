package rwagent

import (
	"errors"
	"fmt"
)

// AgentError represents an error returned by the rootwallet agent API.
type AgentError struct {
	Code       string // e.g., "AGENT_LOCKED", "NOT_FOUND"
	Message    string
	StatusCode int
}

func (e *AgentError) Error() string {
	return fmt.Sprintf("rootwallet agent: %s (%s)", e.Message, e.Code)
}

// IsLocked returns true if the error indicates the agent is locked.
func IsLocked(err error) bool {
	var ae *AgentError
	if errors.As(err, &ae) {
		return ae.Code == "AGENT_LOCKED"
	}
	return false
}

// IsNotRunning returns true if the error indicates the agent is not reachable.
func IsNotRunning(err error) bool {
	var ae *AgentError
	if errors.As(err, &ae) {
		return ae.Code == "AGENT_NOT_RUNNING"
	}
	// Also check for connection errors
	return errors.Is(err, ErrAgentNotRunning)
}

// IsNotFound returns true if the vault entry was not found.
func IsNotFound(err error) bool {
	var ae *AgentError
	if errors.As(err, &ae) {
		return ae.Code == "NOT_FOUND"
	}
	return false
}

// IsApprovalDenied returns true if the user denied the app's access request.
func IsApprovalDenied(err error) bool {
	var ae *AgentError
	if errors.As(err, &ae) {
		return ae.Code == "APPROVAL_DENIED" || ae.Code == "PERMISSION_DENIED"
	}
	return false
}

// ErrAgentNotRunning is returned when the agent socket is not reachable.
var ErrAgentNotRunning = fmt.Errorf("rootwallet agent is not running — start with: rw agent start && rw agent unlock")

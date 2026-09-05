package rwagent

import (
	"errors"
	"fmt"
	"net/http"
)

// Error codes the RootWallet agent returns. They are the complete set emitted
// by err_json in the agent's request handlers; anything else is a new code the
// agent grew and this client has not been taught.
const (
	// CodeAgentLocked — the wallet is locked. The agent answers this two ways:
	// 423 after waiting out its unlock timeout (vault routes), or 401
	// immediately without waiting (wallet routes). See AgentError.Error.
	CodeAgentLocked = "AGENT_LOCKED"
	// CodeApprovalDenied — the user refused this application's request.
	CodeApprovalDenied = "APPROVAL_DENIED"
	// CodeApprovalTimeout — nobody answered the approval prompt in time.
	CodeApprovalTimeout = "APPROVAL_TIMEOUT"
	// CodePermissionDenied — the application is not permitted this capability.
	CodePermissionDenied = "PERMISSION_DENIED"
	// CodeNotFound — no such vault entry.
	CodeNotFound = "NOT_FOUND"
	// CodeInvalidRequest — the agent could not parse the request.
	CodeInvalidRequest = "INVALID_REQUEST"
	// CodePayloadTooLarge — the request body exceeded the agent's 1MiB limit.
	CodePayloadTooLarge = "PAYLOAD_TOO_LARGE"
	// CodePeerVanished — the calling process changed identity mid-request, so
	// the agent stopped trusting it.
	CodePeerVanished = "PEER_VANISHED"
	// CodeInternalError — the agent failed for its own reasons.
	CodeInternalError = "INTERNAL_ERROR"
	// CodeAgentNotRunning is this client's own code for an unreachable socket.
	CodeAgentNotRunning = "AGENT_NOT_RUNNING"
)

// AgentError represents an error returned by the rootwallet agent API.
type AgentError struct {
	Code       string // e.g., "AGENT_LOCKED", "NOT_FOUND"
	Message    string
	StatusCode int
}

// Error returns something the person at the terminal can act on.
//
// The agent's own message says what happened; the hint says what to do. Every
// error used to read "rootwallet agent: <message> (<code>)", which for a
// timed-out approval told a user their command had failed and nothing else.
func (e *AgentError) Error() string {
	base := fmt.Sprintf("rootwallet agent: %s (%s)", e.Message, e.Code)
	if hint := e.hint(); hint != "" {
		return base + " — " + hint
	}
	return base
}

func (e *AgentError) hint() string {
	switch e.Code {
	case CodeAgentLocked:
		// The agent answers a locked wallet two ways, and the difference
		// matters to the person waiting: the vault routes wait out their unlock
		// timeout and answer 423, while the wallet routes refuse at once with
		// 401. Telling someone their unlock "timed out" when it was never
		// waited for sends them looking for a slow machine.
		if e.StatusCode == http.StatusUnauthorized {
			return "unlock it in the RootWallet desktop app and run this again; this operation does not wait for an unlock"
		}
		return "unlock it in the RootWallet desktop app — the agent waited for an unlock and gave up"
	case CodeApprovalDenied:
		return "approve this application in the RootWallet desktop app"
	case CodeApprovalTimeout:
		return "the approval prompt went unanswered; run this again and approve it in the RootWallet desktop app"
	case CodePermissionDenied:
		return "grant this application the capability it asked for, under app permissions in the RootWallet desktop app"
	case CodeNotFound:
		return "no such entry in the vault"
	case CodePayloadTooLarge:
		return "the request exceeded the agent's 1MiB limit"
	case CodePeerVanished:
		return "the calling process changed while the request was in flight, so the agent stopped trusting it; run this again"
	case CodeInvalidRequest:
		return "this client sent something the agent could not parse — report it"
	case CodeInternalError:
		return "check the RootWallet desktop app's logs"
	}
	return ""
}

// IsLocked returns true if the error indicates the agent is locked.
func IsLocked(err error) bool { return hasCode(err, CodeAgentLocked) }

// IsNotRunning returns true if the error indicates the agent is not reachable.
func IsNotRunning(err error) bool {
	if hasCode(err, CodeAgentNotRunning) {
		return true
	}
	return errors.Is(err, ErrAgentNotRunning)
}

// IsNotFound returns true if the vault entry was not found.
func IsNotFound(err error) bool { return hasCode(err, CodeNotFound) }

// IsApprovalDenied returns true if the user refused the application's request.
func IsApprovalDenied(err error) bool {
	return hasCode(err, CodeApprovalDenied) || hasCode(err, CodePermissionDenied)
}

// IsApprovalTimeout returns true if nobody answered the approval prompt.
//
// This is a different outcome from a denial: the user did not say no, they were
// not there. Retrying is reasonable; retrying a denial is not.
func IsApprovalTimeout(err error) bool { return hasCode(err, CodeApprovalTimeout) }

// IsPeerVanished returns true if the agent stopped trusting the calling process
// mid-request, which happens when the binary is replaced under a running
// command. The operation is safe to retry.
func IsPeerVanished(err error) bool { return hasCode(err, CodePeerVanished) }

// IsPayloadTooLarge returns true if the request exceeded the agent's body limit.
func IsPayloadTooLarge(err error) bool { return hasCode(err, CodePayloadTooLarge) }

// IsRetryable reports whether running the same command again could succeed.
//
// A denied approval or a missing entry will not change on its own; an
// unanswered prompt or a vanished peer will.
func IsRetryable(err error) bool {
	return IsApprovalTimeout(err) || IsPeerVanished(err)
}

func hasCode(err error, code string) bool {
	var ae *AgentError
	if errors.As(err, &ae) {
		return ae.Code == code
	}
	return false
}

// ErrAgentNotRunning is returned when the agent socket is not reachable.
var ErrAgentNotRunning = fmt.Errorf("rootwallet agent is not reachable — open the RootWallet desktop app and unlock it")

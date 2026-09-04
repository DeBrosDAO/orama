package rwagent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These cover the skew between this client and the RootWallet agent it talks
// to: a timeout below the agent's own worst case, an ignored HTTP status, and
// six of the agent's nine error codes with no classification.

func TestTimeoutCoversTheAgentsWorstCase(t *testing.T) {
	// A first run against a locked wallet costs both agent waits in sequence:
	// check_permission (APPROVAL_TIMEOUT) then get_metadata_key_or_wait
	// (UNLOCK_WAIT_TIMEOUT). At 150s this client died in the middle of the very
	// flow the timeout existed for.
	worstCase := AgentApprovalTimeout + AgentUnlockWaitTimeout
	if DefaultTimeout <= worstCase {
		t.Errorf("DefaultTimeout is %s, which is not more than the agent's worst case of %s — "+
			"the client gives up before the agent answers", DefaultTimeout, worstCase)
	}
	if AgentApprovalTimeout != 120*time.Second {
		t.Errorf("AgentApprovalTimeout is %s; the agent's APPROVAL_TIMEOUT is 120s", AgentApprovalTimeout)
	}
	if AgentUnlockWaitTimeout != 120*time.Second {
		t.Errorf("AgentUnlockWaitTimeout is %s; the agent's UNLOCK_WAIT_TIMEOUT is 120s", AgentUnlockWaitTimeout)
	}
}

// agentStub serves canned responses over a Unix socket, the way the real agent
// does.
//
// It does not use t.TempDir: on macOS a Unix socket path is limited to about a
// hundred bytes, and t.TempDir builds one from the test's name, so a
// descriptively named test fails to bind rather than failing its assertion.
func agentStub(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	return New(shortSocket(t, handler))
}

func shortSocket(t *testing.T, handler http.Handler) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "rwa")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	socket := filepath.Join(dir, "a.sock")

	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", socket, err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = os.RemoveAll(dir)
	})
	return socket
}

// rawJSON answers with a literal body, so a test can send exactly the envelope
// the agent sends rather than one this package's own types round-trip.
func rawJSON(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestStatusCarriesPendingUnlocks(t *testing.T) {
	client := agentStub(t, rawJSON(200, `{"ok":true,"data":{
		"version":"1.2.3","locked":true,"uptime":42,"pid":9,
		"connectedApps":1,"pendingUnlocks":2}}`))

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	// The agent has always sent this; the client dropped it, so a command could
	// sit through the approval timeout unable to say a prompt was already open.
	if status.PendingUnlocks != 2 {
		t.Errorf("PendingUnlocks = %d, want 2", status.PendingUnlocks)
	}
	if !status.Locked || status.ConnectedApps != 1 {
		t.Errorf("the rest of the status was lost: %+v", status)
	}
}

// The agent reports a locked wallet two ways and the difference decides what
// the user should do, so the status has to survive the round trip.
func TestLockedIsToldApartByStatus(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantInHint string
	}{
		{"vault route waits then gives up", http.StatusLocked, "waited for an unlock and gave up"},
		{"wallet route refuses at once", http.StatusUnauthorized, "does not wait for an unlock"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := agentStub(t, rawJSON(tc.status,
				`{"ok":false,"error":"agent is locked","code":"AGENT_LOCKED"}`))

			_, err := client.GetSSHKey(context.Background(), "h", "u", "priv")
			if err == nil {
				t.Fatal("expected an error")
			}
			var agentErr *AgentError
			if !errors.As(err, &agentErr) {
				t.Fatalf("expected an AgentError, got %T", err)
			}
			if agentErr.StatusCode != tc.status {
				t.Errorf("StatusCode = %d, want %d — the HTTP status was dropped", agentErr.StatusCode, tc.status)
			}
			if !strings.Contains(err.Error(), tc.wantInHint) {
				t.Errorf("hint does not fit the status: %s", err)
			}
			if !IsLocked(err) {
				t.Error("IsLocked should recognise this")
			}
		})
	}
}

func TestEveryAgentCodeIsClassified(t *testing.T) {
	tests := []struct {
		code       string
		status     int
		classified func(error) bool
		wantHint   string
	}{
		{CodeAgentLocked, http.StatusLocked, IsLocked, "RootWallet desktop app"},
		{CodeApprovalDenied, http.StatusForbidden, IsApprovalDenied, "approve this application"},
		{CodePermissionDenied, http.StatusForbidden, IsApprovalDenied, "app permissions"},
		{CodeApprovalTimeout, http.StatusRequestTimeout, IsApprovalTimeout, "went unanswered"},
		{CodeNotFound, http.StatusNotFound, IsNotFound, "no such entry"},
		{CodePayloadTooLarge, http.StatusRequestEntityTooLarge, IsPayloadTooLarge, "1MiB"},
		{CodePeerVanished, http.StatusForbidden, IsPeerVanished, "stopped trusting"},
	}

	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			client := agentStub(t, rawJSON(tc.status,
				fmt.Sprintf(`{"ok":false,"error":"something happened","code":%q}`, tc.code)))

			_, err := client.GetSSHKey(context.Background(), "h", "u", "priv")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !tc.classified(err) {
				t.Errorf("%s is not recognised by its classifier: %v", tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("%s has no actionable hint: %s", tc.code, err)
			}
		})
	}
}

// An unanswered prompt or a vanished peer can succeed on a second run; a
// refusal or a missing entry cannot. Telling a user to retry a denial wastes
// their time and their patience.
func TestRetryableSeparatesWaitingFromRefusing(t *testing.T) {
	retryable := []string{CodeApprovalTimeout, CodePeerVanished}
	terminal := []string{CodeAgentLocked, CodeApprovalDenied, CodePermissionDenied, CodeNotFound, CodeInvalidRequest}

	for _, code := range retryable {
		if !IsRetryable(&AgentError{Code: code}) {
			t.Errorf("%s should be retryable", code)
		}
	}
	for _, code := range terminal {
		if IsRetryable(&AgentError{Code: code}) {
			t.Errorf("%s should not be retryable", code)
		}
	}
}

// A body that is not the agent's envelope is still an answer, and the status
// says what kind. Every one of these used to arrive as "decode response".
func TestNonJSONBodyKeepsItsStatus(t *testing.T) {
	client := agentStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte("request too large\nsecond line that should not be pasted"))
	})

	_, err := client.GetSSHKey(context.Background(), "h", "u", "priv")
	if err == nil {
		t.Fatal("expected an error")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("expected an AgentError, got %T: %v", err, err)
	}
	if agentErr.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("StatusCode = %d, want 413", agentErr.StatusCode)
	}
	if !IsPayloadTooLarge(err) {
		t.Errorf("a 413 with no code should still be classified: %v", err)
	}
	if strings.Contains(err.Error(), "second line") {
		t.Errorf("the whole body was pasted into the error: %s", err)
	}
	if strings.Contains(err.Error(), "decode response") {
		t.Errorf("a non-JSON body is not a decoding bug: %s", err)
	}
}

// missingSocket returns a path where no agent is listening.
func missingSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rwa")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "gone.sock")
}

func TestUnreachableAgentIsRecognised(t *testing.T) {
	client := New(missingSocket(t))
	_, statusErr := client.Status(context.Background())
	if !IsNotRunning(statusErr) {
		t.Errorf("an absent socket should read as a stopped agent, got: %v", statusErr)
	}
}

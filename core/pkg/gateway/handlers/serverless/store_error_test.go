package serverless

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// A namespace database without a leader answered every function route with an
// unclassified 500 (stagenet e2e, 2026-09-30: listing functions just after
// provisioning). It is a retryable 503 now, and no driver text reaches the
// caller either way.
func TestWriteStoreError(t *testing.T) {
	unavailable := errors.New("tried all peers unsuccessfully: 503 Service Unavailable, message: leader not found")
	rec := httptest.NewRecorder()
	writeStoreError(rec, "Failed to list functions", unavailable)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"retryable":true`) {
		t.Fatalf("no leader: %d %s, want a retryable 503", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "peers") {
		t.Errorf("the driver's text reached the caller: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	writeStoreError(rec, "Failed to set secret", errors.New("UNIQUE constraint failed: function_secrets.name"))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "UNIQUE") {
		t.Fatalf("a fault: %d %s, want a 500 without the driver's text", rec.Code, rec.Body.String())
	}
}

// A deploy that lost the namespace database's leader mid-election answered a
// non-retryable 500 FUNCTION_DEPLOY_FAILED (stagenet e2e, 2026-10-01: "failed
// to query function: not leader"). It is a retryable 503; a rejected function
// stays a 500, and a function's own name is never read as a transport error.
func TestStoreUnavailable_deployErrors(t *testing.T) {
	notLeader := &serverless.DeployError{FunctionName: "ref-fetch",
		Cause: fmt.Errorf("failed to query function: %w", errors.New("not leader"))}
	if !storeUnavailable(notLeader) {
		t.Fatal("not leader during a deploy must be unavailable")
	}
	rec := httptest.NewRecorder()
	writeStoreError(rec, "Failed to deploy function", notLeader)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"retryable":true`) {
		t.Fatalf("got %d %s, want a retryable 503", rec.Code, rec.Body.String())
	}

	if !storeUnavailable(fmt.Errorf("wrapped: %w", notLeader)) {
		t.Error("a wrapped DeployError must still classify by its cause")
	}
	named := &serverless.DeployError{FunctionName: "timeout-probe", Cause: errors.New("wasm rejected: bad magic")}
	if storeUnavailable(named) {
		t.Error("a function named timeout-probe was read as a transport failure")
	}
	if storeUnavailable(nil) {
		t.Error("nil is not unavailable")
	}
}

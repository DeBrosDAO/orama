package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// internalFailure is the kind of text an attempt fails with: addresses,
// statements, credentials in a DSN. None of it may reach a caller.
const internalFailure = "Post http://orama:s3cret@10.0.0.7:10100/db/execute: SELECT COUNT(*) FROM api_keys: leader not found"

// convergeOnce drives the readiness loop through one failed attempt and stops
// it, leaving the gateway in the state that attempt produced.
func convergeOnce(t *testing.T, g *Gateway, err error) {
	t.Helper()
	fastRetries(t)
	ctx, cancel := context.WithCancel(context.Background())
	prepare := func(context.Context) error {
		cancel()
		return err
	}
	done := make(chan struct{})
	go func() {
		g.convergeSchema(ctx, prepare, func() string { return "Follower" })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("convergeSchema did not stop")
	}
}

// unauthenticatedBodies is every response a caller without credentials gets
// from a gateway that is not ready: a refused request and both health paths.
func unauthenticatedBodies(t *testing.T, g *Gateway) map[string]string {
	t.Helper()
	bodies := map[string]string{}
	gate := g.readinessGate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a gateway that is not ready served a request")
	}))
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/storage/pin", nil))
	bodies["refusal"] = rec.Body.String()
	for _, path := range []string{"/health", "/v1/health"} {
		rec := httptest.NewRecorder()
		g.healthHandler(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, rec.Code)
		}
		bodies[path] = rec.Body.String()
	}
	return bodies
}

func TestReadiness_rawErrorsNeverReachAnUnauthenticatedResponse(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantState ReadinessState
		wantCode  ReadinessReason
	}{
		{"schema attempt", fmt.Errorf("rqlite not ready for schema work: %s", internalFailure), ReadinessStarting, ReasonSchema},
		{"gating step", fmt.Errorf("%w: remove plaintext API keys: %s", errPostSchemaStep, internalFailure), ReadinessStarting, ReasonPostSchema},
		{"schema contract", fmt.Errorf("%w: %s", errSchemaContract, internalFailure), ReadinessBlocked, ReasonSchemaVersion},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newReadinessGateway(t)
			convergeOnce(t, g, tc.err)

			// The detail is kept for the process and its log.
			if state, detail := g.Readiness(); state != tc.wantState || !strings.Contains(detail, "leader not found") {
				t.Fatalf("Readiness() = %q, %q; want %q with the full error", state, detail, tc.wantState)
			}
			for where, body := range unauthenticatedBodies(t, g) {
				for _, leak := range []string{"s3cret", "10.0.0.7", "SELECT", "leader not found", "api_keys"} {
					if strings.Contains(body, leak) {
						t.Errorf("%s leaks %q: %s", where, leak, body)
					}
				}
				var got map[string]any
				if err := json.Unmarshal([]byte(body), &got); err != nil {
					t.Fatalf("%s is not JSON: %v", where, err)
				}
				if got["status"] != string(tc.wantState) || got["reason"] != string(tc.wantCode) {
					t.Errorf("%s = status %v reason %v, want %q/%q", where, got["status"], got["reason"], tc.wantState, tc.wantCode)
				}
			}
		})
	}
}

// Before the loop has reported anything the gateway says so with a code too.
func TestReadiness_initialStateHasACode(t *testing.T) {
	g := newReadinessGateway(t)
	for where, body := range unauthenticatedBodies(t, g) {
		if !strings.Contains(body, `"reason":"`+string(ReasonInitializing)+`"`) {
			t.Errorf("%s = %s, want reason %q", where, body, ReasonInitializing)
		}
	}

	var zero Gateway
	if state, code, _, _ := zero.ready.snapshot(); state != ReadinessStarting || code != ReasonInitializing {
		t.Errorf("an uninitialised readiness = %q/%q, want starting/initializing", state, code)
	}
}

func TestRetryReason(t *testing.T) {
	if got := retryReason(fmt.Errorf("outer: %w", fmt.Errorf("%w: x", errPostSchemaStep))); got != ReasonPostSchema {
		t.Errorf("a wrapped gating-step failure = %q, want %q", got, ReasonPostSchema)
	}
	if got := retryReason(fmt.Errorf("no leader")); got != ReasonSchema {
		t.Errorf("a schema failure = %q, want %q", got, ReasonSchema)
	}
}

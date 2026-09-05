package production

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fastPolling shortens the retry interval so these tests exercise the logic
// rather than the clock.
func fastPolling(t *testing.T) {
	t.Helper()
	original := verifyPollInterval
	verifyPollInterval = time.Millisecond
	t.Cleanup(func() { verifyPollInterval = original })
}

func TestAwaitVerify_succeeds_immediately(t *testing.T) {
	fastPolling(t)
	calls := 0
	err := awaitVerify(context.Background(), time.Second, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("probed %d times, want 1", calls)
	}
}

func TestAwaitVerify_succeeds_after_retries(t *testing.T) {
	fastPolling(t)
	calls := 0
	err := awaitVerify(context.Background(), 30*time.Second, func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("probed %d times, want 3", calls)
	}
}

// The timeout error must carry the last DIAGNOSTIC error, not just "timed out".
// The diagnostic is the part that says what to fix; a bare timeout sends an
// operator looking at the wrong thing.
func TestAwaitVerify_timeout_reports_the_last_diagnostic(t *testing.T) {
	fastPolling(t)
	err := awaitVerify(context.Background(), 10*time.Millisecond, func(context.Context) error {
		return errors.New("connection refused on port 10100")
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "connection refused on port 10100") {
		t.Fatalf("timeout error dropped the diagnostic: %v", err)
	}
}

func TestAwaitVerify_cancellation_reports_the_last_diagnostic(t *testing.T) {
	fastPolling(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := awaitVerify(ctx, time.Minute, func(context.Context) error {
		return errors.New("rqlite still Candidate")
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "rqlite still Candidate") {
		t.Fatalf("cancellation error dropped the diagnostic: %v", err)
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancellation not identified as such: %v", err)
	}
}

// A probe that never runs to completion must not be reported as ready. This is
// the property the whole phase rests on: install exits non-zero, so nothing
// downstream proceeds on the assumption that the node came up.
func TestAwaitVerify_never_ready_is_an_error(t *testing.T) {
	fastPolling(t)
	if err := awaitVerify(context.Background(), 10*time.Millisecond, func(context.Context) error {
		return errors.New("nope")
	}); err == nil {
		t.Fatal("a probe that never succeeded reported ready")
	}
}

func TestVerifyFailure_names_the_component(t *testing.T) {
	cause := errors.New("connection refused")
	err := &VerifyFailure{Component: "gateway /health", Err: cause}

	if !strings.Contains(err.Error(), "gateway /health") {
		t.Fatalf("does not name the component: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("does not unwrap to the cause")
	}
}

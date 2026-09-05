package namespace

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hostPortOf strips the scheme so a probe can be pointed at a test server.
func hostPortOf(url string) string { return strings.TrimPrefix(url, "http://") }

func TestRQLiteReady_acceptsLeaderAndFollower(t *testing.T) {
	for _, state := range []string{"Leader", "Follower", "leader", "follower"} {
		t.Run(state, func(t *testing.T) {
			srv := httptest.NewServer(rqliteHandler(state, ""))
			defer srv.Close()

			if err := rqliteReady(context.Background(), hostPortOf(srv.URL)); err != nil {
				t.Fatalf("state %q should be ready: %v", state, err)
			}
		})
	}
}

func TestRQLiteReady_rejectsAStateThatIsNotServing(t *testing.T) {
	// The whole reason a port probe is not enough: rqlite binds its listener
	// before it has elected anything.
	for _, state := range []string{"Candidate", "Shutdown", ""} {
		t.Run(state, func(t *testing.T) {
			srv := httptest.NewServer(rqliteHandler(state, ""))
			defer srv.Close()

			err := rqliteReady(context.Background(), hostPortOf(srv.URL))
			if err == nil {
				t.Fatalf("state %q was accepted as ready", state)
			}
		})
	}
}

func TestRQLiteReady_rejectsAQueryErrorInsideA200(t *testing.T) {
	// rqlite answers HTTP 200 with the failure in the body, so the status code
	// says nothing on its own.
	srv := httptest.NewServer(rqliteHandler("Leader", "no leader"))
	defer srv.Close()

	err := rqliteReady(context.Background(), hostPortOf(srv.URL))
	if err == nil {
		t.Fatal("a 200 carrying a query error was accepted as ready")
	}
	if !strings.Contains(err.Error(), "no leader") {
		t.Fatalf("the error should carry rqlite's reason: %v", err)
	}
}

func TestRQLiteReady_refusedConnection(t *testing.T) {
	if err := rqliteReady(context.Background(), "127.0.0.1:1"); err == nil {
		t.Fatal("a closed port was accepted as ready")
	}
}

func rqliteHandler(state, queryErr string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"store":{"raft":{"state":%q}}}`, state)
	})
	mux.HandleFunc("/db/query", func(w http.ResponseWriter, _ *http.Request) {
		if queryErr != "" {
			fmt.Fprintf(w, `{"results":[{"error":%q}]}`, queryErr)
			return
		}
		fmt.Fprint(w, `{"results":[{"columns":["1"],"values":[[1]]}]}`)
	})
	return mux
}

func TestGatewayReady_rejectsAnUnhealthyDependency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"status":"ok","services":{"rqlite":"ok","olric":"unavailable"}}`)
	}))
	defer srv.Close()

	err := gatewayReady(context.Background(), hostPortOf(srv.URL))
	if err == nil {
		t.Fatal("a gateway that cannot reach Olric was accepted as ready")
	}
	if !strings.Contains(err.Error(), "olric") {
		t.Fatalf("the error should name the dependency: %v", err)
	}
}

func TestGatewayReady_acceptsEveryDependencyHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"status":"healthy","services":{"rqlite":"ok","olric":"connected"}}`)
	}))
	defer srv.Close()

	if err := gatewayReady(context.Background(), hostPortOf(srv.URL)); err != nil {
		t.Fatalf("a healthy gateway was rejected: %v", err)
	}
}

func TestGatewayReady_rejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if err := gatewayReady(context.Background(), hostPortOf(srv.URL)); err == nil {
		t.Fatal("a 503 was accepted as ready")
	}
}

func TestOlricReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"member":{"name":"test"}}`)
	}))
	defer srv.Close()

	if err := olricReady(context.Background(), hostPortOf(srv.URL)); err != nil {
		t.Fatalf("an answering Olric was rejected: %v", err)
	}
	if err := olricReady(context.Background(), "127.0.0.1:1"); err == nil {
		t.Fatal("a closed port was accepted as ready")
	}
}

func TestAwaitReady_succeedsOnceTheProbeStopsFailing(t *testing.T) {
	attempts := 0
	err := awaitReady(context.Background(), 5*time.Second, "thing", func(context.Context) error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("not yet")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("awaitReady: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("got %d attempts, want 3", attempts)
	}
}

func TestAwaitReady_reportsTheDiagnosticReasonNotTheTimeout(t *testing.T) {
	// The reason an operator needs is "still Candidate", not "context deadline
	// exceeded" — and once the budget is nearly spent every probe returns the
	// latter.
	err := awaitReady(context.Background(), 300*time.Millisecond, "rqlite", func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("in raft state \"Candidate\"")
	})
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if !strings.Contains(err.Error(), "Candidate") {
		t.Fatalf("the diagnostic reason was lost: %v", err)
	}
}

func TestAwaitReady_honoursACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := awaitReady(ctx, time.Minute, "thing", func(context.Context) error {
		t.Fatal("the probe ran despite a cancelled context")
		return nil
	})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestHealthyState(t *testing.T) {
	for _, s := range []string{"ok", "OK", " healthy ", "up", "ready", "connected"} {
		if !healthyState(s) {
			t.Errorf("healthyState(%q) = false", s)
		}
	}
	for _, s := range []string{"", "degraded", "unavailable", "error", "down", "unknown"} {
		if healthyState(s) {
			t.Errorf("healthyState(%q) = true", s)
		}
	}
}

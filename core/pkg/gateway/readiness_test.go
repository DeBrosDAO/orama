package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

func newReadinessGateway(t *testing.T) *Gateway {
	t.Helper()
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	return &Gateway{logger: logger, ready: newReadiness(), startedAt: time.Now()}
}

// fastRetries shrinks the backoff so the loop can be driven in a test without
// waiting out real seconds.
func fastRetries(t *testing.T) {
	t.Helper()
	base, max := schemaRetryBaseBackoff, schemaRetryMaxBackoff
	schemaRetryBaseBackoff = time.Millisecond
	schemaRetryMaxBackoff = 5 * time.Millisecond
	t.Cleanup(func() { schemaRetryBaseBackoff, schemaRetryMaxBackoff = base, max })
}

func TestNewReadiness_startsAsStarting(t *testing.T) {
	g := newReadinessGateway(t)
	if state, _ := g.Readiness(); state != ReadinessStarting {
		t.Fatalf("initial state = %q, want %q", state, ReadinessStarting)
	}
	if g.IsReady() {
		t.Fatal("a gateway that has not converged must not report ready")
	}
}

// "No leader yet" is transient and must never stop the gateway retrying. This
// is the reproduction: a cluster with no leader for a while, then an election.
func TestConvergeSchema_retriesUntilALeaderAppears(t *testing.T) {
	fastRetries(t)
	g := newReadinessGateway(t)

	var mu sync.Mutex
	attempts := 0
	prepare := func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts < 5 {
			return fmt.Errorf("rqlite not ready for schema work: no leader")
		}
		return nil
	}

	done := make(chan struct{})
	go func() {
		g.convergeSchema(context.Background(), prepare, func() string { return "Candidate" })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("convergeSchema never finished")
	}

	if state, reason := g.Readiness(); state != ReadinessReady {
		t.Fatalf("state = %q (%s), want ready", state, reason)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 5 {
		t.Fatalf("prepared %d times, want 5", attempts)
	}
}

// A schema genuinely below the binary's required version does not fix itself,
// and serving on it corrupts data. The loop must stop and say so.
func TestConvergeSchema_stopsOnASchemaContractViolation(t *testing.T) {
	fastRetries(t)
	g := newReadinessGateway(t)

	var mu sync.Mutex
	attempts := 0
	prepare := func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		return fmt.Errorf("%w: applied=3, required=7", errSchemaContract)
	}

	g.convergeSchema(context.Background(), prepare, func() string { return "Leader" })

	state, reason := g.Readiness()
	if state != ReadinessBlocked {
		t.Fatalf("state = %q, want %q", state, ReadinessBlocked)
	}
	if reason == "" {
		t.Fatal("a blocked gateway must say why")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Fatalf("retried a contract violation %d times; it cannot fix itself", attempts)
	}
}

func TestConvergeSchema_stopsOnCancellation(t *testing.T) {
	fastRetries(t)
	g := newReadinessGateway(t)

	ctx, cancel := context.WithCancel(context.Background())
	prepare := func(context.Context) error { return errors.New("not yet") }

	done := make(chan struct{})
	go func() {
		g.convergeSchema(ctx, prepare, func() string { return "unknown" })
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("convergeSchema did not stop on cancellation")
	}
}

func TestIsSchemaContractViolation(t *testing.T) {
	if isSchemaContractViolation(errors.New("rqlite not ready for schema work")) {
		t.Error("a leader-wait failure must be retryable, not fatal")
	}
	if !isSchemaContractViolation(fmt.Errorf("%w: applied=1, required=2", errSchemaContract)) {
		t.Error("a wrapped contract violation must be recognised")
	}
	if !isSchemaContractViolation(fmt.Errorf("outer: %w", fmt.Errorf("%w: x", errSchemaContract))) {
		t.Error("a doubly-wrapped contract violation must be recognised")
	}
}

func TestReadinessGate(t *testing.T) {
	tests := []struct {
		name       string
		state      ReadinessState
		path       string
		wantStatus int
		wantServed bool
	}{
		{"ready gateway serves everything", ReadinessReady, "/v1/storage/pin", http.StatusOK, true},
		{"starting gateway refuses work", ReadinessStarting, "/v1/storage/pin", http.StatusServiceUnavailable, false},
		{"blocked gateway refuses work", ReadinessBlocked, "/v1/storage/pin", http.StatusServiceUnavailable, false},

		// These are all isPublicPath — they need no API key — and every one of
		// them writes to or reads from the database. Reusing that predicate for
		// readiness would let a gateway that has decided its schema is below
		// what this binary requires keep writing to that schema, which is the
		// corruption ReadinessBlocked exists to prevent.
		{"auth writes are refused while blocked", ReadinessBlocked, "/v1/auth/verify", http.StatusServiceUnavailable, false},
		{"registration is refused while blocked", ReadinessBlocked, "/v1/auth/register", http.StatusServiceUnavailable, false},
		{"api-key issuance is refused while blocked", ReadinessBlocked, "/v1/auth/api-key", http.StatusServiceUnavailable, false},
		{"serverless invoke is refused while starting", ReadinessStarting, "/v1/invoke/fn", http.StatusServiceUnavailable, false},
		{"vault proxy is refused while starting", ReadinessStarting, "/v1/vault/unseal", http.StatusServiceUnavailable, false},
		{"cluster join is refused while starting", ReadinessStarting, "/v1/internal/join", http.StatusServiceUnavailable, false},
		{"namespace spawn is refused while starting", ReadinessStarting, "/v1/namespace/spawn", http.StatusServiceUnavailable, false},
		{"storage eviction is refused while starting", ReadinessStarting, "/v1/internal/storage/evict", http.StatusServiceUnavailable, false},

		// Diagnosis, peer liveness and certificate issuance must survive.
		{"health stays reachable while starting", ReadinessStarting, "/v1/health", http.StatusOK, true},
		{"peer liveness stays reachable while starting", ReadinessStarting, "/v1/internal/ping", http.StatusOK, true},
		{"health stays reachable while blocked", ReadinessBlocked, "/health", http.StatusOK, true},
		{"status stays reachable while blocked", ReadinessBlocked, "/v1/status", http.StatusOK, true},
		{"version stays reachable while blocked", ReadinessBlocked, "/v1/version", http.StatusOK, true},
		{"the TLS check stays reachable", ReadinessStarting, "/v1/internal/tls/check", http.StatusOK, true},
		{"ACME challenges stay reachable", ReadinessStarting, "/.well-known/acme-challenge/abc", http.StatusOK, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newReadinessGateway(t)
			g.ready.set(tc.state, "because")

			served := false
			handler := g.readinessGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				served = true
				w.WriteHeader(http.StatusOK)
			}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

			if served != tc.wantServed {
				t.Fatalf("handler reached = %v, want %v", served, tc.wantServed)
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantServed {
				return
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("refusal body is not JSON: %v", err)
			}
			if body["status"] != string(tc.state) {
				t.Errorf("body status = %v, want %q", body["status"], tc.state)
			}
			if body["reason"] != "because" {
				t.Errorf("refusal must carry the reason, got %v", body["reason"])
			}
		})
	}
}

// The health endpoint is how an operator and the DNS reconcile tell a gateway
// that is coming up from one that is serving.
func TestHealthHandler_reportsReadinessBeforeSubsystemChecks(t *testing.T) {
	for _, state := range []ReadinessState{ReadinessStarting, ReadinessBlocked} {
		t.Run(string(state), func(t *testing.T) {
			g := newReadinessGateway(t)
			g.ready.set(state, "waiting for rqlite leader")

			rec := httptest.NewRecorder()
			g.healthHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v", err)
			}
			if body["status"] != string(state) {
				t.Errorf("status = %v, want %q", body["status"], state)
			}
			if body["reason"] != "waiting for rqlite leader" {
				t.Errorf("reason = %v", body["reason"])
			}
			if _, hasChecks := body["checks"]; hasChecks {
				t.Error("a gateway that is not ready must not report subsystem checks as if it were")
			}
		})
	}
}

func TestRQLiteHostPortFromDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{"empty", "", ""},
		{"local", "http://localhost:10100?disableClusterDiscovery=true", "localhost:10100"},
		// A namespace gateway can be configured against a remote rqlite, and
		// reporting the LOCAL node's raft state under that address would be
		// confidently wrong rather than merely unknown.
		{"remote with credentials", "http://user:pass@10.0.0.4:10000?level=weak", "10.0.0.4:10000"},
		{"no port", "http://localhost", ""},
		{"not a url", "://not a url", ""},
		{"non-numeric port", "http://localhost:notaport", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rqliteHostPortFromDSN(tc.dsn); got != tc.want {
				t.Fatalf("rqliteHostPortFromDSN(%q) = %q, want %q", tc.dsn, got, tc.want)
			}
		})
	}
}

// Background work that touches the database has to wait for the schema; before
// the schema work moved off the start-up path it was implicitly ordered after
// the migrations.
func TestAwaitReady(t *testing.T) {
	g := newReadinessGateway(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if g.AwaitReady(ctx) {
		t.Fatal("AwaitReady returned true for a gateway that never became ready")
	}

	g2 := newReadinessGateway(t)
	go func() {
		time.Sleep(10 * time.Millisecond)
		g2.ready.set(ReadinessReady, "")
	}()
	if !g2.AwaitReady(context.Background()) {
		t.Fatal("AwaitReady did not unblock when the gateway became ready")
	}

	// Already ready is an immediate true, not a second wait on a closed channel.
	if !g2.AwaitReady(context.Background()) {
		t.Fatal("AwaitReady must keep returning true once ready")
	}
}

// A Gateway assembled without New has no readiness channel; waiting on it must
// end with the context rather than blocking for ever or panicking.
func TestAwaitReady_zeroValueGatewayNeverBecomesReady(t *testing.T) {
	g := &Gateway{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if g.AwaitReady(ctx) {
		t.Fatal("a zero-value gateway must never report ready")
	}
}

func TestConfigSchemaBudgets(t *testing.T) {
	cfg := &Config{}
	if got := cfg.rqliteReadyTimeout(); got != defaultRQLiteReadyTimeout {
		t.Errorf("default ready timeout = %s", got)
	}
	if got := cfg.schemaApplyTimeout(); got != defaultSchemaApplyTimeout {
		t.Errorf("default apply timeout = %s", got)
	}

	cfg = &Config{RQLiteReadyTimeout: time.Second, SchemaApplyTimeout: 2 * time.Second}
	if got := cfg.rqliteReadyTimeout(); got != time.Second {
		t.Errorf("configured ready timeout ignored: %s", got)
	}
	if got := cfg.schemaApplyTimeout(); got != 2*time.Second {
		t.Errorf("configured apply timeout ignored: %s", got)
	}
}

// A namespace gateway binds and answers long before it has a usable schema, so
// dialling its port cannot tell whether it can serve. This is what kept a
// gateway with no schema in the DNS round-robin.
func TestProbeGatewayReadiness(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantStatus string
	}{
		{
			name: "serving gateway",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{"status": "healthy"})
			},
			wantStatus: "ok",
		},
		{
			name: "gateway still waiting for its schema",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{
					"status": string(ReadinessStarting), "reason": "no leader",
				})
			},
			wantStatus: "starting",
		},
		{
			name: "gateway blocked on a schema contract violation",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{
					"status": string(ReadinessBlocked), "reason": "applied=1 required=2",
				})
			},
			wantStatus: "error",
		},
		{
			// Subsystem health is not this probe's business: rqlite and Olric
			// have their own probes, and withdrawing a node because its IPFS
			// blipped turns one unavailable subsystem into an unavailable node.
			name: "degraded subsystems still serve",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded"})
			},
			wantStatus: "ok",
		},
		{
			name: "unhealthy subsystems are still the subsystems' problem",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unhealthy"})
			},
			wantStatus: "ok",
		},
		{
			name: "port answers but not with health JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("nope"))
			},
			wantStatus: "error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			host, port := splitHostPortForTest(t, srv.URL)
			got := probeGatewayReadiness(context.Background(), host, port)
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %q (%s), want %q", got.Status, got.Error, tc.wantStatus)
			}
			if got.Port != port {
				t.Errorf("port = %d, want %d", got.Port, port)
			}
		})
	}
}

func TestProbeGatewayReadiness_unreachablePortIsAnError(t *testing.T) {
	// A port nothing is listening on: bind and immediately release one.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host, port := splitHostPortForTest(t, srv.URL)
	srv.Close()

	got := probeGatewayReadiness(context.Background(), host, port)
	if got.Status != "error" {
		t.Fatalf("status = %q, want error", got.Status)
	}
}

func splitHostPortForTest(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split %q: %v", u.Host, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	return host, port
}

// The DNS reconcile keys on NamespaceHealth.Status != "healthy", so which
// service statuses roll up to which namespace status decides whether a node
// stays in the ns-<name> round-robin.
func TestAggregateNamespaceStatus(t *testing.T) {
	tests := []struct {
		name     string
		services map[string]string
		want     string
		inDNS    bool
	}{
		{"all serving", map[string]string{"rqlite": "ok", "olric": "ok", "gateway": "ok"}, "healthy", true},
		{"gateway coming up", map[string]string{"rqlite": "ok", "olric": "ok", "gateway": "starting"}, "starting", false},
		{"a service is down", map[string]string{"rqlite": "error", "olric": "ok", "gateway": "ok"}, "unhealthy", false},
		{"down beats coming up", map[string]string{"rqlite": "error", "olric": "ok", "gateway": "starting"}, "unhealthy", false},

		{"no services probed", nil, "healthy", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			services := map[string]NamespaceServiceHealth{}
			for name, status := range tc.services {
				services[name] = NamespaceServiceHealth{Status: status}
			}

			got := aggregateNamespaceStatus(services)
			if got != tc.want {
				t.Fatalf("aggregateNamespaceStatus = %q, want %q", got, tc.want)
			}
			if inDNS := got == "healthy"; inDNS != tc.inDNS {
				t.Errorf("stays in the DNS round-robin = %v, want %v", inDNS, tc.inDNS)
			}
		})
	}
}

// TestReadinessGate exercises the gate in isolation, which says nothing about
// whether the gate is actually in the chain. Deleting g.readinessGate from
// withMiddleware left that test green and restored the original bug in full,
// so this asserts the wiring itself.
func TestWithMiddleware_gatesWhenNotReady(t *testing.T) {
	served := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	})

	g := newReadinessGateway(t)
	g.cfg = &Config{}
	g.ready.set(ReadinessStarting, "waiting for rqlite leader")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/storage/pin", nil)
	g.withMiddleware(inner).ServeHTTP(rec, req)

	if served {
		t.Fatal("the readiness gate is not in the middleware chain: a starting gateway reached the handler")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("refusal body is not JSON: %v", err)
	}
	if body["reason"] != "waiting for rqlite leader" {
		t.Errorf("the refusal must carry the reason, got %v", body["reason"])
	}
}

// A browser can only read the refusal if it carries CORS headers, which is why
// the gate sits inside corsMiddleware rather than above it.
func TestWithMiddleware_refusalIsReadableByABrowser(t *testing.T) {
	g := newReadinessGateway(t)
	g.cfg = &Config{}
	g.ready.set(ReadinessStarting, "waiting for rqlite leader")

	handler := g.withMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/storage/pin", nil)
	req.Header.Set("Origin", "https://example.test")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("the refusal has no CORS headers, so a browser client sees an opaque network error instead of the reason")
	}
}

// A read failure is not a contract violation. Latching the gateway into
// blocked on one would let a momentary blip take a namespace down until
// someone restarted the process.
func TestPrepareSchema_readFailureIsRetryableNotFatal(t *testing.T) {
	readFailure := fmt.Errorf("schema contract read failed: %w",
		errors.New("migrations: query schema_migrations: leader not found"))
	if isSchemaContractViolation(readFailure) {
		t.Fatal("a failure to READ schema_migrations must stay retryable")
	}

	mismatch := fmt.Errorf("%w: %w", errSchemaContract,
		errors.New("schema below required version"))
	if !isSchemaContractViolation(mismatch) {
		t.Fatal("a genuine version mismatch must stop the loop")
	}
}

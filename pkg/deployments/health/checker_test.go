package health

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Mock database
// ---------------------------------------------------------------------------

// queryCall records the arguments passed to a Query invocation.
type queryCall struct {
	query string
	args  []interface{}
}

// execCall records the arguments passed to an Exec invocation.
type execCall struct {
	query string
	args  []interface{}
}

// mockDB implements database.Database with configurable responses.
type mockDB struct {
	mu sync.Mutex

	// Query handling ---------------------------------------------------
	// queryFunc is called when Query is invoked. It receives the dest
	// pointer and the query string + args. The implementation should
	// populate dest (via reflection) and return an error if desired.
	queryFunc func(dest interface{}, query string, args ...interface{}) error
	queryCalls []queryCall

	// Exec handling ----------------------------------------------------
	execFunc  func(query string, args ...interface{}) (interface{}, error)
	execCalls []execCall
}

func (m *mockDB) Query(_ context.Context, dest interface{}, query string, args ...interface{}) error {
	m.mu.Lock()
	m.queryCalls = append(m.queryCalls, queryCall{query: query, args: args})
	fn := m.queryFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(dest, query, args...)
	}
	return nil
}

func (m *mockDB) QueryOne(_ context.Context, dest interface{}, query string, args ...interface{}) error {
	m.mu.Lock()
	m.queryCalls = append(m.queryCalls, queryCall{query: query, args: args})
	m.mu.Unlock()
	return nil
}

func (m *mockDB) Exec(_ context.Context, query string, args ...interface{}) (interface{}, error) {
	m.mu.Lock()
	m.execCalls = append(m.execCalls, execCall{query: query, args: args})
	fn := m.execFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(query, args...)
	}
	return nil, nil
}

// getExecCalls returns a snapshot of the recorded Exec calls.
func (m *mockDB) getExecCalls() []execCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]execCall, len(m.execCalls))
	copy(out, m.execCalls)
	return out
}

// getQueryCalls returns a snapshot of the recorded Query calls.
func (m *mockDB) getQueryCalls() []queryCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]queryCall, len(m.queryCalls))
	copy(out, m.queryCalls)
	return out
}

// ---------------------------------------------------------------------------
// Helper: populate a *[]T dest via reflection so the mock can return rows.
// ---------------------------------------------------------------------------

// appendRows appends rows to dest (a *[]SomeStruct) by creating new elements
// of the destination's element type and copying field values by name.
// Each row is a map[string]interface{} keyed by field name (Go name, not db tag).
// This sidesteps the type-identity problem where the mock and the caller
// define structurally identical but distinct local types.
func appendRows(dest interface{}, rows []map[string]interface{}) {
	dv := reflect.ValueOf(dest).Elem() // []T
	elemType := dv.Type().Elem()        // T

	for _, row := range rows {
		elem := reflect.New(elemType).Elem()
		for name, val := range row {
			f := elem.FieldByName(name)
			if f.IsValid() && f.CanSet() {
				f.Set(reflect.ValueOf(val))
			}
		}
		dv = reflect.Append(dv, elem)
	}
	reflect.ValueOf(dest).Elem().Set(dv)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// ---- a) NewHealthChecker --------------------------------------------------

func TestNewHealthChecker_NonNil(t *testing.T) {
	db := &mockDB{}
	logger := zap.NewNop()

	hc := NewHealthChecker(db, logger)

	if hc == nil {
		t.Fatal("expected non-nil HealthChecker")
	}
	if hc.db != db {
		t.Error("expected db to be stored")
	}
	if hc.logger != logger {
		t.Error("expected logger to be stored")
	}
	if hc.workers != 10 {
		t.Errorf("expected default workers=10, got %d", hc.workers)
	}
	if hc.active == nil {
		t.Error("expected active map to be initialized")
	}
	if len(hc.active) != 0 {
		t.Errorf("expected active map to be empty, got %d entries", len(hc.active))
	}
}

// ---- b) checkDeployment ---------------------------------------------------

func TestCheckDeployment_StaticDeployment(t *testing.T) {
	db := &mockDB{}
	hc := NewHealthChecker(db, zap.NewNop())

	dep := deploymentRow{
		ID:   "dep-1",
		Name: "static-site",
		Port: 0, // static deployment
	}

	if !hc.checkDeployment(context.Background(), dep) {
		t.Error("static deployment (port 0) should always be healthy")
	}
}

func TestCheckDeployment_HealthyEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// Extract the port from the test server.
	port := serverPort(t, srv)

	db := &mockDB{}
	hc := NewHealthChecker(db, zap.NewNop())

	dep := deploymentRow{
		ID:              "dep-2",
		Name:            "web-app",
		Port:            port,
		HealthCheckPath: "/healthz",
	}

	if !hc.checkDeployment(context.Background(), dep) {
		t.Error("expected healthy for 200 response")
	}
}

func TestCheckDeployment_UnhealthyEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	port := serverPort(t, srv)

	db := &mockDB{}
	hc := NewHealthChecker(db, zap.NewNop())

	dep := deploymentRow{
		ID:              "dep-3",
		Name:            "broken-app",
		Port:            port,
		HealthCheckPath: "/healthz",
	}

	if hc.checkDeployment(context.Background(), dep) {
		t.Error("expected unhealthy for 500 response")
	}
}

func TestCheckDeployment_UnreachableEndpoint(t *testing.T) {
	db := &mockDB{}
	hc := NewHealthChecker(db, zap.NewNop())

	dep := deploymentRow{
		ID:              "dep-4",
		Name:            "ghost-app",
		Port:            19999, // nothing listening here
		HealthCheckPath: "/healthz",
	}

	if hc.checkDeployment(context.Background(), dep) {
		t.Error("expected unhealthy for unreachable endpoint")
	}
}

// ---- c) checkConsecutiveFailures ------------------------------------------

func TestCheckConsecutiveFailures_HealthyReturnsEarly(t *testing.T) {
	db := &mockDB{}
	hc := NewHealthChecker(db, zap.NewNop())

	// When the current check is healthy, the method returns immediately
	// without querying the database.
	hc.checkConsecutiveFailures(context.Background(), "dep-1", true)

	calls := db.getQueryCalls()
	if len(calls) != 0 {
		t.Errorf("expected no query calls when healthy, got %d", len(calls))
	}
}

func TestCheckConsecutiveFailures_LessThan3Failures(t *testing.T) {
	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			// Return only 2 unhealthy rows (fewer than 3).
			appendRows(dest, []map[string]interface{}{
				{"Status": "unhealthy"},
				{"Status": "unhealthy"},
			})
			return nil
		},
	}

	hc := NewHealthChecker(db, zap.NewNop())
	hc.checkConsecutiveFailures(context.Background(), "dep-1", false)

	// Should query the DB but NOT issue any UPDATE or event INSERT.
	execCalls := db.getExecCalls()
	if len(execCalls) != 0 {
		t.Errorf("expected 0 exec calls with <3 failures, got %d", len(execCalls))
	}
}

func TestCheckConsecutiveFailures_ThreeConsecutive(t *testing.T) {
	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			appendRows(dest, []map[string]interface{}{
				{"Status": "unhealthy"},
				{"Status": "unhealthy"},
				{"Status": "unhealthy"},
			})
			return nil
		},
		execFunc: func(query string, args ...interface{}) (interface{}, error) {
			return nil, nil
		},
	}

	hc := NewHealthChecker(db, zap.NewNop())
	hc.checkConsecutiveFailures(context.Background(), "dep-99", false)

	execCalls := db.getExecCalls()

	// Expect 2 exec calls: one UPDATE (mark failed) + one INSERT (event).
	if len(execCalls) != 2 {
		t.Fatalf("expected 2 exec calls (update + event), got %d", len(execCalls))
	}

	// First call: UPDATE deployments SET status = 'failed'
	if !strings.Contains(execCalls[0].query, "UPDATE deployments") {
		t.Errorf("expected UPDATE deployments query, got: %s", execCalls[0].query)
	}
	if !strings.Contains(execCalls[0].query, "status = 'failed'") {
		t.Errorf("expected status='failed' in query, got: %s", execCalls[0].query)
	}

	// Second call: INSERT INTO deployment_events
	if !strings.Contains(execCalls[1].query, "INSERT INTO deployment_events") {
		t.Errorf("expected INSERT INTO deployment_events, got: %s", execCalls[1].query)
	}
	if !strings.Contains(execCalls[1].query, "health_failed") {
		t.Errorf("expected health_failed event_type, got: %s", execCalls[1].query)
	}

	// Verify the deployment ID was passed to both queries.
	for i, call := range execCalls {
		found := false
		for _, arg := range call.args {
			if arg == "dep-99" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("exec call %d: expected deployment id 'dep-99' in args %v", i, call.args)
		}
	}
}

func TestCheckConsecutiveFailures_MixedResults(t *testing.T) {
	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			// 3 rows but NOT all unhealthy — no action should be taken.
			appendRows(dest, []map[string]interface{}{
				{"Status": "unhealthy"},
				{"Status": "healthy"},
				{"Status": "unhealthy"},
			})
			return nil
		},
	}

	hc := NewHealthChecker(db, zap.NewNop())
	hc.checkConsecutiveFailures(context.Background(), "dep-mixed", false)

	execCalls := db.getExecCalls()
	if len(execCalls) != 0 {
		t.Errorf("expected 0 exec calls with mixed results, got %d", len(execCalls))
	}
}

// ---- d) GetHealthStatus ---------------------------------------------------

func TestGetHealthStatus_ReturnsChecks(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			appendRows(dest, []map[string]interface{}{
				{"Status": "healthy", "CheckedAt": now, "ResponseTimeMs": 42},
				{"Status": "unhealthy", "CheckedAt": now.Add(-30 * time.Second), "ResponseTimeMs": 5001},
			})
			return nil
		},
	}

	hc := NewHealthChecker(db, zap.NewNop())
	checks, err := hc.GetHealthStatus(context.Background(), "dep-1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(checks) != 2 {
		t.Fatalf("expected 2 health checks, got %d", len(checks))
	}

	if checks[0].Status != "healthy" {
		t.Errorf("checks[0].Status = %q, want %q", checks[0].Status, "healthy")
	}
	if checks[0].ResponseTimeMs != 42 {
		t.Errorf("checks[0].ResponseTimeMs = %d, want 42", checks[0].ResponseTimeMs)
	}
	if !checks[0].CheckedAt.Equal(now) {
		t.Errorf("checks[0].CheckedAt = %v, want %v", checks[0].CheckedAt, now)
	}

	if checks[1].Status != "unhealthy" {
		t.Errorf("checks[1].Status = %q, want %q", checks[1].Status, "unhealthy")
	}
	if checks[1].ResponseTimeMs != 5001 {
		t.Errorf("checks[1].ResponseTimeMs = %d, want 5001", checks[1].ResponseTimeMs)
	}
}

func TestGetHealthStatus_EmptyList(t *testing.T) {
	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			// Don't populate dest — leave the slice empty.
			return nil
		},
	}

	hc := NewHealthChecker(db, zap.NewNop())
	checks, err := hc.GetHealthStatus(context.Background(), "dep-empty", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(checks) != 0 {
		t.Errorf("expected 0 health checks, got %d", len(checks))
	}
}

func TestGetHealthStatus_DatabaseError(t *testing.T) {
	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			return fmt.Errorf("connection refused")
		},
	}

	hc := NewHealthChecker(db, zap.NewNop())
	_, err := hc.GetHealthStatus(context.Background(), "dep-err", 10)
	if err == nil {
		t.Fatal("expected error from GetHealthStatus")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected 'connection refused' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// serverPort extracts the port number from an httptest.Server.
func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	// URL is http://127.0.0.1:<port>
	addr := srv.Listener.Addr().String()
	var port int
	// addr is "127.0.0.1:PORT"
	_, err := fmt.Sscanf(addr[strings.LastIndex(addr, ":")+1:], "%d", &port)
	if err != nil {
		t.Fatalf("failed to parse port from %q: %v", addr, err)
	}
	return port
}

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

	"github.com/DeBrosOfficial/network/pkg/deployments"
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
	queryFunc  func(dest interface{}, query string, args ...interface{}) error
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
// Mock process manager
// ---------------------------------------------------------------------------

type mockProcessManager struct {
	mu           sync.Mutex
	restartCalls []string // deployment IDs
	restartErr   error
	stopCalls    []string // deployment IDs
	stopErr      error
}

func (m *mockProcessManager) Restart(_ context.Context, dep *deployments.Deployment) error {
	m.mu.Lock()
	m.restartCalls = append(m.restartCalls, dep.ID)
	m.mu.Unlock()
	return m.restartErr
}

func (m *mockProcessManager) Stop(_ context.Context, dep *deployments.Deployment) error {
	m.mu.Lock()
	m.stopCalls = append(m.stopCalls, dep.ID)
	m.mu.Unlock()
	return m.stopErr
}

func (m *mockProcessManager) getRestartCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.restartCalls))
	copy(out, m.restartCalls)
	return out
}

func (m *mockProcessManager) getStopCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.stopCalls))
	copy(out, m.stopCalls)
	return out
}

// ---------------------------------------------------------------------------
// Helper: populate a *[]T dest via reflection so the mock can return rows.
// ---------------------------------------------------------------------------

// appendRows appends rows to dest (a *[]SomeStruct) by creating new elements
// of the destination's element type and copying field values by name.
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
	pm := &mockProcessManager{}

	hc := NewHealthChecker(db, logger, "node-1", pm)

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
	if hc.nodeID != "node-1" {
		t.Errorf("expected nodeID='node-1', got %q", hc.nodeID)
	}
	if hc.processManager != pm {
		t.Error("expected processManager to be stored")
	}
	if hc.states == nil {
		t.Error("expected states map to be initialized")
	}
}

// ---- b) checkDeployment ---------------------------------------------------

func TestCheckDeployment_StaticDeployment(t *testing.T) {
	db := &mockDB{}
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)

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

	port := serverPort(t, srv)

	db := &mockDB{}
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)

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
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)

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
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)

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

// ---- c) checkAllDeployments query -----------------------------------------

func TestCheckAllDeployments_QueriesLocalReplicas(t *testing.T) {
	db := &mockDB{}
	hc := NewHealthChecker(db, zap.NewNop(), "node-abc", nil)

	hc.checkAllDeployments(context.Background())

	calls := db.getQueryCalls()
	if len(calls) == 0 {
		t.Fatal("expected at least one query call")
	}

	q := calls[0].query
	if !strings.Contains(q, "deployment_replicas") {
		t.Errorf("expected query to join deployment_replicas, got: %s", q)
	}
	if !strings.Contains(q, "dr.node_id = ?") {
		t.Errorf("expected query to filter by dr.node_id, got: %s", q)
	}
	if !strings.Contains(q, "'degraded'") {
		t.Errorf("expected query to include 'degraded' status, got: %s", q)
	}

	// Verify nodeID was passed as the bind parameter
	if len(calls[0].args) == 0 {
		t.Fatal("expected query args")
	}
	if nodeID, ok := calls[0].args[0].(string); !ok || nodeID != "node-abc" {
		t.Errorf("expected nodeID arg 'node-abc', got %v", calls[0].args[0])
	}
}

// ---- d) handleUnhealthy ---------------------------------------------------

func TestHandleUnhealthy_RestartsBeforeFailure(t *testing.T) {
	db := &mockDB{}
	pm := &mockProcessManager{}
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", pm)

	dep := deploymentRow{
		ID:              "dep-restart",
		Namespace:       "test",
		Name:            "my-app",
		Type:            "nextjs",
		Port:            10001,
		RestartPolicy:   "on-failure",
		MaxRestartCount: 3,
		ReplicaStatus:   "active",
	}

	ctx := context.Background()

	// Drive 3 consecutive unhealthy checks -> should trigger restart
	for i := 0; i < consecutiveFailuresThreshold; i++ {
		hc.handleUnhealthy(ctx, dep)
	}

	// Verify restart was called
	restarts := pm.getRestartCalls()
	if len(restarts) != 1 {
		t.Fatalf("expected 1 restart call, got %d", len(restarts))
	}
	if restarts[0] != "dep-restart" {
		t.Errorf("expected restart for 'dep-restart', got %q", restarts[0])
	}

	// Verify no replica status UPDATE was issued (only event INSERT)
	execCalls := db.getExecCalls()
	for _, call := range execCalls {
		if strings.Contains(call.query, "UPDATE deployment_replicas") {
			t.Error("should not update replica status when restart succeeds")
		}
	}
}

func TestHandleUnhealthy_MarksReplicaFailedAfterRestartLimit(t *testing.T) {
	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			// Return count of 1 active replica (so deployment becomes degraded, not failed)
			if strings.Contains(query, "COUNT(*)") {
				appendRows(dest, []map[string]interface{}{
					{"Count": 1},
				})
			}
			return nil
		},
	}
	pm := &mockProcessManager{}
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", pm)

	dep := deploymentRow{
		ID:              "dep-limited",
		Namespace:       "test",
		Name:            "my-app",
		Type:            "nextjs",
		Port:            10001,
		RestartPolicy:   "on-failure",
		MaxRestartCount: 1, // Only 1 restart allowed
		ReplicaStatus:   "active",
	}

	ctx := context.Background()

	// First 3 misses -> restart (limit=1, attempt 1)
	for i := 0; i < consecutiveFailuresThreshold; i++ {
		hc.handleUnhealthy(ctx, dep)
	}

	// Should have restarted once
	if len(pm.getRestartCalls()) != 1 {
		t.Fatalf("expected 1 restart call, got %d", len(pm.getRestartCalls()))
	}

	// Next 3 misses -> restart limit exhausted, mark replica failed
	for i := 0; i < consecutiveFailuresThreshold; i++ {
		hc.handleUnhealthy(ctx, dep)
	}

	// Verify replica was marked failed
	execCalls := db.getExecCalls()
	foundReplicaUpdate := false
	foundDeploymentUpdate := false
	for _, call := range execCalls {
		if strings.Contains(call.query, "UPDATE deployment_replicas") && strings.Contains(call.query, "'failed'") {
			foundReplicaUpdate = true
		}
		if strings.Contains(call.query, "UPDATE deployments") {
			foundDeploymentUpdate = true
		}
	}

	if !foundReplicaUpdate {
		t.Error("expected UPDATE deployment_replicas SET status = 'failed'")
	}
	if !foundDeploymentUpdate {
		t.Error("expected UPDATE deployments to recalculate status")
	}

	// Should NOT have restarted again (limit was 1)
	if len(pm.getRestartCalls()) != 1 {
		t.Errorf("expected still 1 restart call, got %d", len(pm.getRestartCalls()))
	}
}

func TestHandleUnhealthy_NeverRestart(t *testing.T) {
	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			if strings.Contains(query, "COUNT(*)") {
				appendRows(dest, []map[string]interface{}{
					{"Count": 0},
				})
			}
			return nil
		},
	}
	pm := &mockProcessManager{}
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", pm)

	dep := deploymentRow{
		ID:              "dep-never",
		Namespace:       "test",
		Name:            "no-restart-app",
		Type:            "nextjs",
		Port:            10001,
		RestartPolicy:   "never",
		MaxRestartCount: 10,
		ReplicaStatus:   "active",
	}

	ctx := context.Background()

	// 3 misses should immediately mark failed without restart
	for i := 0; i < consecutiveFailuresThreshold; i++ {
		hc.handleUnhealthy(ctx, dep)
	}

	// No restart calls
	if len(pm.getRestartCalls()) != 0 {
		t.Errorf("expected 0 restart calls with policy=never, got %d", len(pm.getRestartCalls()))
	}

	// Verify replica was marked failed
	execCalls := db.getExecCalls()
	foundReplicaUpdate := false
	for _, call := range execCalls {
		if strings.Contains(call.query, "UPDATE deployment_replicas") && strings.Contains(call.query, "'failed'") {
			foundReplicaUpdate = true
		}
	}
	if !foundReplicaUpdate {
		t.Error("expected replica to be marked failed immediately")
	}
}

// ---- e) handleHealthy -----------------------------------------------------

func TestHandleHealthy_ResetsCounters(t *testing.T) {
	db := &mockDB{}
	pm := &mockProcessManager{}
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", pm)

	dep := deploymentRow{
		ID:              "dep-reset",
		Namespace:       "test",
		Name:            "flaky-app",
		Type:            "nextjs",
		Port:            10001,
		RestartPolicy:   "on-failure",
		MaxRestartCount: 3,
		ReplicaStatus:   "active",
	}

	ctx := context.Background()

	// 2 misses (below threshold)
	hc.handleUnhealthy(ctx, dep)
	hc.handleUnhealthy(ctx, dep)

	// Health recovered
	hc.handleHealthy(ctx, dep)

	// 2 more misses — should NOT trigger restart (counters were reset)
	hc.handleUnhealthy(ctx, dep)
	hc.handleUnhealthy(ctx, dep)

	if len(pm.getRestartCalls()) != 0 {
		t.Errorf("expected 0 restart calls after counter reset, got %d", len(pm.getRestartCalls()))
	}
}

func TestHandleHealthy_RecoversFailedReplica(t *testing.T) {
	callCount := 0
	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			if strings.Contains(query, "COUNT(*)") {
				callCount++
				if callCount == 1 {
					// First COUNT: over-replication check — 1 active (under-replicated, allow recovery)
					appendRows(dest, []map[string]interface{}{{"Count": 1}})
				} else {
					// Second COUNT: recalculateDeploymentStatus — now 2 active after recovery
					appendRows(dest, []map[string]interface{}{{"Count": 2}})
				}
			}
			return nil
		},
	}
	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)

	dep := deploymentRow{
		ID:            "dep-recover",
		Namespace:     "test",
		Name:          "recovered-app",
		ReplicaStatus: "failed", // Was failed, now passing health check
	}

	ctx := context.Background()
	hc.handleHealthy(ctx, dep)

	// Verify replica was updated back to 'active'
	execCalls := db.getExecCalls()
	foundReplicaRecovery := false
	foundEvent := false
	for _, call := range execCalls {
		if strings.Contains(call.query, "UPDATE deployment_replicas") && strings.Contains(call.query, "'active'") {
			foundReplicaRecovery = true
		}
		if strings.Contains(call.query, "replica_recovered") {
			foundEvent = true
		}
	}
	if !foundReplicaRecovery {
		t.Error("expected UPDATE deployment_replicas SET status = 'active'")
	}
	if !foundEvent {
		t.Error("expected replica_recovered event")
	}
}

func TestHandleHealthy_StopsZombieReplicaWhenAlreadyReplaced(t *testing.T) {
	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			if strings.Contains(query, "COUNT(*)") {
				// 2 active replicas already exist — this replica was replaced
				appendRows(dest, []map[string]interface{}{{"Count": 2}})
			}
			return nil
		},
	}
	pm := &mockProcessManager{}
	hc := NewHealthChecker(db, zap.NewNop(), "node-zombie", pm)

	dep := deploymentRow{
		ID:            "dep-zombie",
		Namespace:     "test",
		Name:          "zombie-app",
		Type:          "nextjs",
		Port:          10001,
		ReplicaStatus: "failed", // Was failed, but process is running (systemd Restart=always)
	}

	ctx := context.Background()
	hc.handleHealthy(ctx, dep)

	// Verify Stop was called (not Restart)
	stopCalls := pm.getStopCalls()
	if len(stopCalls) != 1 {
		t.Fatalf("expected 1 Stop call, got %d", len(stopCalls))
	}
	if stopCalls[0] != "dep-zombie" {
		t.Errorf("expected Stop for 'dep-zombie', got %q", stopCalls[0])
	}

	// Verify replica row was DELETED (not updated to active)
	execCalls := db.getExecCalls()
	foundDelete := false
	foundZombieEvent := false
	for _, call := range execCalls {
		if strings.Contains(call.query, "DELETE FROM deployment_replicas") {
			foundDelete = true
			// Verify the right deployment and node
			if len(call.args) >= 2 {
				if call.args[0] != "dep-zombie" || call.args[1] != "node-zombie" {
					t.Errorf("DELETE args: got (%v, %v), want (dep-zombie, node-zombie)", call.args[0], call.args[1])
				}
			}
		}
		if strings.Contains(call.query, "zombie_replica_stopped") {
			foundZombieEvent = true
		}
		// Should NOT recover to active
		if strings.Contains(call.query, "UPDATE deployment_replicas") && strings.Contains(call.query, "'active'") {
			t.Error("should NOT update replica to active when it's a zombie")
		}
	}
	if !foundDelete {
		t.Error("expected DELETE FROM deployment_replicas for zombie replica")
	}
	if !foundZombieEvent {
		t.Error("expected zombie_replica_stopped event")
	}

	// Verify no Restart calls
	if len(pm.getRestartCalls()) != 0 {
		t.Errorf("expected 0 restart calls, got %d", len(pm.getRestartCalls()))
	}
}

// ---- f) recordHealthCheck -------------------------------------------------

func TestRecordHealthCheck_IncludesNodeID(t *testing.T) {
	db := &mockDB{}
	hc := NewHealthChecker(db, zap.NewNop(), "node-xyz", nil)

	hc.recordHealthCheck(context.Background(), "dep-1", true)

	execCalls := db.getExecCalls()
	if len(execCalls) != 1 {
		t.Fatalf("expected 1 exec call, got %d", len(execCalls))
	}

	q := execCalls[0].query
	if !strings.Contains(q, "node_id") {
		t.Errorf("expected INSERT to include node_id column, got: %s", q)
	}

	// Verify node_id is the second arg (after deployment_id)
	if len(execCalls[0].args) < 2 {
		t.Fatal("expected at least 2 args")
	}
	if nodeID, ok := execCalls[0].args[1].(string); !ok || nodeID != "node-xyz" {
		t.Errorf("expected node_id arg 'node-xyz', got %v", execCalls[0].args[1])
	}
}

// ---- g) GetHealthStatus ---------------------------------------------------

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

	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)
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
}

func TestGetHealthStatus_EmptyList(t *testing.T) {
	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			return nil
		},
	}

	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)
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

	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)
	_, err := hc.GetHealthStatus(context.Background(), "dep-err", 10)
	if err == nil {
		t.Fatal("expected error from GetHealthStatus")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected 'connection refused' in error, got: %v", err)
	}
}

// ---- h) reconcileDeployments ----------------------------------------------

type mockReconciler struct {
	mu               sync.Mutex
	selectCalls      []string // primaryNodeIDs
	selectResult     []string
	selectErr        error
	updateStatusCalls []struct {
		deploymentID string
		nodeID       string
		status       deployments.ReplicaStatus
	}
}

func (m *mockReconciler) SelectReplicaNodes(_ context.Context, primaryNodeID string, _ int) ([]string, error) {
	m.mu.Lock()
	m.selectCalls = append(m.selectCalls, primaryNodeID)
	m.mu.Unlock()
	return m.selectResult, m.selectErr
}

func (m *mockReconciler) UpdateReplicaStatus(_ context.Context, deploymentID, nodeID string, status deployments.ReplicaStatus) error {
	m.mu.Lock()
	m.updateStatusCalls = append(m.updateStatusCalls, struct {
		deploymentID string
		nodeID       string
		status       deployments.ReplicaStatus
	}{deploymentID, nodeID, status})
	m.mu.Unlock()
	return nil
}

type mockProvisioner struct {
	mu        sync.Mutex
	setupCalls []struct {
		deploymentID string
		nodeID       string
	}
}

func (m *mockProvisioner) SetupDynamicReplica(_ context.Context, dep *deployments.Deployment, nodeID string) {
	m.mu.Lock()
	m.setupCalls = append(m.setupCalls, struct {
		deploymentID string
		nodeID       string
	}{dep.ID, nodeID})
	m.mu.Unlock()
}

func TestReconcileDeployments_UnderReplicated(t *testing.T) {
	// Start a mock RQLite status endpoint that reports Leader
	leaderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"store":{"raft":{"state":"Leader"}}}`))
	}))
	defer leaderSrv.Close()

	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			if strings.Contains(query, "active_replicas") {
				appendRows(dest, []map[string]interface{}{
					{
						"ID":              "dep-under",
						"Namespace":       "test",
						"Name":            "under-app",
						"Type":            "nextjs",
						"HomeNodeID":      "node-home",
						"ContentCID":      "cid-123",
						"BuildCID":        "",
						"Environment":     "",
						"Port":            10001,
						"HealthCheckPath": "/health",
						"MemoryLimitMB":   256,
						"CPULimitPercent": 50,
						"RestartPolicy":   "on-failure",
						"MaxRestartCount": 10,
						"ActiveReplicas":  1, // Under-replicated (desired=2)
					},
				})
			}
			return nil
		},
	}

	rc := &mockReconciler{selectResult: []string{"node-new"}}
	rp := &mockProvisioner{}

	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)
	hc.SetReconciler(leaderSrv.URL, rc, rp)

	hc.reconcileDeployments(context.Background())

	// Wait briefly for the goroutine to fire
	time.Sleep(50 * time.Millisecond)

	// Verify SelectReplicaNodes was called
	rc.mu.Lock()
	selectCount := len(rc.selectCalls)
	rc.mu.Unlock()
	if selectCount != 1 {
		t.Fatalf("expected 1 SelectReplicaNodes call, got %d", selectCount)
	}

	// Verify SetupDynamicReplica was called
	rp.mu.Lock()
	setupCount := len(rp.setupCalls)
	rp.mu.Unlock()
	if setupCount != 1 {
		t.Fatalf("expected 1 SetupDynamicReplica call, got %d", setupCount)
	}
	rp.mu.Lock()
	if rp.setupCalls[0].deploymentID != "dep-under" {
		t.Errorf("expected deployment 'dep-under', got %q", rp.setupCalls[0].deploymentID)
	}
	if rp.setupCalls[0].nodeID != "node-new" {
		t.Errorf("expected node 'node-new', got %q", rp.setupCalls[0].nodeID)
	}
	rp.mu.Unlock()
}

func TestReconcileDeployments_FullyReplicated(t *testing.T) {
	leaderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"store":{"raft":{"state":"Leader"}}}`))
	}))
	defer leaderSrv.Close()

	db := &mockDB{
		queryFunc: func(dest interface{}, query string, args ...interface{}) error {
			if strings.Contains(query, "active_replicas") {
				appendRows(dest, []map[string]interface{}{
					{
						"ID":              "dep-full",
						"Namespace":       "test",
						"Name":            "full-app",
						"Type":            "nextjs",
						"HomeNodeID":      "node-home",
						"ContentCID":      "cid-456",
						"BuildCID":        "",
						"Environment":     "",
						"Port":            10002,
						"HealthCheckPath": "/health",
						"MemoryLimitMB":   256,
						"CPULimitPercent": 50,
						"RestartPolicy":   "on-failure",
						"MaxRestartCount": 10,
						"ActiveReplicas":  2, // Fully replicated
					},
				})
			}
			return nil
		},
	}

	rc := &mockReconciler{selectResult: []string{"node-new"}}
	rp := &mockProvisioner{}

	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)
	hc.SetReconciler(leaderSrv.URL, rc, rp)

	hc.reconcileDeployments(context.Background())

	time.Sleep(50 * time.Millisecond)

	// Should NOT trigger re-replication
	rc.mu.Lock()
	if len(rc.selectCalls) != 0 {
		t.Errorf("expected 0 SelectReplicaNodes calls for fully replicated deployment, got %d", len(rc.selectCalls))
	}
	rc.mu.Unlock()
}

func TestReconcileDeployments_NotLeader(t *testing.T) {
	// Not-leader RQLite status
	followerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"store":{"raft":{"state":"Follower"}}}`))
	}))
	defer followerSrv.Close()

	db := &mockDB{}
	rc := &mockReconciler{}
	rp := &mockProvisioner{}

	hc := NewHealthChecker(db, zap.NewNop(), "node-1", nil)
	hc.SetReconciler(followerSrv.URL, rc, rp)

	hc.reconcileDeployments(context.Background())

	// Should not query deployments at all
	calls := db.getQueryCalls()
	if len(calls) != 0 {
		t.Errorf("expected 0 query calls on follower, got %d", len(calls))
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// serverPort extracts the port number from an httptest.Server.
func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	addr := srv.Listener.Addr().String()
	var port int
	_, err := fmt.Sscanf(addr[strings.LastIndex(addr, ":")+1:], "%d", &port)
	if err != nil {
		t.Fatalf("failed to parse port from %q: %v", addr, err)
	}
	return port
}

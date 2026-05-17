package namespace

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Mock DB with callback support for cluster recovery tests
// ---------------------------------------------------------------------------

// recoveryMockDB implements rqlite.Client with configurable query/exec callbacks.
type recoveryMockDB struct {
	mu         sync.Mutex
	queryFunc  func(dest any, query string, args ...any) error
	execFunc   func(query string, args ...any) error
	queryCalls []mockQueryCall
	execCalls  []mockExecCall
}

func (m *recoveryMockDB) Query(_ context.Context, dest any, query string, args ...any) error {
	m.mu.Lock()
	ifaceArgs := make([]interface{}, len(args))
	for i, a := range args {
		ifaceArgs[i] = a
	}
	m.queryCalls = append(m.queryCalls, mockQueryCall{Query: query, Args: ifaceArgs})
	fn := m.queryFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(dest, query, args...)
	}
	return nil
}

func (m *recoveryMockDB) Exec(_ context.Context, query string, args ...any) (sql.Result, error) {
	m.mu.Lock()
	ifaceArgs := make([]interface{}, len(args))
	for i, a := range args {
		ifaceArgs[i] = a
	}
	m.execCalls = append(m.execCalls, mockExecCall{Query: query, Args: ifaceArgs})
	fn := m.execFunc
	m.mu.Unlock()

	if fn != nil {
		if err := fn(query, args...); err != nil {
			return nil, err
		}
	}
	return mockResult{rowsAffected: 1}, nil
}

func (m *recoveryMockDB) FindBy(_ context.Context, _ any, _ string, _ map[string]any, _ ...rqlite.FindOption) error {
	return nil
}
func (m *recoveryMockDB) FindOneBy(_ context.Context, _ any, _ string, _ map[string]any, _ ...rqlite.FindOption) error {
	return nil
}
func (m *recoveryMockDB) Save(_ context.Context, _ any) error   { return nil }
func (m *recoveryMockDB) Remove(_ context.Context, _ any) error { return nil }
func (m *recoveryMockDB) Repository(_ string) any               { return nil }
func (m *recoveryMockDB) CreateQueryBuilder(_ string) *rqlite.QueryBuilder {
	return nil
}
func (m *recoveryMockDB) Tx(_ context.Context, fn func(tx rqlite.Tx) error) error { return nil }
func (m *recoveryMockDB) Batch(_ context.Context, ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
	return &rqlite.BatchResult{Committed: true, Results: make([]rqlite.OpResult, len(ops))}, nil
}
func (m *recoveryMockDB) BatchWithSeq(_ context.Context, _ string, ops []rqlite.BatchOp) (*rqlite.BatchResult, int64, error) {
	res, _ := m.Batch(context.Background(), ops)
	return res, 1, nil
}
func (m *recoveryMockDB) BatchQuery(_ context.Context, ops []rqlite.BatchOp) ([]rqlite.OpResult, error) {
	out := make([]rqlite.OpResult, len(ops))
	for i := range ops {
		out[i] = rqlite.OpResult{Kind: rqlite.BatchOpQuery}
	}
	return out, nil
}

var _ rqlite.Client = (*recoveryMockDB)(nil)

func (m *recoveryMockDB) getExecCalls() []mockExecCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]mockExecCall, len(m.execCalls))
	copy(cp, m.execCalls)
	return cp
}

func (m *recoveryMockDB) getQueryCalls() []mockQueryCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]mockQueryCall, len(m.queryCalls))
	copy(cp, m.queryCalls)
	return cp
}

// appendToSlice creates a new element of the slice's element type, sets named
// fields using the provided map (keyed by struct field name), and appends it.
// This works with locally-defined types whose names are not accessible at compile time.
func appendToSlice(dest any, fields map[string]any) {
	sliceVal := reflect.ValueOf(dest).Elem()
	elemType := sliceVal.Type().Elem()
	newElem := reflect.New(elemType).Elem()
	for name, val := range fields {
		f := newElem.FieldByName(name)
		if f.IsValid() && f.CanSet() {
			f.Set(reflect.ValueOf(val))
		}
	}
	sliceVal.Set(reflect.Append(sliceVal, newElem))
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestMarkDeadNodeReplicasFailed_MarksReplicasAndDegradesDeploy(t *testing.T) {
	// Scenario: node "dead-node" has 1 active replica for deployment "dep-1".
	// Another replica on a healthy node remains active.
	// Expected: replica marked failed, deployment set to 'degraded'.
	db := &recoveryMockDB{}

	db.queryFunc = func(dest any, query string, args ...any) error {
		if strings.Contains(query, "DISTINCT deployment_id") {
			appendToSlice(dest, map[string]any{"DeploymentID": "dep-1"})
			return nil
		}
		if strings.Contains(query, "COUNT(*)") {
			// One active replica remaining on a healthy node.
			appendToSlice(dest, map[string]any{"Count": 1})
			return nil
		}
		return nil
	}

	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cm.markDeadNodeReplicasFailed(context.Background(), "dead-node")

	execCalls := db.getExecCalls()

	// Should have: 1 UPDATE replicas + 1 UPDATE deployment status + 1 INSERT event = 3
	if len(execCalls) != 3 {
		t.Fatalf("expected 3 exec calls, got %d: %+v", len(execCalls), execCalls)
	}

	// First exec: mark replicas failed.
	if !strings.Contains(execCalls[0].Query, "UPDATE deployment_replicas") {
		t.Errorf("first exec should update replicas, got: %s", execCalls[0].Query)
	}
	if execCalls[0].Args[0] != "dead-node" {
		t.Errorf("expected dead-node arg, got: %v", execCalls[0].Args[0])
	}

	// Second exec: set deployment to degraded (not failed, since 1 replica remains).
	if !strings.Contains(execCalls[1].Query, "status = 'degraded'") {
		t.Errorf("expected degraded status update, got: %s", execCalls[1].Query)
	}

	// Third exec: deployment event log.
	if !strings.Contains(execCalls[2].Query, "deployment_events") {
		t.Errorf("expected event INSERT, got: %s", execCalls[2].Query)
	}
	if !strings.Contains(execCalls[2].Args[1].(string), "1 active replicas remaining") {
		t.Errorf("event message should mention remaining replicas, got: %s", execCalls[2].Args[1])
	}
}

func TestMarkDeadNodeReplicasFailed_AllReplicasDead_SetsFailed(t *testing.T) {
	// Scenario: node "dead-node" has the only replica for "dep-2".
	// Expected: replica marked failed, deployment set to 'failed'.
	db := &recoveryMockDB{}

	db.queryFunc = func(dest any, query string, args ...any) error {
		if strings.Contains(query, "DISTINCT deployment_id") {
			appendToSlice(dest, map[string]any{"DeploymentID": "dep-2"})
			return nil
		}
		if strings.Contains(query, "COUNT(*)") {
			// Zero active replicas remaining.
			appendToSlice(dest, map[string]any{"Count": 0})
			return nil
		}
		return nil
	}

	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cm.markDeadNodeReplicasFailed(context.Background(), "dead-node")

	execCalls := db.getExecCalls()

	if len(execCalls) != 3 {
		t.Fatalf("expected 3 exec calls, got %d: %+v", len(execCalls), execCalls)
	}

	// Second exec: set deployment to failed (not degraded).
	if !strings.Contains(execCalls[1].Query, "status = 'failed'") {
		t.Errorf("expected failed status update, got: %s", execCalls[1].Query)
	}
}

func TestMarkDeadNodeReplicasFailed_NoReplicas_ReturnsEarly(t *testing.T) {
	// Scenario: dead node has no deployment replicas.
	// Expected: no exec calls at all.
	db := &recoveryMockDB{}

	db.queryFunc = func(dest any, query string, args ...any) error {
		// Return empty slice for all queries.
		return nil
	}

	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cm.markDeadNodeReplicasFailed(context.Background(), "dead-node")

	execCalls := db.getExecCalls()
	if len(execCalls) != 0 {
		t.Errorf("expected 0 exec calls when no replicas, got %d", len(execCalls))
	}
}

func TestMarkDeadNodeReplicasFailed_MultipleDeployments(t *testing.T) {
	// Scenario: dead node has replicas for 2 deployments.
	// dep-1: has another healthy replica (degraded).
	// dep-2: only replica was on dead node (failed).
	db := &recoveryMockDB{}
	countCallIdx := 0

	db.queryFunc = func(dest any, query string, args ...any) error {
		if strings.Contains(query, "DISTINCT deployment_id") {
			appendToSlice(dest, map[string]any{"DeploymentID": "dep-1"})
			appendToSlice(dest, map[string]any{"DeploymentID": "dep-2"})
			return nil
		}
		if strings.Contains(query, "COUNT(*)") {
			// First deployment has 1 remaining, second has 0.
			counts := []int{1, 0}
			appendToSlice(dest, map[string]any{"Count": counts[countCallIdx]})
			countCallIdx++
			return nil
		}
		return nil
	}

	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cm.markDeadNodeReplicasFailed(context.Background(), "dead-node")

	execCalls := db.getExecCalls()

	// 1 mark-all-failed + 2*(status update + event) = 5
	if len(execCalls) != 5 {
		t.Fatalf("expected 5 exec calls, got %d: %+v", len(execCalls), execCalls)
	}

	// dep-1: degraded
	if !strings.Contains(execCalls[1].Query, "status = 'degraded'") {
		t.Errorf("dep-1 should be degraded, got: %s", execCalls[1].Query)
	}

	// dep-2: failed
	if !strings.Contains(execCalls[3].Query, "status = 'failed'") {
		t.Errorf("dep-2 should be failed, got: %s", execCalls[3].Query)
	}
}

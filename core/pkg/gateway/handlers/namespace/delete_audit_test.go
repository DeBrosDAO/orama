package namespace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// recordingDB is the registry database an audit log writes to, readable by a test.
type recordingDB struct {
	client.DatabaseClient
	mu   sync.Mutex
	rows [][]interface{}
}

func (d *recordingDB) Query(_ context.Context, _ string, args ...interface{}) (*client.QueryResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rows = append(d.rows, args)
	return &client.QueryResult{Count: 1}, nil
}

func newRecordingAudit() (*auth.AuditLog, *recordingDB) {
	db := &recordingDB{}
	return auth.NewAuditLog(func() client.DatabaseClient { return db }, nil), db
}

type failingDeprov struct{}

func (failingDeprov) DeprovisionCluster(context.Context, int64) error {
	return errors.New("dial tcp 10.0.0.9:4001: rqlite driver detail")
}

func assertAuditResult(t *testing.T, db *recordingDB, action, result string) {
	t.Helper()
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, row := range db.rows {
		var hasAction, hasResult bool
		for _, a := range row {
			hasAction = hasAction || a == action
			hasResult = hasResult || a == result
		}
		if hasAction && hasResult {
			return
		}
	}
	t.Fatalf("no audit row with action %q and result %q among %v", action, result, db.rows)
}

// The marker is removed after the namespace row, so a delete that fails after
// the references were released leaves the namespace with its marker, and no
// other namespace's unpin is refused because of it.
//
// Mutation check: remove the marker in unpinNamespaceContent again and the
// marker assertion fails.
func TestServeHTTP_failureAfterTheReleaseKeepsTheMarker(t *testing.T) {
	db := migratedDB(t)
	audit, arows := newRecordingAudit()
	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), &unpinRecorder{}, audit, zap.NewNop())
	seedNamespaces(t, h, "gone", "other")
	addRefs(t, h, "gone", "QmA")
	for _, ns := range []string{"gone", "other"} {
		if err := h.refs.Register(context.Background(), "", ns, "backfilled"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DROP TABLE api_keys`); err != nil { // deleteNamespaceRows fails
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("gone"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'gone'`); n != 1 {
		t.Fatal("the namespace row was deleted")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'gone' AND kind = 'backfilled'`); n != 1 {
		t.Fatal("the namespace still exists but its backfill marker is gone")
	}
	if err := h.refs.CheckReady(context.Background(), "other"); err != nil {
		t.Fatalf("another namespace's readiness check fails: %v", err)
	}
	if strings.Contains(rec.Body.String(), "api_keys") {
		t.Fatalf("the response carries database text: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"retryable":true`) {
		t.Fatalf("the failure is not marked retryable: %s", rec.Body.String())
	}
	assertAuditResult(t, arows, auth.AuditNamespaceDeleted, auth.AuditFailure)
}

// A failed deprovision answers a fixed message, not the driver's text, and
// leaves a failure audit row.
func TestServeHTTP_deprovisionFailureIsAuditedAndAnswersAFixedMessage(t *testing.T) {
	db := migratedDB(t)
	audit, arows := newRecordingAudit()
	h := NewDeleteHandler(failingDeprov{}, rqlite.NewClient(db), nil, audit, zap.NewNop())
	seedNamespaces(t, h, "gone")
	if err := h.refs.Register(context.Background(), "", "gone", "backfilled"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("gone"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "rqlite") || strings.Contains(rec.Body.String(), "10.0.0.9") {
		t.Fatalf("the response leaks the underlying error: %s", rec.Body.String())
	}
	assertAuditResult(t, arows, auth.AuditNamespaceDeleted, auth.AuditFailure)
}

// A success is still audited as one, and a refusal before the cluster is
// touched is not audited as a failure of a removal that never began.
func TestServeHTTP_auditsSuccessAndNotAnEarlyRefusal(t *testing.T) {
	db := migratedDB(t)
	audit, arows := newRecordingAudit()
	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), nil, audit, zap.NewNop())
	seedNamespaces(t, h, "gone", "other") // "other" has no marker: refused up front
	if err := h.refs.Register(context.Background(), "", "gone", "backfilled"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("gone"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rec.Code)
	}
	if len(arows.rows) != 0 {
		t.Fatalf("a refusal before the cluster was touched was audited: %v", arows.rows)
	}

	if err := h.refs.Register(context.Background(), "", "other", "backfilled"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("gone"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	assertAuditResult(t, arows, auth.AuditNamespaceDeleted, auth.AuditSuccess)
}

// The operator's removal shares remove(): its failure audit carries its own
// action and metadata.
func TestOperatorRemove_failureIsAuditedWithItsActionAndMetadata(t *testing.T) {
	db := migratedDB(t)
	audit, arows := newRecordingAudit()
	h := NewDeleteHandler(failingDeprov{}, rqlite.NewClient(db), nil, audit, zap.NewNop())
	seedNamespaces(t, h, "gone")
	if err := h.refs.Register(context.Background(), "", "gone", "backfilled"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.remove(rec, deleteRequest("gone"), "gone", auth.AuditNamespaceRemovedByOperator, map[string]string{"operator": "0xop", "reason": "orphan"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	assertAuditResult(t, arows, auth.AuditNamespaceRemovedByOperator, auth.AuditFailure)
	found := false
	for _, row := range arows.rows {
		for _, a := range row {
			if s, ok := a.(string); ok && strings.Contains(s, "0xop") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the failure row lost the operator metadata")
	}
}

package namespace

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// clusterInsertingProvisioner records a cluster row, as ProvisionNamespaceCluster
// does before it can fail on anything later.
type clusterInsertingProvisioner struct {
	db  *sql.DB
	err error
}

func (p clusterInsertingProvisioner) ProvisionNamespaceCluster(_ context.Context, id int, namespace, _ string) (string, string, error) {
	if _, err := p.db.Exec(`INSERT INTO namespace_clusters (id, namespace_id, namespace_name, status, provisioned_by)
		VALUES ('c-new', ?, ?, 'provisioning', 'test')`, id, namespace); err != nil {
		return "", "", err
	}
	return "", "", p.err
}

func openCreateDB(t *testing.T) *sql.DB {
	t.Helper()
	db := migratedDB(t)
	if _, err := db.Exec(`INSERT OR REPLACE INTO cluster_settings(key, value, updated_by) VALUES (?, ?, 'test')`,
		operator.SettingNamespaceCreation, operator.CreationOpen); err != nil {
		t.Fatal(err)
	}
	return db
}

func ownedGrants(t *testing.T, db *sql.DB) int {
	t.Helper()
	return countRows(t, db, `SELECT COUNT(*) FROM grants g JOIN principals p ON p.id = g.principal_id
		WHERE p.identifier = '0xowner' AND g.role = 'owner' AND g.revoked_at IS NULL`)
}

// On the real schema: a create whose provisioning does not start leaves no
// namespace row and no owner grant, so the wallet's cap is not consumed and the
// name can be used again.
func TestCreate_provisioningFailureLeavesNoRowsAndFreesTheName(t *testing.T) {
	db := openCreateDB(t)
	h := NewCreateHandler(rqlite.NewClient(db), &recordingProvisioner{err: errString("no capacity")}, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'myapp'`); n != 0 {
		t.Error("the namespace row was left behind")
	}
	if n := ownedGrants(t, db); n != 0 {
		t.Errorf("owner grants = %d, want 0", n)
	}

	h = NewCreateHandler(rqlite.NewClient(db), &recordingProvisioner{}, nil, zap.NewNop())
	w = httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("the name could not be used again: %d %s", w.Code, w.Body.String())
	}
}

// Provisioning that failed after it recorded a cluster is a cluster the owner's
// delete deprovisions. The undo must not drop the namespace from under it.
func TestCreate_provisioningFailureKeepsANamespaceThatHasACluster(t *testing.T) {
	db := openCreateDB(t)
	h := NewCreateHandler(rqlite.NewClient(db), clusterInsertingProvisioner{db: db, err: errString("lost the reply")}, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "delete namespace myapp") {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'myapp'`); n != 1 {
		t.Error("the namespace of a recorded cluster was removed")
	}
	if n := ownedGrants(t, db); n != 1 {
		t.Errorf("owner grants = %d, want the owner kept", n)
	}
}

// An undo that cannot be done is reported, not swallowed: the namespace stays
// and the answer says how to clear it.
func TestCreate_failedUndoIsReported(t *testing.T) {
	db := openCreateDB(t)
	h := NewCreateHandler(rqlite.NewClient(db), &recordingProvisioner{err: errString("no capacity")}, nil, zap.NewNop())
	if _, err := db.Exec(`CREATE TRIGGER keep_grants BEFORE DELETE ON grants BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "no capacity") ||
		!strings.Contains(w.Body.String(), "delete namespace myapp") {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'myapp'`); n != 1 {
		t.Error("the namespace row went although its grant could not be removed")
	}
}

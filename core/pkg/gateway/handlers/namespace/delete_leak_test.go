package namespace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	namespacepkg "github.com/DeBrosOfficial/network/pkg/namespace"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

type erroringDeprov struct{ err error }

func (e erroringDeprov) DeprovisionCluster(context.Context, int64) error { return e.err }

// seedOwned writes a namespace with its owner grant and, when withCluster, a
// cluster row, then returns the handler over that registry.
func seedOwned(t *testing.T, dp NamespaceDeprovisioner, withCluster bool) (*DeleteHandler, *sql.DB) {
	t.Helper()
	db := migratedDB(t)
	h := NewDeleteHandler(dp, rqlite.NewClient(db), nil, nil, zap.NewNop())
	// The migrations seed "default", whose gateway has loaded.
	if err := h.refs.Register(context.Background(), "", "default", "backfilled"); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO namespaces(id, name) VALUES (100, 'leaky')`,
		`INSERT INTO principals(id, type, identifier, created_by) VALUES (900, 'wallet', '0xowner', '0xowner')`,
		`INSERT INTO grants(principal_id, namespace_id, role, created_by) VALUES (900, 100, 'owner', '0xowner')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if withCluster {
		if _, err := db.Exec(`INSERT INTO namespace_clusters (id, namespace_id, namespace_name, status, provisioned_by)
			VALUES ('c-100', 100, 'leaky', 'ready', 'test')`); err != nil {
			t.Fatal(err)
		}
	}
	return h, db
}

func ownedByWallet(t *testing.T, db *sql.DB) int {
	t.Helper()
	return countRows(t, db, `SELECT COUNT(*) FROM grants g JOIN principals p ON p.id = g.principal_id
		WHERE p.identifier = '0xowner' AND g.role = 'owner' AND g.revoked_at IS NULL`)
}

// The leak: a node that did not confirm its teardown made DeprovisionCluster
// return an error after it had deleted the cluster row, the delete answered 500
// and stopped, and the namespace row and the owner grant stayed with no cluster,
// counted against the wallet's cap. The delete finishes instead.
//
// Mutation check: make the handler fail on every DeprovisionCluster error and
// this fails.
func TestDelete_unconfirmedTeardownStillRemovesTheNamespaceAndItsGrant(t *testing.T) {
	err := fmt.Errorf("%w: node1: timeout", namespacepkg.ErrTeardownIncomplete)
	h, db := seedOwned(t, erroringDeprov{err}, false)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("leaky"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"cleanup_pending":true`) {
		t.Errorf("the response does not say a teardown is still owed: %s", rec.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE id = 100`); n != 0 {
		t.Error("the namespace row stayed")
	}
	if n := ownedByWallet(t, db); n != 0 {
		t.Errorf("the wallet still owns %d namespaces: the cap is not freed", n)
	}
}

// A plain deprovision failure leaves the cluster row in place, so the retry
// finds the cluster: the namespace must stay until then.
func TestDelete_plainDeprovisionFailureKeepsTheNamespaceForTheRetry(t *testing.T) {
	h, db := seedOwned(t, erroringDeprov{errors.New("registry unreachable")}, true)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("leaky"))

	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"retryable":true`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE id = 100`); n != 1 {
		t.Error("the namespace was removed while its cluster is still registered")
	}
	if n := ownedByWallet(t, db); n != 1 {
		t.Errorf("owner grants = %d, want 1", n)
	}
}

// The cleanup path for rows that already leaked: the owner's delete of a
// namespace that has no cluster removes the row, the grant and its other
// per-namespace rows, and answers 200.
func TestDelete_namespaceWithoutAClusterIsRemovedByTheOwnersDelete(t *testing.T) {
	h, db := seedOwned(t, stubDeprov{}, false)
	for _, stmt := range []string{
		`INSERT INTO api_keys(id, key, namespace_id, scopes, expires_at) VALUES (1, 'ak_x:leaky', 100, '', datetime('now','+90 days'))`,
		`INSERT INTO audit_events(namespace, actor, action, result) VALUES ('leaky', '0xowner', 'namespace.created', 'success')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("leaky"))

	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "cleanup_pending") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM namespaces WHERE id = 100`,
		`SELECT COUNT(*) FROM grants WHERE namespace_id = 100`,
		`SELECT COUNT(*) FROM api_keys WHERE namespace_id = 100`,
		`SELECT COUNT(*) FROM audit_events WHERE namespace = 'leaky'`,
	} {
		if n := countRows(t, db, q); n != 0 {
			t.Errorf("%s = %d, want 0", q, n)
		}
	}
}

// Deleting twice is not an error worth a 500: the second finds nothing.
func TestDelete_secondDeleteOfARemovedNamespaceIsNotFound(t *testing.T) {
	h, _ := seedOwned(t, stubDeprov{}, false)
	for i, want := range []int{http.StatusOK, http.StatusNotFound} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, deleteRequest("leaky"))
		if rec.Code != want {
			t.Fatalf("delete %d: status %d, want %d: %s", i+1, rec.Code, want, rec.Body.String())
		}
	}
}

func TestWithTeardownPending_doesNotModifyTheCallersMetadata(t *testing.T) {
	in := map[string]string{"by": "operator"}
	out := withTeardownPending(in)
	if _, ok := in["teardown"]; ok {
		t.Error("the caller's map was modified")
	}
	if out["teardown"] != "pending" || out["by"] != "operator" {
		t.Errorf("out = %v", out)
	}
	if got := withTeardownPending(nil); got["teardown"] != "pending" {
		t.Errorf("nil metadata: %v", got)
	}
}

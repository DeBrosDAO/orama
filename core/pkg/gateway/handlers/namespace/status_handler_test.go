package namespace

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	ns "github.com/DeBrosOfficial/network/pkg/namespace"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// statusHandlerOn is a StatusHandler on db, with the log it writes to.
func statusHandlerOn(db *sql.DB) (*StatusHandler, *observer.ObservedLogs) {
	core, logs := observer.New(zap.ErrorLevel)
	cm := ns.NewClusterManager(rqlite.NewClient(db), ns.ClusterManagerConfig{}, zap.NewNop())
	return &StatusHandler{clusterManager: cm, logger: zap.New(core)}, logs
}

func seedCluster(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO namespace_clusters (id, namespace_id, namespace_name, status, provisioned_by, error_message)
		VALUES (?, 1, ?, 'ready', '0xaaaa', '')`, id, name); err != nil {
		t.Fatal(err)
	}
}

func statusRequest(_ *StatusHandler, target string, handle func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	handle(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

const registryDetail = "no such table"

func assertNoInternals(t *testing.T, body string) {
	t.Helper()
	for _, leak := range []string{registryDetail, "namespace_cluster_nodes", "namespace_clusters", "sqlite", "SELECT"} {
		if strings.Contains(body, leak) {
			t.Errorf("the response gives the client %q: %s", leak, body)
		}
	}
}

func TestStatusHandle_missingClusterIs404(t *testing.T) {
	h, logs := statusHandlerOn(migratedDB(t))
	w := statusRequest(h, "/v1/namespace/status?id=nope", h.Handle)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "cluster not found") {
		t.Fatalf("missing cluster: %d %s", w.Code, w.Body.String())
	}
	if logs.Len() != 0 {
		t.Errorf("a missing cluster is not a server error, but it was logged: %v", logs.All())
	}
}

// The nodes of the cluster could not be read: the cluster is there, and the
// answer was a 404 saying it was not.
func TestStatusHandle_nodeReadFailureIs503AndIsLogged(t *testing.T) {
	db := migratedDB(t)
	seedCluster(t, db, "c1", "acme")
	if _, err := db.Exec(`DROP TABLE namespace_cluster_nodes`); err != nil {
		t.Fatal(err)
	}
	h, logs := statusHandlerOn(db)
	w := statusRequest(h, "/v1/namespace/status?id=c1", h.Handle)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "retry") {
		t.Fatalf("node read failure: %d %s", w.Code, w.Body.String())
	}
	assertNoInternals(t, w.Body.String())
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("logged %d entries, want 1: %v", len(entries), entries)
	}
	fields := entries[0].ContextMap()
	if fields["cluster_id"] != "c1" || !strings.Contains(fields["error"].(string), registryDetail) {
		t.Errorf("log fields = %v, want the cluster id and the wrapped error", fields)
	}
}

func TestStatusHandle_registryReadFailureIs503(t *testing.T) {
	db := migratedDB(t)
	if _, err := db.Exec(`DROP TABLE namespace_clusters`); err != nil {
		t.Fatal(err)
	}
	h, _ := statusHandlerOn(db)
	w := statusRequest(h, "/v1/namespace/status?id=c1", h.Handle)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("registry read failure: %d %s", w.Code, w.Body.String())
	}
	assertNoInternals(t, w.Body.String())
}

func TestStatusHandle_existingClusterIs200(t *testing.T) {
	db := migratedDB(t)
	seedCluster(t, db, "c1", "acme")
	h, _ := statusHandlerOn(db)
	w := statusRequest(h, "/v1/namespace/status?id=c1", h.Handle)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"namespace":"acme"`) {
		t.Fatalf("existing cluster: %d %s", w.Code, w.Body.String())
	}
}

// GetClusterByNamespace answers nil for a namespace with no cluster; the
// handler dereferenced it.
func TestStatusHandleByName_unknownNamespaceIs404(t *testing.T) {
	h, _ := statusHandlerOn(migratedDB(t))
	w := statusRequest(h, "/v1/namespace/status/name/ghost", h.HandleByName)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown namespace: %d %s", w.Code, w.Body.String())
	}
}

func TestStatusHandleByName_nodeReadFailureIs503(t *testing.T) {
	db := migratedDB(t)
	seedCluster(t, db, "c1", "acme")
	if _, err := db.Exec(`DROP TABLE namespace_cluster_nodes`); err != nil {
		t.Fatal(err)
	}
	h, logs := statusHandlerOn(db)
	w := statusRequest(h, "/v1/namespace/status/name/acme", h.HandleByName)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("node read failure: %d %s", w.Code, w.Body.String())
	}
	assertNoInternals(t, w.Body.String())
	if logs.Len() != 1 {
		t.Fatalf("logged %d entries, want 1", logs.Len())
	}
	if fields := logs.All()[0].ContextMap(); fields["cluster_id"] != "c1" {
		t.Errorf("log fields = %v, want the cluster's id", fields)
	}
}

func TestStatusHandleByName_registryReadFailureIs503(t *testing.T) {
	db := migratedDB(t)
	if _, err := db.Exec(`DROP TABLE namespace_clusters`); err != nil {
		t.Fatal(err)
	}
	h, logs := statusHandlerOn(db)
	w := statusRequest(h, "/v1/namespace/status/name/acme", h.HandleByName)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("registry read failure: %d %s", w.Code, w.Body.String())
	}
	assertNoInternals(t, w.Body.String())
	if logs.Len() != 1 {
		t.Fatalf("logged %d entries, want 1", logs.Len())
	}
	// The namespace name was logged as the cluster id.
	fields := logs.All()[0].ContextMap()
	if _, mislabelled := fields["cluster_id"]; mislabelled || fields["namespace"] != "acme" {
		t.Errorf("log fields = %v, want the namespace named as namespace and no cluster_id", fields)
	}
}

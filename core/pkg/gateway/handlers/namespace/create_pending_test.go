package namespace

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

const internalCause = "dial tcp 10.0.0.7:4001: /opt/orama/data/acme: connection refused"

// A name whose previous namespace a node has not finished tearing down is
// refused with a retryable 409 that names the nodes and nothing else, and
// nothing is written or provisioned.
func TestCreate_refusesANameStillBeingTornDown(t *testing.T) {
	db := newRegistry()
	db.pendingTeardown = []string{"node-a", "node-b"}
	p := &recordingProvisioner{}
	h := NewCreateHandler(db, p, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", w.Code, w.Body.String())
	}
	body := decodeCreate(t, w)
	if body["code"] != ErrCodeNamespaceTeardownPending || body["retryable"] != true {
		t.Errorf("body = %v, want the teardown-pending code and retryable", body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "node-a, node-b") || !strings.Contains(msg, "myapp") {
		t.Errorf("error %q does not name the name and the nodes", msg)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After on a retryable refusal")
	}
	if len(db.writes) != 0 || p.called {
		t.Errorf("writes = %v, provisioned = %v: a refused create must do neither", db.writes, p.called)
	}
}

func TestCreate_deniesWhenThePendingTeardownCannotBeChecked(t *testing.T) {
	db := newRegistry()
	db.failPendingQuery = true
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusServiceUnavailable || len(db.writes) != 0 {
		t.Fatalf("status %d, writes %v, want 503 and no writes", w.Code, db.writes)
	}
}

func TestCreate_aNameWithNothingOwedIsCreated(t *testing.T) {
	db := newRegistry()
	h := NewCreateHandler(db, &recordingProvisioner{}, nil, zap.NewNop())

	w := httptest.NewRecorder()
	h.ServeHTTP(w, createRequest("0xowner", "myapp"))

	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

// On the real schema: an owed cleanup on an active node holds the name; the
// same row on a node that is gone does not, and neither does a finished one.
func TestCreate_pendingCleanupHoldsTheNameOnlyWhileTheNodeIsActive(t *testing.T) {
	db := openCreateDB(t)
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES ('n1', '192.0.2.1', '10.0.0.1', 'active')`)
	mustExec(`INSERT INTO namespace_pending_cleanup (namespace, node_id, node_ip, action, attempts) VALUES ('myapp', 'n1', '10.0.0.1', 'teardown-namespace', 30)`)
	h := NewCreateHandler(rqlite.NewClient(db), &recordingProvisioner{}, nil, zap.NewNop())

	create := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, createRequest("0xowner", "myapp"))
		return w
	}
	if w := create(); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "n1") {
		t.Fatalf("an exhausted cleanup did not hold the name: %d %s", w.Code, w.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'myapp'`); n != 0 {
		t.Fatal("the refused create left a namespace row")
	}

	mustExec(`UPDATE dns_nodes SET status = 'offline' WHERE id = 'n1'`)
	if w := create(); w.Code != http.StatusAccepted {
		t.Fatalf("a cleanup owed to a node that is gone held the name for ever: %d %s", w.Code, w.Body.String())
	}
}

// The provisioner's error carries addresses and paths; the client gets a fixed
// answer on both failure paths and the cause stays in the log.
func TestCreate_provisioningFailureAnswersNeverCarryTheCause(t *testing.T) {
	t.Run("undone", func(t *testing.T) {
		db := openCreateDB(t)
		h := NewCreateHandler(rqlite.NewClient(db), &recordingProvisioner{err: errString(internalCause)}, nil, zap.NewNop())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, createRequest("0xowner", "myapp"))
		if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "10.0.0.7") || strings.Contains(w.Body.String(), "/opt/orama") {
			t.Fatalf("status %d, body %q: want 503 without the cause", w.Code, w.Body.String())
		}
	})
	t.Run("undo failed", func(t *testing.T) {
		db := openCreateDB(t)
		if _, err := db.Exec(`CREATE TRIGGER keep_grants BEFORE DELETE ON grants BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
			t.Fatal(err)
		}
		h := NewCreateHandler(rqlite.NewClient(db), &recordingProvisioner{err: errString(internalCause)}, nil, zap.NewNop())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, createRequest("0xowner", "myapp"))
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "10.0.0.7") ||
			strings.Contains(w.Body.String(), "/opt/orama") || strings.Contains(w.Body.String(), "refused") ||
			!strings.Contains(w.Body.String(), "delete namespace myapp") {
			t.Fatalf("status %d, body %q: want 500 naming the delete, without the cause", w.Code, w.Body.String())
		}
	})
}

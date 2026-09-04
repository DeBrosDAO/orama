package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

type auditQueryDB struct {
	lastSQL  string
	lastArgs []interface{}
	rows     []interface{}
	err      error
}

func (d *auditQueryDB) Query(_ context.Context, sql string, args ...interface{}) (*QueryResult, error) {
	d.lastSQL = sql
	d.lastArgs = args
	if d.err != nil {
		return nil, d.err
	}
	return &QueryResult{Count: len(d.rows), Rows: d.rows}, nil
}

type auditQueryNet struct{ db *auditQueryDB }

func (n *auditQueryNet) Database() DatabaseClient { return n.db }

func auditHandlers(t *testing.T, db *auditQueryDB) *Handlers {
	t.Helper()
	svc, err := authsvc.NewService(nil, nil, "", "default")
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	return &Handlers{authService: svc, netClient: &auditQueryNet{db: db}}
}

func auditRequest(namespace, query string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/audit"+query, nil)
	if namespace != "" {
		req = req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, namespace))
	}
	return req
}

// Reading another namespace's trail would say who its owners are and when they
// sign in, so the namespace comes from the credential and never the URL.
func TestAuditHandler_readsOnlyTheCallersOwnNamespace(t *testing.T) {
	db := &auditQueryDB{}
	h := auditHandlers(t, db)

	rec := httptest.NewRecorder()
	h.AuditHandler(rec, auditRequest("acme", "?namespace=someone-else"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(db.lastArgs) == 0 || db.lastArgs[0] != "acme" {
		t.Errorf("queried namespace %v, want the caller's own", db.lastArgs)
	}
	if strings.Contains(db.lastSQL, "someone-else") {
		t.Error("the query string reached the query")
	}
}

func TestAuditHandler_refusesACallerWithNoNamespace(t *testing.T) {
	rec := httptest.NewRecorder()
	auditHandlers(t, &auditQueryDB{}).AuditHandler(rec, auditRequest("", ""))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestAuditHandler_returnsTheEvents(t *testing.T) {
	db := &auditQueryDB{rows: []interface{}{
		[]interface{}{"key.issue", "0xowner", "key 7", "success", "203.0.113.4", "orama-cli", `{"scopes":"admin"}`, "2026-09-04T10:00:00Z"},
	}}
	rec := httptest.NewRecorder()
	auditHandlers(t, db).AuditHandler(rec, auditRequest("acme", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Namespace string       `json:"namespace"`
		Count     int          `json:"count"`
		Events    []AuditEntry `json:"events"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Namespace != "acme" || body.Count != 1 {
		t.Fatalf("body = %+v", body)
	}
	got := body.Events[0]
	if got.Action != "key.issue" || got.Actor != "0xowner" || got.Result != "success" {
		t.Errorf("event = %+v", got)
	}
}

// A typo in the filter should say so rather than return an empty page that
// reads as "nothing happened".
func TestAuditHandler_refusesAnUnknownAction(t *testing.T) {
	rec := httptest.NewRecorder()
	auditHandlers(t, &auditQueryDB{}).AuditHandler(rec, auditRequest("acme", "?action=key.issued"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestAuditHandler_acceptsAKnownAction(t *testing.T) {
	db := &auditQueryDB{}
	rec := httptest.NewRecorder()
	auditHandlers(t, db).AuditHandler(rec, auditRequest("acme", "?action="+authsvc.AuditKeyIssued))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(db.lastArgs) < 2 || db.lastArgs[1] != authsvc.AuditKeyIssued {
		t.Errorf("the action filter did not reach the query: %v", db.lastArgs)
	}
}

func TestAuditHandler_boundsThePage(t *testing.T) {
	db := &auditQueryDB{}
	rec := httptest.NewRecorder()
	auditHandlers(t, db).AuditHandler(rec, auditRequest("acme", "?limit=100000"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	limit := db.lastArgs[len(db.lastArgs)-1]
	if limit != auditPageMax {
		t.Errorf("limit = %v, want it capped at %d", limit, auditPageMax)
	}
}

func TestAuditHandler_refusesANonsenseLimit(t *testing.T) {
	for _, q := range []string{"?limit=0", "?limit=-5", "?limit=lots"} {
		rec := httptest.NewRecorder()
		auditHandlers(t, &auditQueryDB{}).AuditHandler(rec, auditRequest("acme", q))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
}

func TestAuditHandler_refusesOtherMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1/audit", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "acme"))
	auditHandlers(t, &auditQueryDB{}).AuditHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d", rec.Code)
	}
}

// An unreadable trail is not an empty trail, and saying it is would hide
// exactly the events somebody came looking for.
func TestAuditHandler_saysSoWhenItCannotRead(t *testing.T) {
	rec := httptest.NewRecorder()
	auditHandlers(t, &auditQueryDB{err: errString("the registry did not answer")}).
		AuditHandler(rec, auditRequest("acme", ""))
	if rec.Code == http.StatusOK {
		t.Fatalf("an unreadable trail came back as an empty one: %s", rec.Body.String())
	}
}

// `since` and `principal` are what make the trail answerable: "what has this
// wallet done", "what has happened since I last looked".

func TestAuditHandler_filtersByPrincipal(t *testing.T) {
	db := &auditQueryDB{}
	rec := httptest.NewRecorder()
	auditHandlers(t, db).AuditHandler(rec, auditRequest("acme", "?principal=0xowner"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(db.lastSQL, "actor = ?") {
		t.Fatalf("principal did not reach the query: %s", db.lastSQL)
	}
	if len(db.lastArgs) < 2 || db.lastArgs[1] != "0xowner" {
		t.Errorf("args = %v, want the principal bound", db.lastArgs)
	}
}

// The timestamps the table holds are SQLite's own — UTC, no zone — and they are
// compared as strings. An offset that is not converted first sorts as if it
// were UTC, so "since 00:00+02:00" would hide two hours of events.
func TestAuditHandler_convertsSinceToTheStoredForm(t *testing.T) {
	db := &auditQueryDB{}
	rec := httptest.NewRecorder()
	auditHandlers(t, db).AuditHandler(rec, auditRequest("acme", "?since=2026-09-04T12:00:00%2B02:00"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(db.lastSQL, "created_at > ?") {
		t.Fatalf("since did not reach the query: %s", db.lastSQL)
	}
	if len(db.lastArgs) < 2 || db.lastArgs[1] != "2026-09-04 10:00:00" {
		t.Errorf("bound %v, want the UTC stored form 2026-09-04 10:00:00", db.lastArgs)
	}
}

// The created_at the API itself returns has to be accepted back, or --follow
// cannot ask for "everything after the last row I saw".
func TestAuditHandler_acceptsAStoredTimestampAsSince(t *testing.T) {
	db := &auditQueryDB{}
	rec := httptest.NewRecorder()
	auditHandlers(t, db).AuditHandler(rec, auditRequest("acme", "?since=2026-09-04+10:00:00"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(db.lastArgs) < 2 || db.lastArgs[1] != "2026-09-04 10:00:00" {
		t.Errorf("bound %v", db.lastArgs)
	}
}

func TestAuditHandler_refusesAnUnreadableSince(t *testing.T) {
	rec := httptest.NewRecorder()
	auditHandlers(t, &auditQueryDB{}).AuditHandler(rec, auditRequest("acme", "?since=yesterday"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// An empty filter must not narrow anything: no actor clause, no time clause.
func TestAuditHandler_withNoFiltersBindsOnlyTheNamespaceAndLimit(t *testing.T) {
	db := &auditQueryDB{}
	rec := httptest.NewRecorder()
	auditHandlers(t, db).AuditHandler(rec, auditRequest("acme", ""))

	if strings.Contains(db.lastSQL, "actor = ?") || strings.Contains(db.lastSQL, "created_at > ?") {
		t.Errorf("an unfiltered read narrowed the query: %s", db.lastSQL)
	}
	if len(db.lastArgs) != 2 {
		t.Errorf("args = %v, want namespace and limit only", db.lastArgs)
	}
}

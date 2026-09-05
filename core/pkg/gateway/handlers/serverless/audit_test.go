package serverless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// Deploying a function, deleting one and changing a secret are the three ways a
// namespace's behaviour changes without anyone signing in again, and none of
// them left a trace. The statement itself is covered against real SQLite in
// pkg/gateway/auth; what these check is that the handler reaches it at all, and
// with the namespace and resource it acted on.

type recordingAuditDB struct {
	client.DatabaseClient
	mu   sync.Mutex
	rows [][]interface{}
}

func (d *recordingAuditDB) Query(_ context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	if !strings.Contains(sql, "audit_events") {
		return &client.QueryResult{}, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rows = append(d.rows, args)
	return &client.QueryResult{Count: 1}, nil
}

func (d *recordingAuditDB) recorded() [][]interface{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]interface{}, len(d.rows))
	copy(out, d.rows)
	return out
}

type recordingAuditNet struct {
	client.NetworkClient
	db *recordingAuditDB
}

func (n *recordingAuditNet) Database() client.DatabaseClient { return n.db }

// auditingHandlers returns handlers wired to a log that keeps what it is told.
func auditingHandlers(t *testing.T, sm serverless.SecretsManager, reg serverless.FunctionRegistry) (*ServerlessHandlers, *recordingAuditDB) {
	t.Helper()
	logger := zap.NewNop()
	db := &recordingAuditDB{}
	if reg == nil {
		reg = newMockRegistry()
	}
	h := NewServerlessHandlers(
		nil, nil, reg, serverless.NewWSManager(logger),
		nil, nil, nil, nil, nil,
		sm,
		auth.NewAuditLog(func() client.DatabaseClient { return (&recordingAuditNet{db: db}).Database() }, nil),
		logger,
	)
	return h, db
}

// namespacedRequest is a request the auth middleware has already resolved.
func namespacedRequest(method, target, namespace, wallet, body string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, namespace)
	if wallet != "" {
		ctx = context.WithValue(ctx, ctxkeys.JWT, &auth.JWTClaims{Sub: wallet})
	}
	return req.WithContext(ctx)
}

// auditRow is one recorded event, by the column order Record binds.
type auditRow struct {
	Namespace, Actor, Action, Resource, Result interface{}
}

func onlyAuditRow(t *testing.T, db *recordingAuditDB) auditRow {
	t.Helper()
	rows := db.recorded()
	if len(rows) != 1 {
		t.Fatalf("%d audit events recorded, want 1", len(rows))
	}
	row := rows[0]
	if len(row) < 5 {
		t.Fatalf("audit row has %d columns: %v", len(row), row)
	}
	return auditRow{row[0], row[1], row[2], row[3], row[4]}
}

const testWallet = "0x1234567890abcdef1234567890abcdef12345678"

func TestHandleSetSecret_recordsTheNameAndNotTheValue(t *testing.T) {
	h, db := auditingHandlers(t, newMockSecretsManager(), nil)

	rec := httptest.NewRecorder()
	h.HandleSetSecret(rec, namespacedRequest(http.MethodPut, "/v1/functions/secrets",
		"acme", testWallet, `{"name":"STRIPE_KEY","value":"sk_live_do_not_record_me"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := onlyAuditRow(t, db)
	if got.Namespace != "acme" || got.Action != auth.AuditSecretSet || got.Resource != "STRIPE_KEY" {
		t.Errorf("event = %+v", got)
	}
	if got.Actor != testWallet {
		t.Errorf("actor = %v, want the signed-in wallet", got.Actor)
	}
	for _, cell := range db.recorded()[0] {
		if s, ok := cell.(string); ok && strings.Contains(s, "sk_live_do_not_record_me") {
			t.Fatal("the secret's value was written to the audit trail")
		}
	}
}

func TestHandleDeleteSecret_recordsTheDeletion(t *testing.T) {
	sm := newMockSecretsManager()
	if err := sm.Set(context.Background(), "acme", "STRIPE_KEY", "v"); err != nil {
		t.Fatal(err)
	}
	h, db := auditingHandlers(t, sm, nil)

	rec := httptest.NewRecorder()
	h.HandleDeleteSecret(rec, namespacedRequest(http.MethodDelete,
		"/v1/functions/secrets/STRIPE_KEY", "acme", testWallet, ""), "STRIPE_KEY")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := onlyAuditRow(t, db)
	if got.Action != auth.AuditSecretDeleted || got.Resource != "STRIPE_KEY" || got.Namespace != "acme" {
		t.Errorf("event = %+v", got)
	}
}

// A refusal is not an event. Recording one per failed call would let anyone who
// can reach the endpoint fill a table replicated to every node.
func TestHandleDeleteSecret_recordsNothingWhenTheSecretIsNotThere(t *testing.T) {
	h, db := auditingHandlers(t, newMockSecretsManager(), nil)

	rec := httptest.NewRecorder()
	h.HandleDeleteSecret(rec, namespacedRequest(http.MethodDelete,
		"/v1/functions/secrets/MISSING", "acme", testWallet, ""), "MISSING")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if rows := db.recorded(); len(rows) != 0 {
		t.Errorf("%d events recorded for a refusal: %v", len(rows), rows)
	}
}

func TestHandleSetSecret_recordsNothingWhenTheStoreRefuses(t *testing.T) {
	sm := newMockSecretsManager()
	sm.setErr = context.DeadlineExceeded
	h, db := auditingHandlers(t, sm, nil)

	rec := httptest.NewRecorder()
	h.HandleSetSecret(rec, namespacedRequest(http.MethodPut, "/v1/functions/secrets",
		"acme", testWallet, `{"name":"K","value":"v"}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if rows := db.recorded(); len(rows) != 0 {
		t.Errorf("a failed write was recorded as a success: %v", rows)
	}
}

func TestDeleteFunction_recordsTheDeletion(t *testing.T) {
	h, db := auditingHandlers(t, nil, newMockRegistry())

	rec := httptest.NewRecorder()
	h.DeleteFunction(rec, namespacedRequest(http.MethodDelete,
		"/v1/functions/greet", "acme", testWallet, ""), "greet", 0)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := onlyAuditRow(t, db)
	if got.Action != auth.AuditFunctionDeleted || got.Resource != "greet" || got.Namespace != "acme" {
		t.Errorf("event = %+v", got)
	}
	if got.Result != auth.AuditSuccess {
		t.Errorf("result = %v", got.Result)
	}
}

func TestDeleteFunction_recordsNothingWhenTheRegistryRefuses(t *testing.T) {
	reg := newMockRegistry()
	reg.deleteErr = context.DeadlineExceeded
	h, db := auditingHandlers(t, nil, reg)

	rec := httptest.NewRecorder()
	h.DeleteFunction(rec, namespacedRequest(http.MethodDelete,
		"/v1/functions/greet", "acme", testWallet, ""), "greet", 0)

	if rec.Code == http.StatusOK {
		t.Fatalf("a failed delete answered 200")
	}
	if rows := db.recorded(); len(rows) != 0 {
		t.Errorf("a failed delete was recorded: %v", rows)
	}
}

// An API-key-authenticated caller is recorded too, but never by the key: the
// exchange endpoint mints JWTs whose subject IS the key.
func TestDeleteFunction_doesNotRecordTheCredential(t *testing.T) {
	const key = "orama_ak_3kFj9sPqR2vX7mNb_1a2b3c"
	h, db := auditingHandlers(t, nil, newMockRegistry())

	rec := httptest.NewRecorder()
	h.DeleteFunction(rec, namespacedRequest(http.MethodDelete,
		"/v1/functions/greet", "acme", key, ""), "greet", 0)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := onlyAuditRow(t, db)
	actor, _ := got.Actor.(string)
	if strings.Contains(actor, "3kFj9sPqR2vX7mNb") || actor == key {
		t.Fatalf("actor = %q — the key reached the audit trail", actor)
	}
	if actor == "" {
		t.Error("a key-authenticated delete recorded no actor at all")
	}
}

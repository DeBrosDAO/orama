package auth

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
)

type auditDB struct {
	client.DatabaseClient
	mu      sync.Mutex
	rows    [][]interface{}
	failing bool
}

func (d *auditDB) Query(_ context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !strings.Contains(sql, "audit_events") {
		return nil, errString("unexpected sql: " + sql)
	}
	if d.failing {
		return nil, errString("the registry did not answer")
	}
	d.rows = append(d.rows, args)
	return &client.QueryResult{Count: 1}, nil
}

type auditNet struct {
	client.NetworkClient
	db *auditDB
}

func (n *auditNet) Database() client.DatabaseClient { return n.db }

func newTestAudit() (*AuditLog, *auditDB) {
	db := &auditDB{}
	return NewAuditLog(registryOf(&auditNet{db: db}), nil), db
}

func TestAuditLog_recordsTheEvent(t *testing.T) {
	log, db := newTestAudit()

	log.Record(context.Background(), AuditEvent{
		Namespace: "acme",
		Actor:     "0xowner",
		Action:    AuditKeyIssued,
		Resource:  "key 7",
		Result:    AuditSuccess,
		IP:        "203.0.113.4",
		UserAgent: "orama-cli/1.0",
		Metadata:  map[string]string{"scopes": "admin"},
	})

	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.rows) != 1 {
		t.Fatalf("%d rows written", len(db.rows))
	}
	row := db.rows[0]
	if row[0] != "acme" || row[1] != "0xowner" || row[2] != AuditKeyIssued {
		t.Errorf("row = %v", row[:3])
	}
	if row[4] != AuditSuccess {
		t.Errorf("result = %v", row[4])
	}
	metadata, _ := row[7].(string)
	if !strings.Contains(metadata, "admin") {
		t.Errorf("metadata = %v", row[7])
	}
}

// A cluster-level event has no namespace, and the column has to take that —
// the table it replaced had a NOT NULL foreign key, so an attempt against a
// namespace that does not exist could not be recorded at all.
func TestAuditLog_recordsAnEventWithNoNamespace(t *testing.T) {
	log, db := newTestAudit()
	log.Record(context.Background(), AuditEvent{Action: AuditOperatorAction, Actor: "0xoperator"})

	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.rows) != 1 {
		t.Fatalf("%d rows written", len(db.rows))
	}
	if db.rows[0][0] != nil {
		t.Errorf("namespace = %v, want NULL", db.rows[0][0])
	}
}

func TestAuditLog_defaultsToSuccess(t *testing.T) {
	log, db := newTestAudit()
	log.Record(context.Background(), AuditEvent{Action: AuditLoggedOut})

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.rows[0][4] != AuditSuccess {
		t.Errorf("result = %v, want %q", db.rows[0][4], AuditSuccess)
	}
}

// The record is evidence, not a control. Refusing a login because the audit row
// could not be written would turn a database blip into an outage, and the
// caller was already authenticated or refused on its own merits.
func TestAuditLog_aFailedWriteDoesNotStopAnything(t *testing.T) {
	log, db := newTestAudit()
	db.mu.Lock()
	db.failing = true
	db.mu.Unlock()

	// Nothing to assert but that it returns: Record has no error to return,
	// which is the decision this test pins.
	log.Record(context.Background(), AuditEvent{Action: AuditVerifySucceeded})
}

func TestAuditLog_nilLogAndNilDatabaseAreNoOps(t *testing.T) {
	var nilLog *AuditLog
	nilLog.Record(context.Background(), AuditEvent{Action: AuditKeyIssued})
	NewAuditLog(nil, nil).Record(context.Background(), AuditEvent{Action: AuditKeyIssued})
}

func TestAuditLog_ignoresAnEventWithNoAction(t *testing.T) {
	log, db := newTestAudit()
	log.Record(context.Background(), AuditEvent{Namespace: "acme"})

	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.rows) != 0 {
		t.Errorf("an event with no action was recorded: %v", db.rows)
	}
}

// The user agent is whatever the client sends and this table is replicated to
// every node.
func TestAuditLog_boundsTheFieldsACallerControls(t *testing.T) {
	log, db := newTestAudit()
	log.Record(context.Background(), AuditEvent{
		Action:    AuditVerifySucceeded,
		Actor:     strings.Repeat("a", maxAuditFieldLength*3),
		UserAgent: strings.Repeat("u", maxAuditFieldLength*3),
	})

	db.mu.Lock()
	defer db.mu.Unlock()
	actor, _ := db.rows[0][1].(string)
	agent, _ := db.rows[0][6].(string)
	if len(actor) > maxAuditFieldLength {
		t.Errorf("actor is %d bytes", len(actor))
	}
	if len(agent) > maxAuditFieldLength {
		t.Errorf("user agent is %d bytes", len(agent))
	}
}

// The address recorded is the client's, not the reverse proxy's — every public
// request reaches this gateway from 127.0.0.1.
func TestAuditLog_recordsTheClientAddressNotTheProxys(t *testing.T) {
	log, db := newTestAudit()

	req := httptest.NewRequest("POST", "/v1/auth/verify", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.4, 10.0.0.1")
	req.Header.Set("User-Agent", "anchat/2.1")

	log.RecordFromRequest(context.Background(), req, AuditEvent{Action: AuditVerifySucceeded})

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.rows[0][5] != "203.0.113.4" {
		t.Errorf("ip = %v, want the client's address", db.rows[0][5])
	}
	if db.rows[0][6] != "anchat/2.1" {
		t.Errorf("user agent = %v", db.rows[0][6])
	}
}

func TestAuditLog_fallsBackToRemoteAddrWithNoProxyHeader(t *testing.T) {
	log, db := newTestAudit()
	req := httptest.NewRequest("POST", "/v1/auth/verify", nil)
	req.RemoteAddr = "198.51.100.7:1234"

	log.RecordFromRequest(context.Background(), req, AuditEvent{Action: AuditVerifySucceeded})

	db.mu.Lock()
	defer db.mu.Unlock()
	if db.rows[0][5] != "198.51.100.7:1234" {
		t.Errorf("ip = %v", db.rows[0][5])
	}
}

// Every action a handler records has to be in the declared list, or the audit
// endpoint's filter and the docs cannot describe what they are showing.
func TestAuditActions_listEveryConstant(t *testing.T) {
	declared := map[string]bool{}
	for _, a := range AuditActions {
		if declared[a] {
			t.Errorf("%q is listed twice", a)
		}
		declared[a] = true
	}
	for _, a := range []string{
		AuditChallengeIssued, AuditVerifySucceeded, AuditRefreshed, AuditRefreshReplayed,
		AuditLoggedOut, AuditKeyIssued, AuditKeyRevoked, AuditKeysRevokedBulk,
		AuditNamespaceCreated, AuditOperatorAction,
	} {
		if !declared[a] {
			t.Errorf("%q is a constant but is not in AuditActions", a)
		}
	}
}

// A Service with a database always has somewhere to write the record.
func TestNewService_wiresTheAuditLogWheneverThereIsADatabase(t *testing.T) {
	s, err := NewService(nil, &auditNet{db: &auditDB{}}, "", "default")
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if s.Audit() == nil {
		t.Fatal("a Service with a database has nowhere to record auth events")
	}
}

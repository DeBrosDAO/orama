package auth

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// keyRegistryDB is enough of the registry for RevokeKey: it resolves the
// namespace, hands back the hashed key, and records everything written —
// including into revoked_tokens, which is the part that used to be missing.
type keyRegistryDB struct {
	client.DatabaseClient

	mu          sync.Mutex
	hashedKey   string
	revocations []revocation
	statements  []string
}

func (d *keyRegistryDB) Query(_ context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	trimmed := strings.TrimSpace(sql)
	d.statements = append(d.statements, strings.Join(strings.Fields(trimmed), " "))

	switch {
	case strings.HasPrefix(trimmed, "SELECT id FROM namespaces"):
		return &client.QueryResult{Columns: []string{"id"}, Rows: [][]interface{}{{int64(1)}}, Count: 1}, nil
	case strings.HasPrefix(trimmed, "SELECT key FROM api_keys"):
		return &client.QueryResult{Columns: []string{"key"}, Rows: [][]interface{}{{d.hashedKey}}, Count: 1}, nil
	case strings.HasPrefix(trimmed, "INSERT INTO revoked_tokens"):
		rev := revocation{}
		if args[1] != nil {
			rev.subject, _ = args[1].(string)
		}
		rev.issuedBefore = toInt64(args[2])
		rev.expiresAt = toInt64(args[3])
		d.revocations = append(d.revocations, rev)
		return &client.QueryResult{Count: 1}, nil
	case strings.HasPrefix(trimmed, "SELECT jti"):
		return &client.QueryResult{Columns: []string{"jti", "subject", "issued_before", "expires_at"}}, nil
	}
	return &client.QueryResult{Count: 1}, nil
}

type keyRegistryNet struct {
	client.NetworkClient
	db *keyRegistryDB
}

func (n *keyRegistryNet) Database() client.DatabaseClient { return n.db }

// The fifteen-minute window, at the place it was opened: revoking a key did
// nothing to the tokens already exchanged from it.
func TestRevokeKey_alsoRevokesTheTokensAlreadyIssuedFromTheKey(t *testing.T) {
	rawKey := "ak_runtime:acme"
	s := &Service{apiKeyHMACSecret: "test-hmac-secret"}
	db := &keyRegistryDB{hashedKey: s.HashAPIKey(rawKey)}
	s.orm = &keyRegistryNet{db: db}
	s.revocations = NewRevocationList(s.registryDatabase, nil)

	if err := s.RevokeKey(context.Background(), "acme", 7); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.revocations) != 1 {
		t.Fatalf("%d revocations recorded; the key stopped authenticating but its tokens did not",
			len(db.revocations))
	}
	rev := db.revocations[0]
	if rev.subject != s.HashAPIKey(rawKey) {
		t.Errorf("revoked subject = %q, want the key's hash — that is what the verifier looks under", rev.subject)
	}
	if rev.issuedBefore == 0 {
		t.Error("the revocation covers no tokens: issued_before is zero")
	}
	if rev.expiresAt <= rev.issuedBefore {
		t.Errorf("the revocation expires at %d, before or when it starts (%d), so it covers nothing",
			rev.expiresAt, rev.issuedBefore)
	}
}

// The record of who revoked what was the other thing missing.
func TestRevokeKey_isRecorded(t *testing.T) {
	s := &Service{apiKeyHMACSecret: "test-hmac-secret"}
	db := &keyRegistryDB{hashedKey: "hashed"}
	s.orm = &keyRegistryNet{db: db}
	s.revocations = NewRevocationList(s.registryDatabase, nil)
	s.audit = NewAuditLog(s.registryDatabase, nil)

	if err := s.RevokeKey(context.Background(), "acme", 7); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	recorded := false
	for _, stmt := range db.statements {
		if strings.Contains(stmt, "INSERT INTO audit_events") {
			recorded = true
		}
	}
	if !recorded {
		t.Error("revoking a key was not recorded anywhere")
	}
}

// If the revocation cannot be written, the caller has to hear about it: the key
// is revoked and its tokens are not, which is the state this exists to prevent.
func TestRevokeKey_reportsAFailureToRevokeTheTokens(t *testing.T) {
	s := &Service{apiKeyHMACSecret: "test-hmac-secret"}
	db := &keyRegistryDB{hashedKey: "hashed"}
	s.orm = &keyRegistryNet{db: db}
	s.revocations = nil // no list: nothing can be recorded

	err := s.RevokeKey(context.Background(), "acme", 7)
	if err == nil {
		t.Fatal("a key was reported as fully revoked while its tokens keep working")
	}
	if !strings.Contains(err.Error(), "keep working") {
		t.Errorf("the error does not say what is still true: %v", err)
	}
}

package auth

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

const auditWallet = "0x1111111111111111111111111111111111111111"

func withCaller(ctx context.Context, sub string) context.Context {
	return context.WithValue(ctx, ctxkeys.JWT, &JWTClaims{Sub: sub})
}

// auditedRegistry is realRegistry with its audit trail switched on.
func auditedRegistry(t *testing.T) *Service {
	t.Helper()
	s, _, _ := realRegistry(t)
	s.audit = NewAuditLog(s.registryDatabase, nil)
	return s
}

func readActions(t *testing.T, s *Service, action string) [][]interface{} {
	t.Helper()
	res, err := s.Audit().Query(context.Background(),
		`SELECT actor, resource, result FROM audit_events WHERE namespace = ? AND action = ?`, "anchat", action)
	if err != nil {
		t.Fatalf("read the trail: %v", err)
	}
	return res.Rows
}

// Key events were recorded from inside the service, with no request to take an
// actor from, so the trail said a key was minted and not by whom.
func TestAudit_aKeyEventNamesWhoDidIt(t *testing.T) {
	s := auditedRegistry(t)

	for name, tc := range map[string]struct{ caller, want string }{
		"a wallet is recorded as itself":  {auditWallet, auditWallet},
		"a key is recorded as its print":  {"ak_secretkeymaterial", RedactSubject("ak_secretkeymaterial")},
		"no caller is recorded as nobody": {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if tc.caller != "" {
				ctx = withCaller(ctx, tc.caller)
			}
			_, id, err := s.IssueScopedKey(ctx, "anchat", "cache", KeyOptions{Label: name})
			if err != nil {
				t.Fatalf("issue: %v", err)
			}
			if err := s.RevokeKey(ctx, "anchat", id); err != nil {
				t.Fatalf("revoke: %v", err)
			}
			for _, action := range []string{AuditKeyIssued, AuditKeyRevoked} {
				var found bool
				for _, row := range readActions(t, s, action) {
					if getStringVal(row[1]) == "key "+strconv.FormatInt(id, 10) {
						found = true
						if got := getStringVal(row[0]); got != tc.want {
							t.Errorf("%s actor = %q, want %q", action, got, tc.want)
						}
					}
				}
				if !found {
					t.Errorf("no %s event for key %d", action, id)
				}
			}
		})
	}
}

func TestAudit_aRecordedActorIsNotOverwritten(t *testing.T) {
	s := auditedRegistry(t)
	s.Audit().Record(withCaller(context.Background(), auditWallet), AuditEvent{
		Namespace: "anchat", Actor: "system", Action: AuditKeyIssued,
	})
	rows := readActions(t, s, AuditKeyIssued)
	if len(rows) != 1 || getStringVal(rows[0][0]) != "system" {
		t.Errorf("rows = %v, want the actor the caller named", rows)
	}
}

func TestAuditLog_queryWithNoDatabaseSaysSo(t *testing.T) {
	var none *AuditLog
	if _, err := none.Query(context.Background(), "SELECT 1"); !errors.Is(err, ErrNoAuditDatabase) {
		t.Errorf("err = %v, want ErrNoAuditDatabase", err)
	}
}

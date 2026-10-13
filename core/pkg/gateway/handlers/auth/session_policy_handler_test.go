package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// A policy recorded while the key sweep stopped is not reported as a policy
// that could not be recorded: the answer says it is set and how to finish.
func TestSessionPolicyHandler_aStoppedKeySweepSaysThePolicyIsSet(t *testing.T) {
	f := newFlow(t)
	f.member("0xenduser", authsvc.RoleRuntime)
	if _, err := f.svc.GetOrCreateAPIKey(context.Background(), "0xenduser", flowNamespace); err != nil {
		t.Fatalf("key: %v", err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER refuse_revocation BEFORE UPDATE OF revoked_at ON api_keys
		BEGIN SELECT RAISE(FAIL, 'injected'); END`); err != nil {
		t.Fatalf("install the failure: %v", err)
	}
	operator := &authsvc.JWTClaims{Sub: "0xowner", Namespace: flowNamespace}

	code, body := f.do(f.h.SessionPolicyHandler, http.MethodPut, "/v1/namespace/session-policy",
		map[string]any{"device_policy": "required"}, operator)
	msg, _ := body["error"].(string)
	if code != http.StatusServiceUnavailable || body["code"] != ErrCodePolicySweepIncomplete {
		t.Fatalf("a stopped sweep answered %d %v", code, body)
	}
	for _, want := range []string{"the policy is set", "repeat the request"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the answer %q does not say %q", msg, want)
		}
	}
	if strings.Contains(msg, "injected") {
		t.Errorf("the answer %q carries the registry's own error", msg)
	}
	var metadata string
	if err := f.db.QueryRow(`SELECT metadata FROM audit_events WHERE action = ?`, authsvc.AuditSessionPolicySet).Scan(&metadata); err != nil {
		t.Fatalf("the policy change was not audited: %v", err)
	}
	if !strings.Contains(metadata, `"sign_in_key_sweep":"incomplete"`) {
		t.Errorf("the audit row %s does not say the sweep stopped", metadata)
	}
	if code, body := f.do(f.h.SessionPolicyHandler, http.MethodGet, "/v1/namespace/session-policy", nil, operator); code != http.StatusOK ||
		body["device_policy"] != "required" {
		t.Errorf("read back: %d %v", code, body)
	}
}

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

const sessionPolicyPath = "/v1/namespace/session-policy"

// owner makes wallet the namespace's one owner, which is what creating it does.
func (f *flow) owner(wallet string) {
	f.t.Helper()
	f.member(wallet, authsvc.RoleAdmin)
	if _, err := f.db.Exec(`UPDATE grants SET role = 'owner' WHERE role = 'admin'`); err != nil {
		f.t.Fatalf("make the owner: %v", err)
	}
}

func (f *flow) setPolicy(body map[string]any) (int, map[string]any) {
	f.t.Helper()
	operator := &authsvc.JWTClaims{Sub: "0xowner", Namespace: flowNamespace}
	return f.do(f.h.SessionPolicyHandler, http.MethodPut, sessionPolicyPath, body, operator)
}

func (f *flow) grantCount(wallet string) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM grants AS g JOIN principals AS p ON p.id = g.principal_id
		WHERE p.identifier = ?`, strings.ToLower(wallet)).Scan(&n); err != nil {
		f.t.Fatalf("count grants: %v", err)
	}
	return n
}

// An application's end user holds no grant. By default the namespace refuses
// it; once the owner opens sign-in it gets a session, no key, and no grant.
func TestVerifyHandler_openSignInLetsAGrantlessWalletInWithoutAKey(t *testing.T) {
	f := newFlow(t)
	f.owner("0xowner")
	w := newWallet(t)

	if code, body := f.signIn(w, nil); code != http.StatusForbidden || body["code"] != ErrCodeNamespaceNotOwned {
		t.Fatalf("a grantless wallet in a members namespace: %d %v", code, body)
	}
	if code, body := f.setPolicy(map[string]any{"sign_in": "open"}); code != http.StatusOK || body["sign_in"] != "open" {
		t.Fatalf("open sign-in: %d %v", code, body)
	}
	code, body := f.signIn(w, nil)
	if code != http.StatusOK {
		t.Fatalf("a grantless wallet in an open namespace: %d %v", code, body)
	}
	if body["api_key"] != nil {
		t.Errorf("an end user was handed a key: %v", body["api_key"])
	}
	if !strings.EqualFold(f.claimsOf(body).Sub, w.address) {
		t.Error("the session is not the wallet's")
	}
	if n := f.grantCount(w.address); n != 0 {
		t.Errorf("signing in wrote %d grants for an end user", n)
	}
}

// A key is a member's credential: asking for one outright refuses.
func TestIssueAPIKeyHandler_refusesAGrantlessEndUser(t *testing.T) {
	f := newFlow(t)
	f.owner("0xowner")
	if code, body := f.setPolicy(map[string]any{"sign_in": "open"}); code != http.StatusOK {
		t.Fatalf("open sign-in: %d %v", code, body)
	}
	w := newWallet(t)
	code, c := f.do(f.h.ChallengeHandler, http.MethodPost, "/v1/auth/challenge",
		map[string]any{"wallet": w.address, "namespace": flowNamespace}, nil)
	if code != http.StatusOK {
		t.Fatalf("challenge: %d %v", code, c)
	}
	message := c["message"].(string)
	code, body := f.do(f.h.IssueAPIKeyHandler, http.MethodPost, "/v1/auth/api-key",
		map[string]any{"message": message, "signature": w.sign(message)}, nil)
	if code != http.StatusForbidden || body["code"] != ErrCodeNoKeyForRole || body["api_key"] != nil {
		t.Errorf("a key for a grantless wallet: %d %v", code, body)
	}
}

// The device policy applies to an open end user as it does to any end user.
func TestVerifyHandler_openSignInStillHoldsEndUsersToTheDevicePolicy(t *testing.T) {
	f := newFlow(t)
	f.owner("0xowner")
	if code, body := f.setPolicy(map[string]any{"sign_in": "open", "device_policy": "required"}); code != http.StatusOK {
		t.Fatalf("policy: %d %v", code, body)
	}
	w := newWallet(t)
	if code, body := f.signIn(w, nil); code != http.StatusForbidden || body["code"] != ErrCodeDeviceRequired {
		t.Errorf("an open end user without a device: %d %v", code, body)
	}
	if code, body := f.signIn(w, newTestDeviceKey(t)); code != http.StatusOK {
		t.Errorf("an open end user with a device: %d %v", code, body)
	}
}

// Closing sign-in ends a grantless wallet's session at its next refresh, and
// leaves a member's alone.
func TestRefreshHandler_closingSignInEndsOnlyGrantlessSessions(t *testing.T) {
	f := newFlow(t)
	f.owner("0xowner")
	member := newWallet(t)
	f.member(member.address, authsvc.RoleRuntime)
	if code, body := f.setPolicy(map[string]any{"sign_in": "open"}); code != http.StatusOK {
		t.Fatalf("open sign-in: %d %v", code, body)
	}
	endUser := newWallet(t)
	_, endBody := f.signIn(endUser, nil)
	_, memberBody := f.signIn(member, nil)

	if code, body := f.setPolicy(map[string]any{"sign_in": "members"}); code != http.StatusOK {
		t.Fatalf("close sign-in: %d %v", code, body)
	}
	refresh := func(session map[string]any) (int, map[string]any) {
		return f.do(f.h.RefreshHandler, http.MethodPost, "/v1/auth/refresh",
			map[string]any{"refresh_token": session["refresh_token"], "namespace": flowNamespace}, nil)
	}
	if code, body := refresh(endBody); code != http.StatusForbidden || body["code"] != ErrCodeSignInClosed {
		t.Errorf("a grantless session refreshed after the close: %d %v", code, body)
	}
	if code, body := refresh(memberBody); code != http.StatusOK {
		t.Errorf("a member's refresh was refused by the close: %d %v", code, body)
	}
	if code, body := f.signIn(newWallet(t), nil); code != http.StatusForbidden || body["code"] != ErrCodeNamespaceNotOwned {
		t.Errorf("a new grantless sign-in after the close: %d %v", code, body)
	}
}

func TestSessionPolicyHandler_putValidatesBothFieldsBeforeWritingEither(t *testing.T) {
	f := newFlow(t)
	for name, body := range map[string]map[string]any{
		"empty":             {},
		"bad sign_in":       {"sign_in": "everyone"},
		"bad device_policy": {"device_policy": "sometimes", "sign_in": "open"},
		"empty sign_in":     {"sign_in": ""},
	} {
		if code, out := f.setPolicy(body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v, want 400", name, code, out)
		}
	}
	operator := &authsvc.JWTClaims{Sub: "0xowner", Namespace: flowNamespace}
	_, got := f.do(f.h.SessionPolicyHandler, http.MethodGet, sessionPolicyPath, nil, operator)
	if got["sign_in"] != "members" || got["device_policy"] != "optional" {
		t.Errorf("a refused request wrote something: %v", got)
	}
}

func TestSessionPolicyHandler_aPartialPutKeepsTheOtherField(t *testing.T) {
	f := newFlow(t)
	operator := &authsvc.JWTClaims{Sub: "0xowner", Namespace: flowNamespace}
	read := func() map[string]any {
		_, got := f.do(f.h.SessionPolicyHandler, http.MethodGet, sessionPolicyPath, nil, operator)
		return got
	}

	if code, body := f.setPolicy(map[string]any{"sign_in": "open"}); code != http.StatusOK ||
		body["device_policy"] != "optional" || body["sign_in"] != "open" {
		t.Fatalf("sign_in alone: %d %v", code, body)
	}
	if code, body := f.setPolicy(map[string]any{"device_policy": "required"}); code != http.StatusOK {
		t.Fatalf("device_policy alone: %d %v", code, body)
	}
	if got := read(); got["sign_in"] != "open" || got["device_policy"] != "required" || got["namespace"] != flowNamespace {
		t.Errorf("setting the device policy changed sign_in: %v", got)
	}
	if code, body := f.setPolicy(map[string]any{"sign_in": "members"}); code != http.StatusOK {
		t.Fatalf("sign_in again: %d %v", code, body)
	}
	if got := read(); got["sign_in"] != "members" || got["device_policy"] != "required" {
		t.Errorf("setting sign_in changed the device policy: %v", got)
	}
	var auditRows int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ? AND resource = 'open'`,
		authsvc.AuditSignInPolicySet).Scan(&auditRows); err != nil || auditRows != 1 {
		t.Errorf("the opening was audited %d times, %v", auditRows, err)
	}
}

func TestSessionPolicyHandler_theLobbyHasNoSignInPolicy(t *testing.T) {
	f := newFlow(t)
	r := httptest.NewRequest(http.MethodPut, sessionPolicyPath, strings.NewReader(`{"sign_in":"open"}`))
	ctx := context.WithValue(r.Context(), CtxKeyNamespaceOverride, authsvc.LobbyNamespace)
	rec := httptest.NewRecorder()
	f.h.SessionPolicyHandler(rec, r.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Errorf("the lobby accepted a sign-in policy: %d %s", rec.Code, rec.Body.String())
	}
}

// A device link is a way to a session, so closing sign-in closes it too: a
// grantless wallet's phone approves its laptop, the owner closes sign-in, and
// the laptop's claim is refused before the code is spent or the laptop
// enrolled — reopening lets the same code be collected.
func TestDeviceTokenHandler_closingSignInRefusesALinkClaim(t *testing.T) {
	f := newFlow(t)
	f.owner("0xowner")
	if code, body := f.setPolicy(map[string]any{"sign_in": "open", "device_policy": "approval"}); code != http.StatusOK {
		t.Fatalf("open: %d %v", code, body)
	}
	w := newWallet(t)
	phone, laptop := newTestDeviceKey(t), newTestDeviceKey(t)
	code, first := f.signIn(w, phone)
	if code != http.StatusOK {
		t.Fatalf("the end user's first device: %d %v", code, first)
	}
	code, pending := f.signIn(w, laptop)
	if code != http.StatusAccepted {
		t.Fatalf("a second device: %d %v", code, pending)
	}
	userCode, deviceCode := pending["user_code"].(string), pending["device_code"].(string)
	if code, body := f.do(f.h.DeviceByIDHandler, http.MethodPost, "/v1/auth/devices/approve",
		map[string]any{"user_code": userCode, "device_proof": proof(phone, authsvc.DeviceProofApprove, userCode)},
		f.claimsOf(first)); code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, body)
	}

	if code, body := f.setPolicy(map[string]any{"sign_in": "members"}); code != http.StatusOK {
		t.Fatalf("close: %d %v", code, body)
	}
	claim := func() (int, map[string]any) {
		return f.do(f.h.DeviceTokenHandler, http.MethodPost, "/v1/auth/device/token",
			map[string]any{"device_code": deviceCode, "device_proof": proof(laptop, authsvc.DeviceProofClaim, deviceCode)}, nil)
	}
	if code, body := claim(); code != http.StatusForbidden || body["code"] != ErrCodeNamespaceNotOwned {
		t.Fatalf("a link claim after sign-in closed: %d %v", code, body)
	}

	if code, body := f.setPolicy(map[string]any{"sign_in": "open"}); code != http.StatusOK {
		t.Fatalf("reopen: %d %v", code, body)
	}
	if code, body := claim(); code != http.StatusOK || f.claimsOf(body).Did != laptop.id {
		t.Errorf("the refused claim spent its code: %d %v", code, body)
	}
}

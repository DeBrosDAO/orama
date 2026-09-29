//go:build e2e_fleet

package authdevices

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	// pendingLife: "A pending login lasts ten minutes" (docs/AUTH.md).
	pendingLife = 10 * time.Minute
	// pollInterval is what the gateway hands back (core/pkg/gateway/auth/device.go).
	pollInterval = 5
	// deviceCodeLength: 32 random bytes, base64url without padding.
	deviceCodeLength = 43
	// expiryPollEvery paces waiting out a pending login without slow_down.
	expiryPollEvery = 30 * time.Second
	// expiresInSlack: expires_in is the whole seconds left when the gateway
	// answers (core/pkg/gateway/handlers/auth/device_handler.go), so a fresh
	// login reports a little under ten minutes.
	expiresInSlack = 10 * time.Second
	// slowDownCertain is the gap under which a second poll is always
	// slow_down: the gateway compares against last_polled_at, stored with
	// one-second resolution, with a 5-second interval
	// (core/pkg/gateway/auth/device.go DevicePollInterval). A paced poll may
	// be held longer than that, and then either answer is correct.
	slowDownCertain = pollInterval*time.Second - time.Second
)

// userCodeShape: eight characters of an unambiguous alphabet, dash in the middle.
var userCodeShape = regexp.MustCompile(`^[BCDFGHJKMNPQRTVWXYZ23467]{4}-[BCDFGHJKMNPQRTVWXYZ23467]{4}$`)

type deviceStart struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	DeviceID        string `json:"device_id"`
	VerificationURI string `json:"verification_uri"`
}

// startLogin begins a device login (RFC 8628) for namespace ("" is whichever
// namespace the approver signs in to).
func startLogin(t testing.TB, c *gw.Client, body map[string]any) deviceStart {
	t.Helper()
	var d deviceStart
	if err := postJSON(t, c, pathDeviceStart, "", body).Expect(t, http.StatusOK).Decode(&d); err != nil {
		t.Fatal(err)
	}
	protect(t, c, d.DeviceCode)
	return d
}

// poll is one POST /v1/auth/device/token.
func poll(t testing.TB, c *gw.Client, code string, p *wallet.Proof) *gw.Response {
	t.Helper()
	body := map[string]any{"device_code": code}
	if p != nil {
		body["device_proof"] = p
	}
	return postJSON(t, c, pathDeviceToken, "", body)
}

// oauthErrorName is the RFC 8628 error a 400 names, or "" for anything else.
func oauthErrorName(resp *gw.Response) string {
	var body struct {
		Error string `json:"error"`
	}
	if resp.Status != http.StatusBadRequest || resp.Decode(&body) != nil {
		return ""
	}
	return body.Error
}

// expectOAuthError: RFC 8628 outcomes are 400 {"error": "<name>"}.
func expectOAuthError(t testing.TB, resp *gw.Response, name string) {
	t.Helper()
	var body struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := resp.Decode(&body); err != nil || resp.Status != http.StatusBadRequest || body.Error != name || body.Description == "" {
		t.Fatalf("want 400 %s with a description, got %d %s", name, resp.Status, resp.Body)
	}
}

// approveWithWallet approves userCode by signing a fresh challenge for namespace.
func approveWithWallet(t testing.TB, c *gw.Client, w *wallet.EVM, namespace, userCode string, deny bool) *gw.Response {
	t.Helper()
	req := signInReq(t, c, w, namespace, nil)
	return postJSON(t, c, pathDeviceApprove, "", map[string]any{
		"user_code": userCode, "message": req.Message, "signature": req.Signature, "deny": deny})
}

// TestDeviceLogin_startShape: a device code, a readable user code, ten
// minutes, a five-second interval and no verification_uri (docs/AUTH.md,
// "Signing in from a machine with no wallet on it").
func TestDeviceLogin_startShape(t *testing.T) {
	t.Parallel()
	d := startLogin(t, harness.GW(t), map[string]any{})
	if len(d.DeviceCode) != deviceCodeLength || !userCodeShape.MatchString(d.UserCode) {
		t.Errorf("device code %d chars, user code %q", len(d.DeviceCode), d.UserCode)
	}
	expires := time.Duration(d.ExpiresIn) * time.Second
	if expires < pendingLife-expiresInSlack || expires > pendingLife || d.Interval != pollInterval || d.VerificationURI != "" {
		t.Errorf("expires_in %d interval %d verification_uri %q", d.ExpiresIn, d.Interval, d.VerificationURI)
	}
}

// TestDeviceLogin_pendingSlowDownApprovedOnce walks the RFC 8628 states:
// pending, slow_down for polling too fast (asserted when the paced second poll
// went out inside the interval; either answer is right once it did not), the session after a wallet
// approves, then invalid_grant for a second collect. Approving twice is 409.
func TestDeviceLogin_pendingSlowDownApprovedOnce(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	d := startLogin(t, c, map[string]any{})
	firstSent := time.Now()
	expectOAuthError(t, poll(t, c, d.DeviceCode, nil), "authorization_pending")
	second := poll(t, c, d.DeviceCode, nil)
	if gap := time.Since(firstSent); gap < slowDownCertain {
		expectOAuthError(t, second, "slow_down")
	} else if name := oauthErrorName(second); name != "slow_down" && name != "authorization_pending" {
		t.Fatalf("a poll %s after the last: want slow_down or authorization_pending, got %d %s", gap.Round(time.Millisecond), second.Status, second.Body)
	}
	w := newWallet(t)
	approveWithWallet(t, c, w, "", strings.ToLower(strings.ReplaceAll(d.UserCode, "-", "")), false).Expect(t, http.StatusOK)
	if resp := approveWithWallet(t, c, w, "", d.UserCode, false); resp.Status != http.StatusConflict {
		t.Errorf("approving twice: want 409, got %d %s", resp.Status, resp.Body)
	}
	var s gw.Session
	if err := poll(t, c, d.DeviceCode, nil).Expect(t, http.StatusOK).Decode(&s); err != nil {
		t.Fatal(err)
	}
	protect(t, c, s.AccessToken, s.RefreshToken)
	if !strings.EqualFold(s.Subject, w.Address()) || s.Namespace != gw.LobbyNamespace || s.RefreshToken == "" || s.APIKey != "" {
		t.Errorf("collected session subject %s namespace %q refresh %v api key %v", s.Subject, s.Namespace, s.RefreshToken != "", s.APIKey != "")
	}
	if st, _ := whoamiStatus(t, c, s.AccessToken); st != http.StatusOK {
		t.Errorf("the collected access token is refused: %d", st)
	}
	expectOAuthError(t, poll(t, c, d.DeviceCode, nil), "invalid_grant")
}

// TestDeviceLogin_deniedAndUnknown: a refusal is access_denied for the waiting
// machine; an unknown or empty code is invalid_grant.
func TestDeviceLogin_deniedAndUnknown(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	d := startLogin(t, c, map[string]any{})
	approveWithWallet(t, c, newWallet(t), "", d.UserCode, true).Expect(t, http.StatusOK)
	expectOAuthError(t, poll(t, c, d.DeviceCode, nil), "access_denied")
	if resp := approveWithWallet(t, c, newWallet(t), "", d.UserCode, false); resp.Status < 400 {
		t.Errorf("a denied login was approved afterwards: %d", resp.Status)
	}
	expectOAuthError(t, poll(t, c, strings.Repeat("A", deviceCodeLength), nil), "invalid_grant")
	expectOAuthError(t, poll(t, c, "", nil), "invalid_grant")
}

// TestDeviceLogin_approvalCostsAFreshSignature: the approval is the same
// signature check verify makes — a spent nonce or another wallet's signature
// approves nothing, and a malformed user code is refused.
func TestDeviceLogin_approvalCostsAFreshSignature(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	d := startLogin(t, c, map[string]any{})
	w := newWallet(t)
	req := signInReq(t, c, w, "", nil)
	if _, _, err := c.For(t).Verify(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	spent := postJSON(t, c, pathDeviceApprove, "", map[string]any{"user_code": d.UserCode, "message": req.Message, "signature": req.Signature})
	expectCode(t, spent, http.StatusUnauthorized, "AUTH_CHALLENGE_INVALID")
	other := signInReq(t, c, w, "", nil)
	forged, err := newWallet(t).Sign(other.Message)
	if err != nil {
		t.Fatal(err)
	}
	bad := postJSON(t, c, pathDeviceApprove, "", map[string]any{"user_code": d.UserCode, "message": other.Message, "signature": forged})
	expectCode(t, bad, http.StatusUnauthorized, "AUTH_SIGNATURE_INVALID")
	for _, code := range []string{"", "ABC", "ABCD-EFGH-IJKL"} {
		if resp := approveWithWallet(t, c, w, "", code, false); resp.Status != http.StatusBadRequest {
			t.Errorf("user code %q: want 400, got %d %s", code, resp.Status, resp.Body)
		}
	}
	expectOAuthError(t, poll(t, c, d.DeviceCode, nil), "authorization_pending")
}

// TestDeviceLogin_expires: nobody approves, and after ten minutes the waiting
// machine is told expired_token; approving it afterwards does nothing.
func TestDeviceLogin_expires(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	d := startLogin(t, c, map[string]any{})
	started := time.Now()
	eventually.Require(t, expiryPollEvery, pendingLife+2*expiryPollEvery, "the pending login to expire", func() (bool, error) {
		resp := poll(t, c, d.DeviceCode, nil)
		var body struct{ Error string }
		if err := resp.Decode(&body); err != nil {
			return false, eventually.Stop(err)
		}
		switch body.Error {
		case "expired_token":
			return true, nil
		case "authorization_pending":
			return false, nil
		default:
			return false, eventually.Stop(&gw.StatusError{Path: pathDeviceToken, Status: resp.Status, Body: string(resp.Body)})
		}
	})
	if took := time.Since(started); took < pendingLife-time.Minute {
		t.Errorf("the login expired after %s, before its ten minutes", took)
	}
	if resp := approveWithWallet(t, c, newWallet(t), "", d.UserCode, false); resp.Status < 400 {
		t.Errorf("an expired login was approved: %d", resp.Status)
	}
}

// TestDeviceLogin_malformedRequests: wrong method, bad JSON and an oversized
// body are client errors.
func TestDeviceLogin_malformedRequests(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	ct := http.Header{"Content-Type": {"application/json"}}
	for name, r := range map[string]gw.Req{
		"GET start":       {Path: pathDeviceStart},
		"GET token":       {Path: pathDeviceToken},
		"GET approve":     {Path: pathDeviceApprove},
		"start not JSON":  {Method: http.MethodPost, Path: pathDeviceStart, Header: ct, Body: []byte(`{"namespace":`)},
		"token not JSON":  {Method: http.MethodPost, Path: pathDeviceToken, Header: ct, Body: []byte(`[`)},
		"start oversized": {Method: http.MethodPost, Path: pathDeviceStart, Header: ct, Body: []byte(`{"device_label":"` + strings.Repeat("x", 8<<10) + `"}`)},
	} {
		if resp := c.MustSend(t, r); resp.Status < 400 || resp.Status >= 500 {
			t.Errorf("%s: want a 4xx, got %d %s", name, resp.Status, resp.Body)
		}
	}
}

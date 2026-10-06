//go:build e2e_fleet

package authdevices

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	// proofWindow: "iat must be within 60 seconds of the gateway's clock".
	proofWindow = 60 * time.Second
	// deviceRefreshPrefix marks a device-bound refresh token (docs/AUTH.md#signing-in-with-a-device).
	deviceRefreshPrefix = "dv1_"
)

func refreshWith(t testing.TB, c *gw.Client, token, namespace string, p *wallet.Proof) *gw.Response {
	t.Helper()
	body := map[string]any{"refresh_token": token, "namespace": namespace}
	if p != nil {
		body["device_proof"] = p
	}
	return postJSON(t, c, gw.PathRefresh, "", body)
}

// TestDeviceRefresh_needsAFreshSingleUseProof: a device-bound refresh token
// is marked dv1_ and renews only with the device's proof: fresh, single use,
// for this action on this token, from this device. A refused refresh spends
// nothing — the token and its rotation are still there for the real device
// (docs/AUTH.md#proving-the-device-on-later-requests).
func TestDeviceRefresh_needsAFreshSingleUseProof(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	d, other := gw.NewDevice(t, wallet.AlgEd25519), gw.NewDevice(t, wallet.AlgES256)
	s := signInSession(t, c, member(t, n, roleRuntime), n.Name, d)
	if !strings.HasPrefix(s.RefreshToken, deviceRefreshPrefix) {
		t.Errorf("a device-bound refresh token is not marked %s", deviceRefreshPrefix)
	}
	rt := s.RefreshToken
	for _, tc := range badRefreshProofs(t, d, other, n.Name, rt) {
		expectCodeNamed(t, tc.name, refreshWith(t, c, rt, n.Name, tc.p), http.StatusUnauthorized, tc.code)
	}
	good := proof(t, d, wallet.ProofRefresh, n.Name, rt)
	var next gw.Session
	if err := refreshWith(t, c, rt, n.Name, good).Expect(t, http.StatusOK).Decode(&next); err != nil {
		t.Fatal(err)
	}
	protect(t, c, next.AccessToken, next.RefreshToken)
	if next.DeviceID != "" && next.DeviceID != d.ID() {
		t.Errorf("the refreshed session names device %q", next.DeviceID)
	}
	replayed := proof(t, d, wallet.ProofRefresh, n.Name, next.RefreshToken)
	refreshWith(t, c, next.RefreshToken, n.Name, replayed).Expect(t, http.StatusOK)
	expectCodeNamed(t, "a spent proof id", refreshWith(t, c, next.RefreshToken, n.Name, replayed), http.StatusUnauthorized, "DEVICE_PROOF_INVALID")
}

type proofCase struct {
	name string
	p    *wallet.Proof
	code string
}

// badRefreshProofs is every way a refresh proof can be wrong: missing, stale,
// from the future, a short id, another action, token, namespace or device.
func badRefreshProofs(t testing.TB, d, other *wallet.Device, namespace, rt string) []proofCase {
	t.Helper()
	now := time.Now()
	at := func(when time.Time, id string) *wallet.Proof {
		p, err := d.ProofAt(wallet.ProofRefresh, namespace, rt, when, id)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	return []proofCase{
		{"no proof", nil, "DEVICE_PROOF_REQUIRED"},
		{"stale proof", at(now.Add(-2*proofWindow), randomB64(t, 24)), "DEVICE_PROOF_INVALID"},
		{"proof from the future", at(now.Add(2*proofWindow), randomB64(t, 24)), "DEVICE_PROOF_INVALID"},
		{"id too short", at(now, "tooShort"), "DEVICE_PROOF_INVALID"},
		{"proof for another action", proof(t, d, wallet.ProofRevoke, namespace, rt), "DEVICE_PROOF_INVALID"},
		{"proof over another token", proof(t, d, wallet.ProofRefresh, namespace, rt+"x"), "DEVICE_PROOF_INVALID"},
		{"proof for another namespace", proof(t, d, wallet.ProofRefresh, "default", rt), "DEVICE_PROOF_INVALID"},
		{"another device's proof", proof(t, other, wallet.ProofRefresh, namespace, rt), "DEVICE_PROOF_INVALID"},
	}
}

// expectCodeNamed is expectCode reporting with Errorf, so a table goes on.
func expectCodeNamed(t testing.TB, name string, resp *gw.Response, status int, code string) {
	t.Helper()
	if resp.Status != status || resp.ErrorCode() != code {
		t.Errorf("%s: want %d %s, got %d %.300s", name, status, code, resp.Status, resp.Body)
	}
}

// TestDeviceSession_actsNeedTheDevicesProof: from a device-bound session,
// ending a session and revoking a device take the device's proof over the
// target; a session bound to no device needs none (docs/AUTH.md#revoking-a-device).
func TestDeviceSession_actsNeedTheDevicesProof(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	w := member(t, n, roleRuntime)
	d, spare := gw.NewDevice(t, wallet.AlgEd25519), gw.NewDevice(t, wallet.AlgEd25519)
	bound := signInSession(t, c, w, n.Name, d)
	signInSession(t, c, w, n.Name, spare)
	plain := signInSession(t, c, w, n.Name, nil)
	sessions, _, err := c.For(t).Sessions(t.Context(), plain.AccessToken)
	if err != nil || len(sessions) < 3 {
		t.Fatalf("sessions: %v %+v", err, sessions)
	}
	target := sessions[0].ID
	resp, _ := c.For(t).EndSession(t.Context(), bound.AccessToken, target, nil)
	expectCode(t, resp, http.StatusUnauthorized, "DEVICE_PROOF_REQUIRED")
	resp, _ = c.For(t).EndSession(t.Context(), bound.AccessToken, target, proof(t, d, wallet.ProofEndSession, n.Name, "999999"))
	expectCode(t, resp, http.StatusUnauthorized, "DEVICE_PROOF_INVALID")
	resp, _ = c.For(t).RevokeDevice(t.Context(), bound.AccessToken, spare.ID(), nil)
	expectCode(t, resp, http.StatusUnauthorized, "DEVICE_PROOF_REQUIRED")
	resp, _ = c.For(t).RevokeDevice(t.Context(), bound.AccessToken, spare.ID(), proof(t, d, wallet.ProofRevoke, n.Name, d.ID()))
	expectCode(t, resp, http.StatusUnauthorized, "DEVICE_PROOF_INVALID")
	if _, err := c.For(t).RevokeDevice(t.Context(), plain.AccessToken, spare.ID(), nil); err != nil {
		t.Fatalf("a session bound to no device (only the wallet can make one) must revoke without a proof: %v", err)
	}
	if _, err := c.For(t).RevokeDevice(t.Context(), bound.AccessToken, spare.ID(), proof(t, d, wallet.ProofRevoke, n.Name, spare.ID())); err != nil {
		t.Fatalf("revoking again with a proper proof is safe to repeat: %v", err)
	}
}

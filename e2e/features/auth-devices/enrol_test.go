//go:build e2e_fleet

package authdevices

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	// maxLabelRunes: a label is truncated to 64 runes (core/pkg/gateway/auth/session_devices.go).
	maxLabelRunes = 64
	// jwkSizeLimit: a device key larger than this is refused.
	jwkSizeLimit = 1024
	coordBytes   = 32
	sigBytes     = 64
)

var b64 = base64.RawURLEncoding

// rawDeviceSignIn is a sign-in naming deviceID in the challenge and carrying
// an arbitrary key and device signature: the negative cases a real device
// never produces.
func rawDeviceSignIn(t testing.TB, c *gw.Client, w *wallet.EVM, namespace, deviceID string, key json.RawMessage, sign func(msg string) string) *gw.Response {
	t.Helper()
	ch, _, err := c.For(t).Challenge(t.Context(), gw.ChallengeRequest{Wallet: w.Address(), Namespace: namespace, DeviceID: deviceID})
	if err != nil {
		t.Fatalf("challenge naming device %q: %v", deviceID, err)
	}
	sig, err := w.Sign(ch.Message)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"message": ch.Message, "signature": sig}
	if key != nil {
		body["device_key"] = key
	}
	if sign != nil {
		body["device_signature"] = sign(ch.Message)
	}
	return postJSON(t, c, gw.PathVerify, "", body)
}

func signWith(t testing.TB, d *wallet.Device) func(string) string {
	return func(msg string) string {
		s, err := d.Sign([]byte(msg))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
}

func randomB64(t testing.TB, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b64.EncodeToString(b)
}

// TestDeviceSignIn_bindsTheSession: Ed25519, ES256 (r||s) and ES256 (DER) keys
// all enrol; the session and its tokens carry the device, the id is the RFC
// 7638 thumbprint, and no API key is handed out beside it (docs/AUTH.md#devices).
func TestDeviceSignIn_bindsTheSession(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	ed, es, der := gw.NewDevice(t, wallet.AlgEd25519), gw.NewDevice(t, wallet.AlgES256), gw.NewDevice(t, wallet.AlgES256)
	for _, d := range []*wallet.Device{ed, es} {
		w := member(t, n, roleRuntime)
		s := signInSession(t, c, w, n.Name, d)
		if s.DeviceID != d.ID() || s.APIKey != "" {
			t.Errorf("%s: device_id %q (want %s), api key handed out %v", d.Alg(), s.DeviceID, d.ID(), s.APIKey != "")
		}
		listed, _, err := c.For(t).Devices(t.Context(), s.AccessToken)
		if err != nil || len(listed) != 1 || listed[0].ID != d.ID() || !listed[0].Current {
			t.Errorf("%s: devices list %+v %v", d.Alg(), listed, err)
		}
	}
	w := member(t, n, roleRuntime)
	derSig := func(msg string) string {
		s, err := der.SignDER([]byte(msg))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	rawDeviceSignIn(t, c, w, n.Name, der.ID(), der.PublicJWK(), derSig).Expect(t, http.StatusOK)
}

// TestDeviceSignIn_keyAndSignatureRules: every malformed device binding is
// refused with the documented code, and nothing is enrolled.
func TestDeviceSignIn_keyAndSignatureRules(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	a, b := gw.NewDevice(t, wallet.AlgEd25519), gw.NewDevice(t, wallet.AlgES256)
	priv, err := a.PrivateJWK()
	if err != nil {
		t.Fatal(err)
	}
	identity := make([]byte, coordBytes)
	identity[0] = 1 // the Ed25519 identity point: order 1, "verifies" forged signatures
	smallOrder := `{"crv":"Ed25519","kty":"OKP","x":"` + b64.EncodeToString(identity) + `"}`
	offCurve := `{"crv":"P-256","kty":"EC","x":"` + randomB64(t, coordBytes) + `","y":"` + randomB64(t, coordBytes) + `"}`
	rsa := `{"e":"AQAB","kty":"RSA","n":"` + randomB64(t, 256) + `"}`
	huge := `{"crv":"Ed25519","kty":"OKP","pad":"` + strings.Repeat("A", jwkSizeLimit) + `","x":"` + randomB64(t, coordBytes) + `"}`
	forged := func(string) string {
		return b64.EncodeToString(append(append([]byte{}, identity...), make([]byte, coordBytes)...))
	}
	cases := []struct {
		name   string
		id     string
		key    json.RawMessage
		sign   func(string) string
		status int
		code   string
	}{
		{"thumbprint names another key", a.ID(), b.PublicJWK(), signWith(t, b), http.StatusBadRequest, "DEVICE_KEY_INVALID"},
		{"device named, no key", a.ID(), nil, signWith(t, a), http.StatusBadRequest, "DEVICE_KEY_INVALID"},
		{"key, no device named", "", a.PublicJWK(), signWith(t, a), http.StatusBadRequest, "DEVICE_KEY_INVALID"},
		{"device signed another message", a.ID(), a.PublicJWK(), func(string) string { return signWith(t, a)("another message") }, http.StatusUnauthorized, "DEVICE_SIGNATURE_INVALID"},
		{"garbage device signature", a.ID(), a.PublicJWK(), func(string) string { return "!!" }, http.StatusUnauthorized, "DEVICE_SIGNATURE_INVALID"},
		{"private key sent", a.ID(), priv, signWith(t, a), http.StatusBadRequest, "DEVICE_KEY_INVALID"},
		{"small-order Ed25519 key", wallet.Thumbprint(smallOrder), json.RawMessage(smallOrder), forged, http.StatusBadRequest, "DEVICE_KEY_INVALID"},
		{"P-256 point off the curve", wallet.Thumbprint(offCurve), json.RawMessage(offCurve), forged, http.StatusBadRequest, "DEVICE_KEY_INVALID"},
		{"RSA key", wallet.Thumbprint(rsa), json.RawMessage(rsa), forged, http.StatusBadRequest, "DEVICE_KEY_INVALID"},
		{"key over the size limit", wallet.Thumbprint(huge), json.RawMessage(huge), forged, http.StatusBadRequest, "DEVICE_KEY_INVALID"},
	}
	for _, tc := range cases {
		w := member(t, n, roleRuntime)
		resp := rawDeviceSignIn(t, c, w, n.Name, tc.id, tc.key, tc.sign)
		if resp.Status != tc.status || resp.ErrorCode() != tc.code {
			t.Errorf("%s: want %d %s, got %d %.300s", tc.name, tc.status, tc.code, resp.Status, resp.Body)
		}
	}
}

// TestDeviceSignIn_lobbyBindsNoDevice: the lobby is nobody's, so a device
// cannot be enrolled there (docs/AUTH.md "the lobby binds none").
func TestDeviceSignIn_lobbyBindsNoDevice(t *testing.T) {
	t.Parallel()
	c := harness.GW(t)
	d := gw.NewDevice(t, wallet.AlgEd25519)
	resp := rawDeviceSignIn(t, c, newWallet(t), "", d.ID(), d.PublicJWK(), signWith(t, d))
	expectCode(t, resp, http.StatusBadRequest, "DEVICE_KEY_INVALID")
}

// TestDeviceSignIn_labelIsCleaned: control and format characters are removed
// and a long label is cut to 64 characters, never refused.
func TestDeviceSignIn_labelIsCleaned(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	d := gw.NewDevice(t, wallet.AlgEd25519)
	w := member(t, n, roleRuntime)
	req := signInReq(t, c, w, n.Name, d)
	req.DeviceLabel = "\u202e\u0000phone\u200b" + strings.Repeat("é", 2*maxLabelRunes)
	var s gw.Session
	if err := postJSON(t, c, gw.PathVerify, "", req).Expect(t, http.StatusOK).Decode(&s); err != nil {
		t.Fatal(err)
	}
	protect(t, c, s.AccessToken, s.RefreshToken)
	listed, _, err := c.For(t).Devices(t.Context(), s.AccessToken)
	if err != nil || len(listed) != 1 {
		t.Fatalf("devices: %v %+v", err, listed)
	}
	label := listed[0].Label
	if strings.ContainsAny(label, "\u202e\u0000\u200b") || utf8.RuneCountInString(label) > maxLabelRunes || !strings.HasPrefix(label, "phone") {
		t.Fatalf("stored label %q (%d runes)", label, utf8.RuneCountInString(label))
	}
}

// TestDeviceSignIn_keyTakenAndTombstoned: a key enrolled for one account is
// refused to another (DEVICE_KEY_TAKEN); once revoked it is a tombstone that
// can never sign in again, by anyone (DEVICE_REVOKED).
func TestDeviceSignIn_keyTakenAndTombstoned(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	d := gw.NewDevice(t, wallet.AlgEd25519)
	alice, bob := member(t, n, roleRuntime), member(t, n, roleRuntime)
	s := signInSession(t, c, alice, n.Name, d)
	expectCode(t, rawDeviceSignIn(t, c, bob, n.Name, d.ID(), d.PublicJWK(), signWith(t, d)), http.StatusForbidden, "DEVICE_KEY_TAKEN")
	if _, err := c.For(t).RevokeDevice(t.Context(), s.AccessToken, d.ID(), proof(t, d, wallet.ProofRevoke, n.Name, d.ID())); err != nil {
		t.Fatalf("revoking the device from its own session: %v", err)
	}
	expectCode(t, rawDeviceSignIn(t, c, alice, n.Name, d.ID(), d.PublicJWK(), signWith(t, d)), http.StatusForbidden, "DEVICE_REVOKED")
	if resp := rawDeviceSignIn(t, c, bob, n.Name, d.ID(), d.PublicJWK(), signWith(t, d)); resp.Status != http.StatusForbidden {
		t.Errorf("another account enrolled a tombstoned key: %d %s", resp.Status, resp.Body)
	}
	signInSession(t, c, alice, n.Name, gw.NewDevice(t, wallet.AlgES256)) // a new device still enrols
}

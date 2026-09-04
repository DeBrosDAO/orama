package gateway

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// TestSetInternalAuthJWTHeaders_setsSubAndCustom verifies that the proxy
// helper writes the validated JWT subject and base64(JSON) custom claims to
// outbound headers (bug #215 wiring).
func TestSetInternalAuthJWTHeaders_setsSubAndCustom(t *testing.T) {
	h := http.Header{}
	claims := &auth.JWTClaims{
		Sub: "BNbN2RNQTsYrrywZCLnhV9j3hd38jwcRqfxBecZX7hDE",
		Custom: map[string]string{
			"tier":         "pro",
			"subscription": "active",
		},
	}
	setInternalAuthJWTHeaders(h, claims)

	if got := h.Get(HeaderInternalAuthJWTSub); got != claims.Sub {
		t.Errorf("Sub header = %q, want %q", got, claims.Sub)
	}
	if h.Get(HeaderInternalAuthJWTCustom) == "" {
		t.Error("Custom header is empty, expected base64 JSON blob")
	}
}

// TestSetInternalAuthJWTHeaders_nilClaimsNoOp guards the API-key path: when
// no JWT was used to authenticate, no JWT headers should be set.
func TestSetInternalAuthJWTHeaders_nilClaimsNoOp(t *testing.T) {
	h := http.Header{}
	setInternalAuthJWTHeaders(h, nil)
	if got := h.Get(HeaderInternalAuthJWTSub); got != "" {
		t.Errorf("Sub header = %q, want empty (nil claims)", got)
	}
	if got := h.Get(HeaderInternalAuthJWTCustom); got != "" {
		t.Errorf("Custom header = %q, want empty (nil claims)", got)
	}
}

// TestSetInternalAuthJWTHeaders_emptySubNotForwarded keeps malformed claims
// from polluting the outbound request.
func TestSetInternalAuthJWTHeaders_emptySubNotForwarded(t *testing.T) {
	h := http.Header{}
	setInternalAuthJWTHeaders(h, &auth.JWTClaims{Sub: "   "})
	if got := h.Get(HeaderInternalAuthJWTSub); got != "" {
		t.Errorf("Sub header = %q, want empty (whitespace-only sub)", got)
	}
}

// TestSetInternalAuthJWTHeaders_emptyCustomMapNotForwarded — empty maps are
// skipped to keep headers small and avoid sending `e30=` (base64 of "{}").
func TestSetInternalAuthJWTHeaders_emptyCustomMapNotForwarded(t *testing.T) {
	h := http.Header{}
	setInternalAuthJWTHeaders(h, &auth.JWTClaims{Sub: "0xabc", Custom: map[string]string{}})
	if got := h.Get(HeaderInternalAuthJWTCustom); got != "" {
		t.Errorf("Custom header = %q, want empty (empty custom map)", got)
	}
}

// TestClaimsFromInternalAuthHeaders_roundTrip is the end-to-end guarantee:
// what the main gateway sets via setInternalAuthJWTHeaders MUST be exactly
// what the namespace gateway recovers via claimsFromInternalAuthHeaders.
func TestClaimsFromInternalAuthHeaders_roundTrip(t *testing.T) {
	original := &auth.JWTClaims{
		Sub: "BNbN2RNQTsYrrywZCLnhV9j3hd38jwcRqfxBecZX7hDE",
		Custom: map[string]string{
			"tier":         "pro",
			"subscription": "active",
		},
	}
	h := http.Header{}
	setInternalAuthJWTHeaders(h, original)

	got := claimsFromInternalAuthHeaders(h, "anchat-test")
	if got == nil {
		t.Fatal("recovered claims is nil")
	}
	if got.Sub != original.Sub {
		t.Errorf("Sub = %q, want %q", got.Sub, original.Sub)
	}
	if got.Namespace != "anchat-test" {
		t.Errorf("Namespace = %q, want %q", got.Namespace, "anchat-test")
	}
	if !reflect.DeepEqual(got.Custom, original.Custom) {
		t.Errorf("Custom = %#v, want %#v", got.Custom, original.Custom)
	}
}

// TestClaimsFromInternalAuthHeaders_noSubReturnsNil — when no JWT was
// forwarded (caller used API key), recovery returns nil so the namespace
// gateway leaves ctxKeyJWT unset.
func TestClaimsFromInternalAuthHeaders_noSubReturnsNil(t *testing.T) {
	h := http.Header{}
	if got := claimsFromInternalAuthHeaders(h, "ns"); got != nil {
		t.Errorf("expected nil claims when no Sub header present, got %#v", got)
	}
}

// TestClaimsFromInternalAuthHeaders_invalidCustomIgnored — corrupt custom
// blob must not block recovery of the subject.
func TestClaimsFromInternalAuthHeaders_invalidCustomIgnored(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderInternalAuthJWTSub, "0xabc")
	h.Set(HeaderInternalAuthJWTCustom, "not-valid-base64!!!")

	got := claimsFromInternalAuthHeaders(h, "ns")
	if got == nil {
		t.Fatal("recovered claims is nil despite valid Sub header")
	}
	if got.Sub != "0xabc" {
		t.Errorf("Sub = %q, want %q", got.Sub, "0xabc")
	}
	if got.Custom != nil {
		t.Errorf("Custom = %#v, want nil (invalid blob should be silently dropped)", got.Custom)
	}
}

// TestStripInboundInternalAuthHeaders_removesAllForgedHeaders is the regression
// test for the security audit's CRITICAL finding (bug #215 follow-up). Without
// this strip, an external attacker could forge X-Internal-Auth-JWT-Sub on a
// public path and impersonate any wallet on the namespace gateway, which gates
// on source IP only.
func TestStripInboundInternalAuthHeaders_removesAllForgedHeaders(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderInternalAuthValidated, "true")
	h.Set(HeaderInternalAuthNamespace, "victim-namespace")
	h.Set(HeaderInternalAuthJWTSub, "0xVICTIM_WALLET")
	h.Set(HeaderInternalAuthJWTCustom, "ZXZpbA==") // base64("evil")
	// Unrelated headers MUST survive the strip.
	h.Set("X-Forwarded-For", "1.2.3.4")
	h.Set("Authorization", "Bearer some-token")

	stripInboundInternalAuthHeaders(h)

	for _, name := range []string{
		HeaderInternalAuthValidated,
		HeaderInternalAuthNamespace,
		HeaderInternalAuthJWTSub,
		HeaderInternalAuthJWTCustom,
	} {
		if got := h.Get(name); got != "" {
			t.Errorf("after strip, %s = %q, want empty", name, got)
		}
	}
	if got := h.Get("X-Forwarded-For"); got != "1.2.3.4" {
		t.Errorf("X-Forwarded-For lost: %q", got)
	}
	if got := h.Get("Authorization"); got != "Bearer some-token" {
		t.Errorf("Authorization lost: %q", got)
	}
}

// TestStripThenSet_attackerSubReplaced exercises the proxy-hop sequence:
// strip-then-set. An attacker-controlled JWT-Sub header MUST be replaced by
// the gateway-validated value (or removed entirely when the caller used an
// API key and validatedClaims is nil).
func TestStripThenSet_attackerSubReplaced(t *testing.T) {
	t.Run("validated_jwt_overwrites_attacker_sub", func(t *testing.T) {
		h := http.Header{}
		h.Set(HeaderInternalAuthJWTSub, "0xATTACKER")

		stripInboundInternalAuthHeaders(h)
		// Mimic the proxy hop: gateway authenticated user as 0xLEGIT.
		h.Set(HeaderInternalAuthValidated, "true")
		h.Set(HeaderInternalAuthNamespace, "ns")
		setInternalAuthJWTHeaders(h, &auth.JWTClaims{Sub: "0xLEGIT"})

		if got := h.Get(HeaderInternalAuthJWTSub); got != "0xLEGIT" {
			t.Errorf("Sub = %q, want %q (attacker value should be overwritten)", got, "0xLEGIT")
		}
	})

	t.Run("api_key_path_drops_attacker_sub", func(t *testing.T) {
		h := http.Header{}
		h.Set(HeaderInternalAuthJWTSub, "0xATTACKER")

		stripInboundInternalAuthHeaders(h)
		// Mimic the proxy hop: gateway authenticated user via API key
		// (validatedClaims is nil).
		h.Set(HeaderInternalAuthValidated, "true")
		h.Set(HeaderInternalAuthNamespace, "ns")
		setInternalAuthJWTHeaders(h, nil)

		if got := h.Get(HeaderInternalAuthJWTSub); got != "" {
			t.Errorf("Sub = %q, want empty (attacker value must not survive API-key auth)", got)
		}
	})
}

// feat-384: gateway-owned claims must not survive the internal-auth hop.
//
// A namespace gateway trusts X-Internal-Auth-* headers on a source-IP check
// covering loopback and the whole WireGuard /8 — and a serverless function can
// reach a namespace gateway directly via http_fetch. So anything on the mesh,
// including another tenant, could otherwise assert `device_fp` for any account
// by forging a header. That is precisely the forgery the device claim exists to
// prevent, so these keys are stripped here and only ever come from a verified
// signature.
func TestClaimsFromInternalAuthHeaders_stripsGatewayOwnedClaims(t *testing.T) {
	forged, _ := json.Marshal(map[string]string{
		"device_fp":    "victim-device-fingerprint",
		"device_since": "0",
		"scopes":       "admin",
		"account_id":   "acct-legitimate",
	})

	h := http.Header{}
	h.Set(HeaderInternalAuthJWTSub, "0xVICTIM")
	h.Set(HeaderInternalAuthJWTCustom, base64.StdEncoding.EncodeToString(forged))

	claims := claimsFromInternalAuthHeaders(h, "victim-namespace")
	if claims == nil {
		t.Fatal("expected claims for a forwarded subject")
	}

	for _, forgedKey := range []string{"device_fp", "device_since", "scopes"} {
		if v, ok := claims.Custom[forgedKey]; ok {
			t.Errorf("gateway-owned claim %q survived the internal hop as %q — a mesh-local caller could forge it", forgedKey, v)
		}
	}
	// Application claims are untouched: this filter is about gateway-owned
	// keys, not about distrusting the hop wholesale.
	if claims.Custom["account_id"] != "acct-legitimate" {
		t.Errorf("an application claim was dropped: %v", claims.Custom)
	}
}

// A header set consisting ONLY of forged gateway claims must yield no custom
// claims at all, rather than an empty-but-present map that could read as
// "device attribution was evaluated".
func TestClaimsFromInternalAuthHeaders_allForgedYieldsNoCustomClaims(t *testing.T) {
	forged, _ := json.Marshal(map[string]string{"device_fp": "forged"})

	h := http.Header{}
	h.Set(HeaderInternalAuthJWTSub, "0xVICTIM")
	h.Set(HeaderInternalAuthJWTCustom, base64.StdEncoding.EncodeToString(forged))

	claims := claimsFromInternalAuthHeaders(h, "ns")
	if len(claims.Custom) != 0 {
		t.Errorf("expected no custom claims, got %v", claims.Custom)
	}
}

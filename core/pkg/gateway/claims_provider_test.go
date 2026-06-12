package gateway

import (
	"testing"
)

// Bugboard #548: the claims-provider sanitizer is the security boundary —
// a namespace function must NOT be able to forge reserved claims, inject
// non-string values, or blow the size budget.

func TestSanitizeProviderClaims_happyPath(t *testing.T) {
	out := sanitizeProviderClaims([]byte(`{"account_id":"u-123","tier":"pro"}`))
	if out["account_id"] != "u-123" || out["tier"] != "pro" {
		t.Fatalf("expected additive claims, got %v", out)
	}
}

func TestSanitizeProviderClaims_dropsReservedKeys(t *testing.T) {
	// A malicious provider tries to override sub/exp/namespace — must be dropped.
	out := sanitizeProviderClaims([]byte(`{"sub":"0xATTACKER","exp":"9999999999","namespace":"evil","account_id":"u-1"}`))
	for _, k := range []string{"sub", "exp", "namespace"} {
		if _, present := out[k]; present {
			t.Errorf("reserved key %q must be dropped, got %v", k, out)
		}
	}
	if out["account_id"] != "u-1" {
		t.Errorf("legitimate claim dropped: %v", out)
	}
}

func TestSanitizeProviderClaims_nonStringValuesDropped(t *testing.T) {
	out := sanitizeProviderClaims([]byte(`{"account_id":"u-1","num":5,"obj":{"a":1},"arr":[1],"ok":"yes"}`))
	if len(out) != 2 || out["account_id"] != "u-1" || out["ok"] != "yes" {
		t.Errorf("non-string values must be dropped; got %v", out)
	}
}

func TestSanitizeProviderClaims_failOpenOnGarbage(t *testing.T) {
	for _, bad := range [][]byte{
		nil,
		[]byte(``),
		[]byte(`not json`),
		[]byte(`[1,2,3]`),         // array, not object
		[]byte(`"just a string"`), // scalar
		[]byte(`{}`),              // empty object
		[]byte(`{"ok":true,"result":{"account_id":"u"}}`), // Ack envelope (wrong shape) → no top-level string claims
	} {
		if got := sanitizeProviderClaims(bad); got != nil {
			t.Errorf("garbage %q must yield nil (fail-open), got %v", bad, got)
		}
	}
}

func TestSanitizeProviderClaims_countAndSizeCapped(t *testing.T) {
	// Way more than maxCustomClaims string entries.
	buf := []byte("{")
	for i := 0; i < maxCustomClaims+20; i++ {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, []byte(`"k`)...)
		buf = append(buf, []byte(itoa(i))...)
		buf = append(buf, []byte(`":"v"`)...)
	}
	buf = append(buf, '}')
	out := sanitizeProviderClaims(buf)
	if len(out) > maxCustomClaims {
		t.Errorf("claim count not capped: got %d, max %d", len(out), maxCustomClaims)
	}

	// Oversized total payload → rejected outright.
	big := make([]byte, maxCustomClaimsBytes+10)
	for i := range big {
		big[i] = 'a'
	}
	if got := sanitizeProviderClaims(big); got != nil {
		t.Errorf("oversized payload must be rejected, got %v", got)
	}
}

func TestResolveClaims_nilInvokerOrEmptyArgs(t *testing.T) {
	p := newJWTClaimsProvider(nil, nil) // nil invoker disables the hook
	if got := p.ResolveClaims(nil, "0xW", "ns"); got != nil {
		t.Errorf("nil invoker must yield nil claims, got %v", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

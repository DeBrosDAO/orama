package gateway

import (
	"context"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/registry"
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

// fakeClaimsInvoker is a programmable claimsInvoker for the retry tests
// (bugboard #143). Each entry in results is returned on the corresponding
// attempt; calls tracks how many times Invoke ran.
type fakeClaimsInvoker struct {
	results []invokeResult
	calls   int
}

type invokeResult struct {
	resp *serverless.InvokeResponse
	err  error
}

func (f *fakeClaimsInvoker) Invoke(_ context.Context, _ *serverless.InvokeRequest) (*serverless.InvokeResponse, error) {
	i := f.calls
	f.calls++
	if i >= len(f.results) {
		i = len(f.results) - 1 // repeat the last programmed result
	}
	return f.results[i].resp, f.results[i].err
}

func successResp(body string) *serverless.InvokeResponse {
	return &serverless.InvokeResponse{Status: serverless.InvocationStatusSuccess, Output: []byte(body)}
}

// newProviderWithFake builds a jwtClaimsProvider wired to a fake invoker.
func newProviderWithFake(f *fakeClaimsInvoker) *jwtClaimsProvider {
	p := newJWTClaimsProvider(nil, nil)
	p.invoker = f // inject the fake seam directly
	return p
}

// (a) A cold-WASM ErrWASMFetchTimeout on attempt 1 must be retried; the 2nd
// attempt succeeds and its claims are returned. This is the core #143 fix —
// without the retry, the transient cold start fragments the user's devices.
func TestResolveClaims_retriesTransientThenSucceeds(t *testing.T) {
	f := &fakeClaimsInvoker{results: []invokeResult{
		{err: fmt.Errorf("wrap: %w", serverless.ErrWASMFetchTimeout)},
		{resp: successResp(`{"account_id":"uuid-X"}`)},
	}}
	p := newProviderWithFake(f)

	out := p.ResolveClaims(context.Background(), "0xW", "ns")
	if out["account_id"] != "uuid-X" {
		t.Fatalf("expected account_id after retry, got %v", out)
	}
	if f.calls != 2 {
		t.Errorf("expected exactly 2 attempts (retry after transient), got %d", f.calls)
	}
}

// (b) A transient failure on EVERY attempt exhausts the retry budget and fails
// open (nil) — auth never breaks because the provider is down.
func TestResolveClaims_transientAllAttemptsFailsOpen(t *testing.T) {
	f := &fakeClaimsInvoker{results: []invokeResult{
		{err: fmt.Errorf("cold: %w", serverless.ErrWASMFetchTimeout)},
	}}
	p := newProviderWithFake(f)

	if out := p.ResolveClaims(context.Background(), "0xW", "ns"); out != nil {
		t.Fatalf("expected nil (fail-open) after exhausting retries, got %v", out)
	}
	if f.calls != claimsProviderMaxAttempts {
		t.Errorf("expected %d attempts, got %d", claimsProviderMaxAttempts, f.calls)
	}
}

// (c) ErrFunctionNotFound (no provider deployed) must NOT retry — it's the
// normal no-claims case for most namespaces. Exactly one attempt, nil result.
func TestResolveClaims_functionNotFoundNoRetry(t *testing.T) {
	f := &fakeClaimsInvoker{results: []invokeResult{
		{err: fmt.Errorf("registry: %w", registry.ErrFunctionNotFound)},
	}}
	p := newProviderWithFake(f)

	if out := p.ResolveClaims(context.Background(), "0xW", "ns"); out != nil {
		t.Fatalf("expected nil for not-found, got %v", out)
	}
	if f.calls != 1 {
		t.Errorf("ErrFunctionNotFound must not retry; expected 1 attempt, got %d", f.calls)
	}
}

// (d) A clean non-success result (the app's own logic returned error status) is
// NOT retried — that's a deliberate outcome, not an infra blip. One attempt, nil.
func TestResolveClaims_nonSuccessNoRetry(t *testing.T) {
	f := &fakeClaimsInvoker{results: []invokeResult{
		{resp: &serverless.InvokeResponse{Status: serverless.InvocationStatusError}},
	}}
	p := newProviderWithFake(f)

	if out := p.ResolveClaims(context.Background(), "0xW", "ns"); out != nil {
		t.Fatalf("expected nil for non-success result, got %v", out)
	}
	if f.calls != 1 {
		t.Errorf("non-success result must not retry; expected 1 attempt, got %d", f.calls)
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

// A namespace claims provider must not be able to mint device claims.
//
// The provider is tenant-deployed WASM. If it could set device_fp, the forgery
// would be handed to the exact layer that is supposed to be CHECKING it — an
// app compromise would silently become a device-attribution bypass.
func TestSanitizeProviderClaims_dropsDeviceClaims(t *testing.T) {
	raw := []byte(`{"device_fp":"forged","device_since":"0","account_id":"acct-1"}`)

	out := sanitizeProviderClaims(raw)

	if _, ok := out["device_fp"]; ok {
		t.Error("a namespace provider injected device_fp — device attribution would be forgeable by the app itself")
	}
	if _, ok := out["device_since"]; ok {
		t.Error("a namespace provider injected device_since")
	}
	if out["account_id"] != "acct-1" {
		t.Errorf("the provider's own claims were dropped: %v", out)
	}
}

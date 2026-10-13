package hostfunctions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// feat-9 — turn_credentials host fn.
//
// Mirrors the /v1/webrtc/turn/credentials HTTP endpoint so WASM
// functions can mint per-namespace TURN credentials without a round-trip
// back through HTTP. These tests pin the contract that external SDK
// helpers and AnChat's call setup logic depend on.

func TestTurnCredentials_returnsConfiguredEnvelopeWhenSecretSet(t *testing.T) {
	// Happy path: TURN configured → returns full envelope with username,
	// password, ttl, uris.
	h := &HostFunctions{
		turnDomain:       "turn.example.com",
		turnSecret:       "deadbeef-shared-secret-for-hmac",
		stealthCDNDomain: "",
	}
	invCtx := invocationCtx(&serverless.InvocationContext{Namespace: "test-ns"})

	raw, err := h.TurnCredentials(invCtx)
	if err != nil {
		t.Fatalf("TurnCredentials: %v", err)
	}
	var env turnCredentialsEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	if !env.Configured {
		t.Error("Configured = false; want true when turnSecret is set")
	}
	if env.Namespace != "test-ns" {
		t.Errorf("Namespace = %q; want test-ns (namespace must be derived from invCtx, not caller-controlled)",
			env.Namespace)
	}
	if env.Username == "" {
		t.Error("Username empty; want HMAC-derived value")
	}
	if env.Password == "" {
		t.Error("Password empty; want HMAC-derived value")
	}
	if env.TTL != int(turnCredentialTTL.Seconds()) {
		t.Errorf("TTL = %d; want %d (matches HTTP endpoint)", env.TTL, int(turnCredentialTTL.Seconds()))
	}
	// Username MUST contain the namespace per pkg/turn HMAC contract —
	// this is what the TURN server uses to scope the credential.
	if !strings.Contains(env.Username, "test-ns") {
		t.Errorf("Username %q must contain the namespace for TURN server-side scope check", env.Username)
	}
}

func TestTurnCredentials_returnsURIsForDomain(t *testing.T) {
	// Verify URI assembly mirrors the HTTP endpoint exactly — same three
	// URIs (udp + tcp + tls5349) when only turnDomain is set.
	h := &HostFunctions{
		turnDomain: "turn.example.com",
		turnSecret: "secret",
	}
	invCtx := invocationCtx(&serverless.InvocationContext{Namespace: "ns"})

	raw, _ := h.TurnCredentials(invCtx)
	var env turnCredentialsEnvelope
	_ = json.Unmarshal(raw, &env)

	if len(env.URIs) != 3 {
		t.Fatalf("URIs count = %d; want 3 (udp+tcp+tls5349)", len(env.URIs))
	}
	want := []string{
		"turn:turn.example.com:3478?transport=udp",
		"turn:turn.example.com:3478?transport=tcp",
		"turns:turn.example.com:5349",
	}
	for i, w := range want {
		if env.URIs[i] != w {
			t.Errorf("URIs[%d] = %q; want %q", i, env.URIs[i], w)
		}
	}
}

func TestTurnCredentials_stealthCDNAppendsTurns443(t *testing.T) {
	// Stealth: turns:<stealthCDNDomain>:443 must be APPENDED to the
	// regular URI list. Used in restricted regions where regular TURN
	// ports are blocked; the SNI router serves it as ordinary HTTPS on
	// :443 so DPI can't distinguish it. Critical for AnChat's restricted-
	// region UX (bugboard #411 + stealth TURN plan 4).
	h := &HostFunctions{
		turnDomain:       "turn.example.com",
		turnSecret:       "secret",
		stealthCDNDomain: "cdn.example.com",
	}
	invCtx := invocationCtx(&serverless.InvocationContext{Namespace: "ns"})

	raw, _ := h.TurnCredentials(invCtx)
	var env turnCredentialsEnvelope
	_ = json.Unmarshal(raw, &env)

	if len(env.URIs) != 4 {
		t.Fatalf("URIs count = %d; want 4 (3 regular + 1 stealth)", len(env.URIs))
	}
	stealth := env.URIs[3]
	want := "turns:cdn.example.com:443"
	if stealth != want {
		t.Errorf("stealth URI = %q; want %q", stealth, want)
	}
}

func TestTurnCredentials_notConfiguredWhenSecretEmpty(t *testing.T) {
	// Back-compat / portability: when TURN isn't configured on this
	// gateway, return a structured {configured:false} envelope — NOT a
	// Go error. Same shape contract as PushSend's silent-noop when push
	// isn't configured. Lets the same WASM function run unchanged on
	// dev environments without TURN.
	h := &HostFunctions{
		turnSecret: "",
	}
	invCtx := invocationCtx(&serverless.InvocationContext{Namespace: "ns"})

	raw, err := h.TurnCredentials(invCtx)
	if err != nil {
		t.Fatalf("TurnCredentials must NOT return Go error when TURN unconfigured; got %v", err)
	}
	var env turnCredentialsEnvelope
	_ = json.Unmarshal(raw, &env)
	if env.Configured {
		t.Error("Configured = true; want false when turnSecret is empty (caller relies on this to fall back to STUN-only)")
	}
	if env.Namespace != "ns" {
		t.Errorf("Namespace = %q; want ns (still populated for logging context)", env.Namespace)
	}
	if env.Username != "" || env.Password != "" {
		t.Error("Username/Password must be empty when not configured (no credentials to leak)")
	}
}

func TestTurnCredentials_errorsWhenNoNamespaceInContext(t *testing.T) {
	// Defensive: serverless invocation should always have a namespace.
	// If not, return a Go error rather than producing TURN credentials
	// for an empty namespace (which would be a security bug — TURN
	// HMAC username is the namespace + ts, so "" would shadow any
	// real-namespace creds at the TURN server's auth check).
	h := &HostFunctions{turnSecret: "secret"}
	// no SetInvocationContext

	_, err := h.TurnCredentials(context.Background())
	if err == nil {
		t.Fatal("no invocation context: must return error (avoid empty-namespace credentials)")
	}
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("error %q should mention namespace for caller diagnostics", err.Error())
	}
}

func TestTurnCredentials_credentialsAreNamespaceScoped(t *testing.T) {
	// Two different namespaces issued through the SAME host fn instance
	// MUST get distinct credentials. Catches a regression where the
	// namespace gets cached at host-fn construction instead of read
	// per-invocation from invCtx.
	h := &HostFunctions{
		turnDomain: "turn.example.com",
		turnSecret: "shared-secret",
	}

	rawA, _ := h.TurnCredentials(invocationCtx(&serverless.InvocationContext{Namespace: "ns-a"}))
	var envA turnCredentialsEnvelope
	_ = json.Unmarshal(rawA, &envA)

	rawB, _ := h.TurnCredentials(invocationCtx(&serverless.InvocationContext{Namespace: "ns-b"}))
	var envB turnCredentialsEnvelope
	_ = json.Unmarshal(rawB, &envB)

	if envA.Username == envB.Username {
		t.Error("ns-a and ns-b got identical username — namespace not flowing per-invocation")
	}
	if envA.Password == envB.Password {
		t.Error("ns-a and ns-b got identical password — credentials not namespace-scoped (security bug)")
	}
}

// buildTURNURIs unit tests — the pure helper used by both this host fn
// and the HTTP endpoint. Cheap regression coverage.

func TestBuildTURNURIs_emptyDomainNoURIs(t *testing.T) {
	if got := buildTURNURIs("", ""); len(got) != 0 {
		t.Errorf("empty domain + empty stealth: want 0 URIs, got %d (%v)", len(got), got)
	}
}

func TestBuildTURNURIs_stealthOnlyOmitsRegularURIs(t *testing.T) {
	// Edge: operator configured stealth but not regular TURN. Returns
	// ONLY the stealth URI — caller falls back to STUN if they can't
	// reach it. Don't pretend the regular TURN exists.
	got := buildTURNURIs("", "cdn.example.com")
	if len(got) != 1 {
		t.Fatalf("want 1 stealth-only URI, got %d (%v)", len(got), got)
	}
	if got[0] != "turns:cdn.example.com:443" {
		t.Errorf("stealth URI mismatch: %q", got[0])
	}
}

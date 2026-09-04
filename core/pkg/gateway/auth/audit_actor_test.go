package auth

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// The JWT the API-key exchange mints carries the key ITSELF as its subject, so
// a handler that recorded the subject verbatim would put a live credential in a
// Raft-replicated table that every owner of the namespace can read back over
// GET /v1/audit.

func TestRedactSubject_keepsAWallet(t *testing.T) {
	const wallet = "0x1234567890abcdef1234567890abcdef12345678"
	if got := RedactSubject(wallet); got != wallet {
		t.Errorf("RedactSubject(%q) = %q, want it unchanged — a wallet is an identity", wallet, got)
	}
}

func TestRedactSubject_doesNotReturnTheCredential(t *testing.T) {
	const key = "orama_ak_3kFj9sPqR2vX7mNb_1a2b3c"

	got := RedactSubject(key)
	if strings.Contains(got, "3kFj9sPqR2vX7mNb") {
		t.Fatalf("RedactSubject(%q) = %q — the key is still in it", key, got)
	}
	if got == key {
		t.Fatalf("the key was recorded verbatim")
	}
	if !strings.HasPrefix(got, "key:") {
		t.Errorf("RedactSubject = %q, want a key: fingerprint", got)
	}
}

// The fingerprint is what groups one caller's events together, so the same key
// has to produce the same one and two keys must not collide.
func TestRedactSubject_isStableAndDistinct(t *testing.T) {
	first := RedactSubject("orama_ak_aaaaaaaaaaaaaaaa_1a2b3c")
	again := RedactSubject("orama_ak_aaaaaaaaaaaaaaaa_1a2b3c")
	other := RedactSubject("orama_ak_bbbbbbbbbbbbbbbb_4d5e6f")

	if first != again {
		t.Errorf("the same key fingerprinted two ways: %q and %q", first, again)
	}
	if first == other {
		t.Errorf("two different keys share the fingerprint %q", first)
	}
}

func TestRedactSubject_leavesAnEmptySubjectEmpty(t *testing.T) {
	if got := RedactSubject("   "); got != "" {
		t.Errorf("RedactSubject(blank) = %q, want empty — no actor is not a wrong actor", got)
	}
}

func TestActorFromRequest_readsTheJWTSubject(t *testing.T) {
	const wallet = "0x1234567890abcdef1234567890abcdef12345678"
	req := httptest.NewRequest("POST", "/v1/functions", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.JWT, &JWTClaims{Sub: wallet}))

	if got := ActorFromRequest(req); got != wallet {
		t.Errorf("ActorFromRequest = %q, want %q", got, wallet)
	}
}

func TestActorFromRequest_redactsAKeySubject(t *testing.T) {
	const key = "orama_ak_3kFj9sPqR2vX7mNb_1a2b3c"
	req := httptest.NewRequest("POST", "/v1/functions", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.JWT, &JWTClaims{Sub: key}))

	got := ActorFromRequest(req)
	if strings.Contains(got, "3kFj9sPqR2vX7mNb") || got == key {
		t.Fatalf("ActorFromRequest = %q — the credential reached the audit trail", got)
	}
}

func TestActorFromRequest_withoutAJWTHasNoActor(t *testing.T) {
	if got := ActorFromRequest(httptest.NewRequest("POST", "/v1/functions", nil)); got != "" {
		t.Errorf("ActorFromRequest = %q, want empty", got)
	}
	if got := ActorFromRequest(nil); got != "" {
		t.Errorf("ActorFromRequest(nil) = %q, want empty", got)
	}
}

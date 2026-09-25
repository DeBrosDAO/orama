package auth

import (
	"context"
	"testing"
	"time"
)

// An open socket was authorized by claims verified once. Revoked is what the
// socket sweeper asks afterwards, and it must see every kind of revocation a
// fresh verification would.
func TestRevoked_seesEveryRevocationOfVerifiedClaims(t *testing.T) {
	s, _ := serviceWithRevocations(t)
	ctx := context.Background()
	now := time.Now().Unix()

	session := &JWTClaims{Sub: "0xwallet", Jti: "session-1", Iat: now, Exp: now + 900}
	if s.Revoked(session) {
		t.Fatal("claims nobody revoked were reported revoked")
	}
	if err := s.RevokeSession(ctx, session); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if !s.Revoked(session) {
		t.Error("an ended session's claims were not reported revoked")
	}

	other := &JWTClaims{Sub: "0xother", Jti: "session-2", Iat: now, Exp: now + 900}
	if err := s.RevokeAllSessions(ctx, "0xother"); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	if !s.Revoked(other) {
		t.Error("claims issued before their subject's revocation were not reported revoked")
	}

	rawKey := "ak_runtime:acme"
	exchanged := &JWTClaims{Sub: rawKey, Jti: "session-3", Iat: now, Exp: now + 900}
	if err := s.revocations.RevokeSubject(ctx, s.HashAPIKey(rawKey), "key revoked", time.Hour); err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}
	if !s.Revoked(exchanged) {
		t.Error("claims exchanged from a revoked key were not reported revoked")
	}

	if s.Revoked(nil) {
		t.Error("no claims were reported revoked")
	}
}

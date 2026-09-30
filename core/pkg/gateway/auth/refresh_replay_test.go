package auth

import (
	"context"
	"errors"
	"testing"
)

// The replay tripwire fired only when two requests raced the rotation. A token
// presented after it had been rotated away — the ordinary shape of a stolen one
// — was refused as "invalid or expired" and recorded as an ordinary failed
// refresh, so the audit trail never said anyone had tried (docs/AUTH.md: "the
// second attempt fails and is recorded").
func TestRefreshToken_aSpentTokenIsAReplay(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()
	_, first, _, err := s.IssueTokens(ctx, deviceOwner, "anchat")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, _, _, err := s.RefreshToken(ctx, first, "anchat", nil); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// The lost-response grace slot is a recovery, not a replay.
	if _, _, _, _, err := s.RefreshToken(ctx, first, "anchat", nil); err != nil {
		t.Fatalf("the grace slot: %v", err)
	}

	_, _, _, _, err = s.RefreshToken(ctx, first, "anchat", nil)
	if !errors.Is(err, ErrRefreshTokenReplay) {
		t.Fatalf("a rotated token, presented after its grace: %v, want ErrRefreshTokenReplay", err)
	}
}

func TestRefreshToken_aLoggedOutTokenIsAReplayAndNeverRecovers(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()
	_, first, _, err := s.IssueTokens(ctx, deviceOwner, "anchat")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := s.RevokeToken(ctx, "anchat", first, false, ""); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, _, _, err := s.RefreshToken(ctx, first, "anchat", nil); !errors.Is(err, ErrRefreshTokenReplay) {
		t.Errorf("a logged-out token: %v, want ErrRefreshTokenReplay", err)
	}
}

// A token nobody issued is a mistake, not a theft: it stays the plain refusal
// and is not recorded as a replay.
func TestRefreshToken_anUnknownTokenIsNotAReplay(t *testing.T) {
	s, _, _ := realRegistry(t)

	_, _, _, _, err := s.RefreshToken(context.Background(), "never-issued", "anchat", nil)
	if err == nil {
		t.Fatal("an unknown refresh token was accepted")
	}
	if errors.Is(err, ErrRefreshTokenReplay) || errors.Is(err, ErrRefreshTransient) {
		t.Errorf("an unknown token: %v, want the plain invalid-or-expired refusal", err)
	}
}

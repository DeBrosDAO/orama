package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func insertRefresh(t *testing.T, db *sql.DB, subject, token string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO refresh_tokens(namespace_id, subject, token, expires_at)
		VALUES (10, ?, ?, datetime('now', '+1 day'))`, subject, refreshTokenHash(token)); err != nil {
		t.Fatal(err)
	}
}

func revoked(t *testing.T, db *sql.DB, token string) bool {
	t.Helper()
	var at sql.NullString
	if err := db.QueryRow(`SELECT revoked_at FROM refresh_tokens WHERE token = ?`, refreshTokenHash(token)).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at.Valid
}

// A logout of every session that also names the caller's own refresh token
// used to revoke only that one token (stagenet e2e, 2026-09-30: the wallet's
// other session kept refreshing). Another wallet's session is untouched.
func TestRevokeToken_allWithATokenEndsEverySession(t *testing.T) {
	s, wrapped, _ := realRegistry(t)
	db := wrapped.db
	insertRefresh(t, db, "0xwallet", "first")
	insertRefresh(t, db, "0xwallet", "second")
	insertRefresh(t, db, "0xbystander", "theirs")

	if err := s.RevokeToken(context.Background(), "anchat", "first", true, "0xwallet"); err != nil {
		t.Fatalf("logout all: %v", err)
	}
	if !revoked(t, db, "first") || !revoked(t, db, "second") {
		t.Error("a session of the wallet survived logout all")
	}
	if revoked(t, db, "theirs") {
		t.Error("another wallet's session was revoked")
	}
}

func TestRevokeToken_oneTokenEndsOnlyThatSession(t *testing.T) {
	s, wrapped, _ := realRegistry(t)
	db := wrapped.db
	insertRefresh(t, db, "0xwallet", "first")
	insertRefresh(t, db, "0xwallet", "second")

	if err := s.RevokeToken(context.Background(), "anchat", "first", false, ""); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !revoked(t, db, "first") || revoked(t, db, "second") {
		t.Error("a single logout did not end exactly its own session")
	}
}

func TestRevokeToken_nothingNamedIsTheCallersMistake(t *testing.T) {
	s, _, _ := realRegistry(t)
	if err := s.RevokeToken(context.Background(), "anchat", "", false, ""); !errors.Is(err, ErrNothingToRevoke) {
		t.Fatalf("err %v, want ErrNothingToRevoke", err)
	}
}

package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

// issueSession puts one refresh-token row in the namespace, the way IssueTokens
// does, without needing a signing key to mint the access token beside it.
func issueSession(t *testing.T, s *Service, subject string, expires string) {
	t.Helper()
	db := s.orm.Database()
	if _, err := db.Query(context.Background(),
		`INSERT INTO refresh_tokens(namespace_id, subject, token, audience, expires_at)
		 VALUES (10, ?, ?, 'gateway', ?)`,
		subject, subject+"-"+expires, expires); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func TestListSessions_showsOnlyTheCallersLiveOnes(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()

	future := time.Now().Add(30 * 24 * time.Hour).UTC().Format(sqliteTime)
	past := time.Now().Add(-time.Hour).UTC().Format(sqliteTime)

	issueSession(t, s, "0xowner", future)
	issueSession(t, s, "0xowner", past)
	issueSession(t, s, "0xsomebodyelse", future)

	// One more for the owner, revoked.
	issueSession(t, s, "0xowner", future+"z")
	if _, err := db.db.Exec(`UPDATE refresh_tokens SET revoked_at = datetime('now') WHERE expires_at = ?`, future+"z"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	sessions, err := s.ListSessions(ctx, "anchat", "0xowner")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("%d sessions, want only the live one of this subject: %+v", len(sessions), sessions)
	}
	if sessions[0].Subject != "0xowner" {
		t.Errorf("subject = %q", sessions[0].Subject)
	}
	if sessions[0].ExpiresAt.IsZero() {
		t.Error("the session has no expiry, so nothing says when it stops on its own")
	}
}

// A list endpoint that hands back the credential it is listing turns a
// fifteen-minute access token into a thirty-day one.
func TestListSessions_neverReturnsTheToken(t *testing.T) {
	s, _, _ := realRegistry(t)
	future := time.Now().Add(time.Hour).UTC().Format(sqliteTime)
	issueSession(t, s, "0xowner", future)

	sessions, err := s.ListSessions(context.Background(), "anchat", "0xowner")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("%d sessions", len(sessions))
	}
	for _, field := range []string{sessions[0].Subject, sessions[0].Audience} {
		if strings.Contains(field, "0xowner-"+future) {
			t.Fatalf("a returned field carries the refresh token: %q", field)
		}
	}
}

func TestEndSession_endsOneAndOnlyTheCallersOwn(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()
	future := time.Now().Add(time.Hour).UTC().Format(sqliteTime)

	issueSession(t, s, "0xowner", future)
	issueSession(t, s, "0xsomebodyelse", future)

	mine, err := s.ListSessions(ctx, "anchat", "0xowner")
	if err != nil || len(mine) != 1 {
		t.Fatalf("list: %v %+v", err, mine)
	}
	theirs, err := s.ListSessions(ctx, "anchat", "0xsomebodyelse")
	if err != nil || len(theirs) != 1 {
		t.Fatalf("list: %v %+v", err, theirs)
	}

	// Somebody else's id, presented by me.
	if err := s.EndSession(ctx, "anchat", "0xowner", theirs[0].ID); err == nil {
		t.Fatal("one wallet ended another's session by naming its id")
	}
	if left, _ := s.ListSessions(ctx, "anchat", "0xsomebodyelse"); len(left) != 1 {
		t.Errorf("the other wallet's session went anyway: %+v", left)
	}

	if err := s.EndSession(ctx, "anchat", "0xowner", mine[0].ID); err != nil {
		t.Fatalf("end my own session: %v", err)
	}
	if left, _ := s.ListSessions(ctx, "anchat", "0xowner"); len(left) != 0 {
		t.Errorf("the session survived being ended: %+v", left)
	}
}

// Ending a session twice is not a second success: the second call has to say
// there was nothing to end, or a script cannot tell "done" from "never there".
func TestEndSession_saysSoWhenThereWasNothingToEnd(t *testing.T) {
	s, _, _ := realRegistry(t)
	ctx := context.Background()
	issueSession(t, s, "0xowner", time.Now().Add(time.Hour).UTC().Format(sqliteTime))

	mine, _ := s.ListSessions(ctx, "anchat", "0xowner")
	if err := s.EndSession(ctx, "anchat", "0xowner", mine[0].ID); err != nil {
		t.Fatalf("end: %v", err)
	}
	if err := s.EndSession(ctx, "anchat", "0xowner", mine[0].ID); err == nil {
		t.Error("ending an already-ended session reported success")
	}
}

// A session belongs to somebody. Listing with no subject would list the whole
// namespace's.
func TestListSessions_refusesAnEmptySubject(t *testing.T) {
	s, _, _ := realRegistry(t)
	if _, err := s.ListSessions(context.Background(), "anchat", "   "); err == nil {
		t.Error("an empty subject listed sessions")
	}
	if err := s.EndSession(context.Background(), "anchat", "", 1); err == nil {
		t.Error("an empty subject ended a session")
	}
}

// A session in one namespace is not a session in another, even for the same
// wallet: the namespace is what the grants hang off.
func TestListSessions_doesNotCrossNamespaces(t *testing.T) {
	s, db, _ := realRegistry(t)
	ctx := context.Background()
	if _, err := db.db.Exec(`INSERT INTO namespaces(id, name) VALUES (11, 'other')`); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	issueSession(t, s, "0xowner", time.Now().Add(time.Hour).UTC().Format(sqliteTime))

	other, err := s.ListSessions(ctx, "other", "0xowner")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("a session in one namespace was listed in another: %+v", other)
	}
}

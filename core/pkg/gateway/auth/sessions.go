package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// Which machines are signed in as you.
//
// A refresh token is a thirty-day credential, and until now the only thing that
// could be done with one was use it: there was no way to see that a laptop you
// no longer have is still able to mint access tokens, and no way to end that
// one session without ending all of them. `/v1/auth/logout` takes `all` or the
// token itself, and a machine you have lost is exactly the one whose token you
// do not have.

// Session is one live refresh token, described without handing it back.
type Session struct {
	// ID is what `orama auth sessions revoke` names. It is the row id: not a
	// secret, and useless without a credential in the same namespace.
	ID int64
	// Subject is the wallet the session belongs to.
	Subject string
	// Audience is what the session was issued for.
	Audience string
	// CreatedAt is when the session began.
	CreatedAt time.Time
	// ExpiresAt is when it stops working on its own.
	ExpiresAt time.Time
	// DeviceID is the device the session is bound to, or "" for a session
	// bound to the account alone.
	DeviceID string
}

// ListSessions returns the live sessions of one subject in one namespace.
//
// It never returns another subject's, and it never returns the token: a list
// endpoint that hands back the credential it is listing is a way to escalate a
// fifteen-minute access token into a thirty-day one.
func (s *Service) ListSessions(ctx context.Context, namespace, subject string) ([]Session, error) {
	db, err := s.deviceDB()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(subject) == "" {
		return nil, fmt.Errorf("a session belongs to somebody: no subject was resolved from this credential")
	}
	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("resolve the namespace %q: %w", namespace, err)
	}

	res, err := db.Query(client.WithInternalAuth(ctx),
		`SELECT id, subject, audience, created_at, expires_at, COALESCE(device_id, '')
		   FROM refresh_tokens
		  WHERE namespace_id = ? AND subject = ?
		    AND revoked_at IS NULL
		    AND (expires_at IS NULL OR expires_at > datetime('now'))
		  ORDER BY id DESC`,
		nsID, subject)
	if err != nil {
		return nil, fmt.Errorf("read the sessions: %w", err)
	}
	if res == nil {
		return nil, nil
	}

	out := make([]Session, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) < 6 {
			continue
		}
		created, _, _ := parseTimestamp(row[3])
		expires, _, _ := parseTimestamp(row[4])
		out = append(out, Session{
			ID:        cellInt64(row[0]),
			Subject:   getStringVal(row[1]),
			Audience:  getStringVal(row[2]),
			CreatedAt: created,
			ExpiresAt: expires,
			DeviceID:  getStringVal(row[5]),
		})
	}
	return out, nil
}

// EndSession ends one session by id.
//
// The subject is part of the WHERE clause rather than checked after the read:
// an id is a small integer, and a check that happens in Go after a query that
// did not filter is one refactor away from not happening.
//
// It ends the ability to mint new access tokens, and — for a session issued
// with an id, which is every session issued since sessions carried one — the
// access tokens it already minted and the sockets they hold open, through the
// revocation list. A session issued before that has no id to name; its access
// tokens go on working for at most one access-token lifetime, and EndSession
// reports that with ErrSessionTokensOutlive rather than implying the machine
// was cut off in the same instant.
func (s *Service) EndSession(ctx context.Context, namespace, subject string, id int64) error {
	if s.db == nil {
		return ErrRotationNotConfigured
	}
	if strings.TrimSpace(subject) == "" {
		return fmt.Errorf("a session belongs to somebody: no subject was resolved from this credential")
	}
	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return fmt.Errorf("resolve the namespace %q: %w", namespace, err)
	}

	sessionID, err := s.sessionIDOf(ctx, nsID, subject, id)
	if err != nil {
		return err
	}
	if sessionID == "" {
		if err := s.endSessionRow(ctx, nsID, subject, id); err != nil {
			return err
		}
		return ErrSessionTokensOutlive
	}
	// Every row of the session, not just the live one: a token rotated in the
	// last minute still has its reuse grace, and spending it would mint a new
	// row carrying the same session id.
	if _, err := s.db.Exec(client.WithInternalAuth(ctx),
		`UPDATE refresh_tokens
		    SET revoked_at = COALESCE(revoked_at, datetime('now')), grace_used_at = datetime('now')
		  WHERE namespace_id = ? AND subject = ? AND session_id = ?`,
		nsID, subject, sessionID); err != nil {
		return fmt.Errorf("end the session: %w", err)
	}
	if err := s.revocations.RevokeSessionID(ctx, sessionID); err != nil {
		return fmt.Errorf("refuse the access tokens of session %d: %w", id, err)
	}
	return nil
}

// ErrSessionTokensOutlive reports a session that was ended but issued before
// sessions carried an id: its refresh token is revoked, and the access tokens
// it already minted work until they expire.
var ErrSessionTokensOutlive = fmt.Errorf("the session is ended, but it predates session ids: "+
	"an access token it already minted keeps working until it expires, at most %s", AccessTokenLifetime)

// endSessionRow ends a session that predates session ids, by its row.
func (s *Service) endSessionRow(ctx context.Context, nsID interface{}, subject string, id int64) error {
	res, err := s.db.Exec(client.WithInternalAuth(ctx),
		`UPDATE refresh_tokens SET revoked_at = datetime('now'), grace_used_at = datetime('now')
		  WHERE id = ? AND namespace_id = ? AND subject = ? AND revoked_at IS NULL`,
		id, nsID, subject)
	if err != nil {
		return fmt.Errorf("end the session: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return fmt.Errorf("session %d is not one of yours, or has already ended", id)
	}
	return nil
}

// sessionIDOf reads the session id of one of a subject's live refresh tokens.
func (s *Service) sessionIDOf(ctx context.Context, nsID interface{}, subject string, id int64) (string, error) {
	db, err := s.deviceDB()
	if err != nil {
		return "", err
	}
	res, err := db.Query(client.WithInternalAuth(ctx),
		`SELECT COALESCE(session_id, '') FROM refresh_tokens
		  WHERE id = ? AND namespace_id = ? AND subject = ? AND revoked_at IS NULL LIMIT 1`,
		id, nsID, subject)
	if err != nil {
		return "", fmt.Errorf("read the session: %w", err)
	}
	if res == nil || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return "", fmt.Errorf("session %d is not one of yours, or has already ended", id)
	}
	return getStringVal(res.Rows[0][0]), nil
}

// cellInt64 reads a column that holds a row id.
//
// rqlite returns a float64 for every number because it decodes JSON, and
// go-sqlite3 returns an int64. A row id read as a float is fine until it is
// not, so both are read here rather than at each call site.
func cellInt64(v any) int64 {
	switch value := v.(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case nil:
		return 0
	default:
		var out int64
		if _, err := fmt.Sscanf(getStringVal(v), "%d", &out); err != nil {
			return 0
		}
		return out
	}
}

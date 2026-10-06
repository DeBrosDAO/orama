package webrtc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// mutedRetention is how long a muted admission is kept after it expires. A mute
// outlives its admission so that admitting the user again does not unmute them,
// but not for ever: the table would grow by one row per muted user and room
// without bound. A user re-admitted after this long is unmuted.
const mutedRetention = 7 * 24 * time.Hour

// AdmissionStore is where a namespace's WebRTC policy and the admissions its
// functions issued live: the namespace's own database (migration 073), read on
// the join path of its gateways. No SFU holds any of it, so it survives an SFU
// restart and is the same whichever node owns the room.
type AdmissionStore struct {
	db  rqlite.Client
	now func() time.Time

	// schemaOK is set once the tables are known to be the ones migration 073
	// makes (admission_schema.go).
	schemaMu sync.Mutex
	schemaOK bool
}

// NewAdmissionStore returns a store over the namespace's own database.
func NewAdmissionStore(db rqlite.Client) *AdmissionStore {
	return &AdmissionStore{db: db, now: time.Now}
}

// Admission is what the namespace has on record for one user in one room.
type Admission struct {
	// Valid is set when an admission applies to the caller's device and has
	// neither expired nor been revoked.
	Valid bool
	// Revoked and Expired explain a refusal; Found is false when the namespace
	// never admitted this user to this room.
	Found, Revoked, Expired bool
	// Muted is set when the namespace has muted this user in this room.
	Muted bool
	// ValidUntil is the unix second the latest valid admission ends (0 when
	// none is valid).
	ValidUntil int64
}

type admissionRow struct {
	DeviceID  string `db:"device_id"`
	ExpiresAt int64  `db:"expires_at"`
	RevokedAt int64  `db:"revoked_at"`
	Muted     int    `db:"muted"`
}

// RequireAdmission reports whether the namespace admits only users its
// functions admitted. A namespace that never set the policy does not.
func (s *AdmissionStore) RequireAdmission(ctx context.Context, namespace string) (bool, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return false, err
	}
	var rows []struct {
		Require int `db:"require_admission"`
	}
	if err := s.db.Query(ctx, &rows, `SELECT require_admission FROM webrtc_settings WHERE namespace = ?`, namespace); err != nil {
		return false, fmt.Errorf("failed to read the WebRTC admission policy of namespace %q: %w", namespace, err)
	}
	return len(rows) > 0 && rows[0].Require != 0, nil
}

// SetRequireAdmission sets the namespace's admission policy.
func (s *AdmissionStore) SetRequireAdmission(ctx context.Context, namespace string, require bool) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	flag := 0
	if require {
		flag = 1
	}
	_, err := s.db.Exec(ctx, `INSERT INTO webrtc_settings (namespace, require_admission, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(namespace) DO UPDATE SET require_admission = excluded.require_admission, updated_at = excluded.updated_at`,
		namespace, flag, s.now().Unix())
	if err != nil {
		return fmt.Errorf("failed to save the WebRTC admission policy of namespace %q: %w", namespace, err)
	}
	return nil
}

// Lookup reads what the namespace has on record for user, on device (""
// when the session is bound to none), in room. An admission for no particular
// device ("") applies to every device; one for a device applies to it alone.
func (s *AdmissionStore) Lookup(ctx context.Context, namespace, room, user, device string) (Admission, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return Admission{}, err
	}
	var rows []admissionRow
	err := s.db.Query(ctx, &rows, `SELECT device_id, expires_at, COALESCE(revoked_at, 0) AS revoked_at, muted
		FROM webrtc_admissions
		WHERE namespace = ? AND room = ? AND user_id = ? AND device_id IN (?, '')`,
		namespace, room, user, device)
	if err != nil {
		return Admission{}, fmt.Errorf("failed to read the WebRTC admissions of %q in room %q: %w", user, room, err)
	}
	return judge(rows, s.now().Unix()), nil
}

// judge picks the verdict over the rows that apply: any valid one admits.
func judge(rows []admissionRow, now int64) Admission {
	var a Admission
	for _, r := range rows {
		a.Found = true
		if r.Muted != 0 {
			a.Muted = true
		}
		switch {
		case r.RevokedAt != 0:
			a.Revoked = true
		case r.ExpiresAt <= now:
			a.Expired = true
		default:
			a.Valid = true
			if r.ExpiresAt > a.ValidUntil {
				a.ValidUntil = r.ExpiresAt
			}
		}
	}
	return a
}

// Admit records that user may join room for ttl, from device (or from any
// device when ""). Admitting again extends the admission and lifts a revocation;
// a mute on record stays. Admissions that expired are removed here, so the table
// holds what is live, except a muted one, which is kept for mutedRetention after
// it expires: removing it sooner would unmute the user on their next admission.
func (s *AdmissionStore) Admit(ctx context.Context, namespace, room, user, device string, ttl time.Duration) (time.Time, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return time.Time{}, err
	}
	now := s.now()
	expires := now.Add(ttl)
	if _, err := s.db.Exec(ctx, `DELETE FROM webrtc_admissions WHERE namespace = ? AND expires_at <= ? AND (muted = 0 OR expires_at <= ?)`,
		namespace, now.Unix(), now.Add(-mutedRetention).Unix()); err != nil {
		return time.Time{}, fmt.Errorf("failed to remove expired WebRTC admissions of namespace %q: %w", namespace, err)
	}
	_, err := s.db.Exec(ctx, `INSERT INTO webrtc_admissions (namespace, room, user_id, device_id, expires_at, revoked_at, muted, created_at)
		VALUES (?, ?, ?, ?, ?, NULL, 0, ?)
		ON CONFLICT(namespace, room, user_id, device_id) DO UPDATE SET expires_at = excluded.expires_at, revoked_at = NULL`,
		namespace, room, user, device, expires.Unix(), now.Unix())
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to record the WebRTC admission of %q to room %q: %w", user, room, err)
	}
	return time.Unix(expires.Unix(), 0), nil
}

// Revoke ends every admission of user to room, whatever the device. A user
// with none is not an error: the namespace may not require admission.
func (s *AdmissionStore) Revoke(ctx context.Context, namespace, room, user string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `UPDATE webrtc_admissions SET revoked_at = ? WHERE namespace = ? AND room = ? AND user_id = ?`,
		s.now().Unix(), namespace, room, user)
	if err != nil {
		return fmt.Errorf("failed to revoke the WebRTC admissions of %q in room %q: %w", user, room, err)
	}
	return nil
}

// SetMuted records the mute on every admission of user to room, so it holds
// when they rejoin. With no admission on record there is nothing to keep it on:
// the mute then lasts as long as the peer's connection.
func (s *AdmissionStore) SetMuted(ctx context.Context, namespace, room, user string, muted bool) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	flag := 0
	if muted {
		flag = 1
	}
	_, err := s.db.Exec(ctx, `UPDATE webrtc_admissions SET muted = ? WHERE namespace = ? AND room = ? AND user_id = ?`,
		flag, namespace, room, user)
	if err != nil {
		return fmt.Errorf("failed to record the mute of %q in room %q: %w", user, room, err)
	}
	return nil
}

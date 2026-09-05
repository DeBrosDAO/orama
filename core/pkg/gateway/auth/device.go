package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// Signing in from a machine that has no wallet on it.
//
// `orama auth login` shelled out to the RootWallet CLI on the same machine.
// That works on a laptop and nowhere else: on a server reached over SSH, in a
// container, or in CI there is no `rw` and no browser, and the documented way
// in was to paste a permanent API key into the environment.
//
// The device authorization grant (RFC 8628) separates the machine that wants
// the session from the machine that can approve it. The waiting machine asks
// for a code and polls; the human approves that code from somewhere their
// wallet already is. Nothing secret crosses between the two: the approver
// sends a signature over this gateway's own challenge — the same signature
// /v1/auth/verify takes — and the waiting machine collects its tokens by
// presenting the device code it was given here.

const (
	// DeviceCodeLifetime is how long a pending login stands. Long enough to
	// walk to another machine, short enough that a code read over someone's
	// shoulder is worthless by the time it is used.
	DeviceCodeLifetime = 10 * time.Minute

	// DevicePollInterval is the shortest gap between two polls for the same
	// device code. The RFC makes it advisory; enforcing it is what stops a
	// client in a tight loop from becoming the login endpoint's load.
	DevicePollInterval = 5 * time.Second

	// deviceUserCodeLength is how many characters the human types, before the
	// separator. Eight over a 28-character alphabet is ~3.7e11 codes, against
	// a table that holds only what is pending and unexpired.
	deviceUserCodeLength = 8

	// deviceUserCodeAlphabet has no character that can be read as another one:
	// no O/0, no I/1/L, no U (which is heard as V), no S/5.
	deviceUserCodeAlphabet = "BCDFGHJKMNPQRTVWXYZ23467"
)

// Device-flow outcomes. RFC 8628 §3.5 gives these names to the polling client,
// and they are the whole vocabulary: everything else is an error.
var (
	// ErrDeviceAuthorizationPending means nobody has approved it yet.
	ErrDeviceAuthorizationPending = errors.New("authorization_pending")
	// ErrDeviceSlowDown means the client polled faster than the interval.
	ErrDeviceSlowDown = errors.New("slow_down")
	// ErrDeviceCodeExpired means nobody approved it in time.
	ErrDeviceCodeExpired = errors.New("expired_token")
	// ErrDeviceAccessDenied means the approver refused.
	ErrDeviceAccessDenied = errors.New("access_denied")
	// ErrDeviceCodeUnknown means the code names no pending login. A code that
	// was already collected reads as unknown, because it is single-use.
	ErrDeviceCodeUnknown = errors.New("invalid_grant")
	// ErrDeviceAlreadyApproved means somebody already approved this code. It
	// is told apart from "unknown" because it is the answer to a different
	// question: the approver did nothing wrong and needs no new code.
	ErrDeviceAlreadyApproved = errors.New("already approved")
)

// DeviceAuthorization is a login waiting to be approved.
type DeviceAuthorization struct {
	// UserCode is what the human types, formatted for reading aloud.
	UserCode string
	// DeviceCode is the secret the waiting machine polls with. It is returned
	// exactly once, from Start, and never stored in the clear.
	DeviceCode string
	// Namespace the waiting machine asked for, or "" for whichever namespace
	// the approver signs in to.
	Namespace string
	// ExpiresAt is when polling stops being worth it.
	ExpiresAt time.Time
	// Interval is the shortest gap between polls, in seconds.
	Interval int
}

// StartDeviceAuthorization records a pending login and returns the pair of
// codes: the secret one for the machine that waits, the short one for the
// human who approves.
func (s *Service) StartDeviceAuthorization(ctx context.Context, namespace string) (*DeviceAuthorization, error) {
	db, err := s.deviceDB()
	if err != nil {
		return nil, err
	}
	// Every new login sweeps the finished ones. A dedicated ticker would be a
	// third background goroutine for a table that only grows when somebody
	// logs in, which is exactly when this runs.
	if err := s.pruneDeviceAuthorizations(ctx, db); err != nil {
		return nil, err
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate the device code: %w", err)
	}
	deviceCode := base64.RawURLEncoding.EncodeToString(raw)

	userCode, err := newUserCode()
	if err != nil {
		return nil, err
	}

	var ns any
	if trimmed := strings.TrimSpace(namespace); trimmed != "" {
		ns = strings.ToLower(trimmed)
	}

	expires := time.Now().Add(DeviceCodeLifetime).UTC()
	if _, err := db.Query(client.WithInternalAuth(ctx),
		`INSERT INTO device_authorizations(device_code, user_code, namespace, expires_at)
		 VALUES (?, ?, ?, ?)`,
		sha256Hex(deviceCode), userCode, ns, expires.Format(sqliteTime)); err != nil {
		return nil, fmt.Errorf("record the pending login: %w", err)
	}

	return &DeviceAuthorization{
		UserCode:   userCode,
		DeviceCode: deviceCode,
		Namespace:  strings.TrimSpace(namespace),
		ExpiresAt:  expires,
		Interval:   int(DevicePollInterval / time.Second),
	}, nil
}

// PendingDeviceAuthorization is what the approver is shown before they decide.
type PendingDeviceAuthorization struct {
	UserCode  string
	Namespace string
	CreatedAt string
	ExpiresAt time.Time
}

// LookupDeviceAuthorization returns the pending login a user code names, so
// the approver can be told what they are about to approve rather than after.
func (s *Service) LookupDeviceAuthorization(ctx context.Context, userCode string) (*PendingDeviceAuthorization, error) {
	db, err := s.deviceDB()
	if err != nil {
		return nil, err
	}
	code, err := NormalizeUserCode(userCode)
	if err != nil {
		return nil, err
	}

	res, err := db.Query(client.WithInternalAuth(ctx),
		`SELECT namespace, created_at, expires_at, approved_at, denied_at, claimed_at
		   FROM device_authorizations WHERE user_code = ? LIMIT 1`, code)
	if err != nil {
		return nil, fmt.Errorf("read the pending login: %w", err)
	}
	if res == nil || len(res.Rows) == 0 || len(res.Rows[0]) < 6 {
		return nil, ErrDeviceCodeUnknown
	}
	row := res.Rows[0]

	switch {
	case getStringVal(row[5]) != "":
		return nil, ErrDeviceCodeUnknown
	case getStringVal(row[4]) != "":
		return nil, ErrDeviceAccessDenied
	case getStringVal(row[3]) != "":
		return nil, ErrDeviceAlreadyApproved
	case deviceExpired(row[2]):
		return nil, ErrDeviceCodeExpired
	}
	expires, _, _ := parseTimestamp(row[2])

	return &PendingDeviceAuthorization{
		UserCode:  code,
		Namespace: getStringVal(row[0]),
		CreatedAt: getStringVal(row[1]),
		ExpiresAt: expires,
	}, nil
}

// ApproveDeviceAuthorization binds a verified wallet to a pending login.
//
// The caller has already proved the signature; what is checked here is that the
// login is still open and that the approver is signing in to the namespace the
// waiting machine asked for. Approving is a CAS on approved_at, so two
// approvals of one code cannot both win.
func (s *Service) ApproveDeviceAuthorization(ctx context.Context, userCode, wallet, namespace string) error {
	code, err := NormalizeUserCode(userCode)
	if err != nil {
		return err
	}
	pending, err := s.LookupDeviceAuthorization(ctx, code)
	if err != nil {
		return err
	}
	// A machine that asked for one namespace must not be handed a session in
	// another: it will use the session for whatever it was going to do there.
	if pending.Namespace != "" && !strings.EqualFold(pending.Namespace, namespace) {
		return fmt.Errorf("this login asked for namespace %q and you signed in to %q",
			pending.Namespace, namespace)
	}
	won, err := s.recordApproval(ctx, code, wallet, namespace)
	if err != nil {
		return err
	}
	if !won {
		return ErrDeviceCodeUnknown
	}
	return nil
}

// recordApproval is the write, and it is a compare-and-swap rather than an
// update.
//
// The read above it cannot stand in for these predicates: between the read and
// the write another approval, or a refusal, can land. It is separate from the
// caller so that "this happens once" is a thing a test can hold two calls
// against, rather than a WHERE clause nothing sequential can reach.
func (s *Service) recordApproval(ctx context.Context, userCode, wallet, namespace string) (bool, error) {
	if s.db == nil {
		return false, ErrRotationNotConfigured
	}
	res, err := s.db.Exec(client.WithInternalAuth(ctx),
		`UPDATE device_authorizations
		    SET approved_at = datetime('now'), subject = ?, namespace = ?
		  WHERE user_code = ? AND approved_at IS NULL AND denied_at IS NULL AND claimed_at IS NULL`,
		wallet, strings.ToLower(namespace), userCode)
	if err != nil {
		return false, fmt.Errorf("approve the pending login: %w", err)
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// DenyDeviceAuthorization refuses a pending login, so the machine waiting on it
// stops rather than polling out its ten minutes.
func (s *Service) DenyDeviceAuthorization(ctx context.Context, userCode string) error {
	code, err := NormalizeUserCode(userCode)
	if err != nil {
		return err
	}
	if s.db == nil {
		return ErrRotationNotConfigured
	}
	res, err := s.db.Exec(client.WithInternalAuth(ctx),
		`UPDATE device_authorizations SET denied_at = datetime('now')
		  WHERE user_code = ? AND approved_at IS NULL AND denied_at IS NULL AND claimed_at IS NULL`,
		code)
	if err != nil {
		return fmt.Errorf("refuse the pending login: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrDeviceCodeUnknown
	}
	return nil
}

// claimApproval is the write that collects an approval, once.
//
// Same reason as recordApproval: the read that precedes it cannot see a write
// that has not happened yet, so two machines polling the same device code would
// both pass the read. This is what makes exactly one of them win.
func (s *Service) claimApproval(ctx context.Context, hashedDeviceCode string) (bool, error) {
	if s.db == nil {
		return false, ErrRotationNotConfigured
	}
	res, err := s.db.Exec(client.WithInternalAuth(ctx),
		`UPDATE device_authorizations SET claimed_at = datetime('now')
		  WHERE device_code = ? AND approved_at IS NOT NULL AND claimed_at IS NULL`, hashedDeviceCode)
	if err != nil {
		return false, fmt.Errorf("collect the approved login: %w", err)
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// ClaimedDeviceAuthorization is an approved login, collected.
type ClaimedDeviceAuthorization struct {
	Subject   string
	Namespace string
}

// ClaimDeviceAuthorization is the waiting machine's poll.
//
// It returns one of the RFC's outcomes and, exactly once, the approval. The
// claim is a CAS on claimed_at: a device code collects a session once, so a
// code read from a log or a shell history collects nothing.
func (s *Service) ClaimDeviceAuthorization(ctx context.Context, deviceCode string) (*ClaimedDeviceAuthorization, error) {
	db, err := s.deviceDB()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(deviceCode) == "" {
		return nil, ErrDeviceCodeUnknown
	}
	hashed := sha256Hex(strings.TrimSpace(deviceCode))

	res, err := db.Query(client.WithInternalAuth(ctx),
		`SELECT subject, namespace, approved_at, denied_at, claimed_at, expires_at, last_polled_at
		   FROM device_authorizations WHERE device_code = ? LIMIT 1`, hashed)
	if err != nil {
		return nil, fmt.Errorf("read the pending login: %w", err)
	}
	if res == nil || len(res.Rows) == 0 || len(res.Rows[0]) < 7 {
		return nil, ErrDeviceCodeUnknown
	}
	row := res.Rows[0]

	if getStringVal(row[4]) != "" {
		return nil, ErrDeviceCodeUnknown
	}
	if getStringVal(row[3]) != "" {
		return nil, ErrDeviceAccessDenied
	}
	if deviceExpired(row[5]) {
		return nil, ErrDeviceCodeExpired
	}
	// The interval only paces the waiting. It is checked after the terminal
	// states and after the approval, because a client that polled a moment ago
	// and has now been approved should be given its session rather than told to
	// wait another five seconds for news that has already arrived.
	if getStringVal(row[2]) == "" {
		if last, present, _ := parseTimestamp(row[6]); present && time.Since(last) < DevicePollInterval {
			return nil, ErrDeviceSlowDown
		}
		if _, err := db.Query(client.WithInternalAuth(ctx),
			`UPDATE device_authorizations SET last_polled_at = datetime('now') WHERE device_code = ?`,
			hashed); err != nil {
			return nil, fmt.Errorf("record the poll: %w", err)
		}
		return nil, ErrDeviceAuthorizationPending
	}

	won, err := s.claimApproval(ctx, hashed)
	if err != nil {
		return nil, err
	}
	if !won {
		return nil, ErrDeviceCodeUnknown
	}

	return &ClaimedDeviceAuthorization{
		Subject:   getStringVal(row[0]),
		Namespace: getStringVal(row[1]),
	}, nil
}

// pruneDeviceAuthorizations removes the rows nobody came back for.
//
// A pending login that expired is worthless, and a claimed one has already
// done its job; keeping either turns a login table into a record of who signed
// in from where and when.
func (s *Service) pruneDeviceAuthorizations(ctx context.Context, db client.DatabaseClient) error {
	if _, err := db.Query(client.WithInternalAuth(ctx),
		`DELETE FROM device_authorizations
		  WHERE expires_at < datetime('now') OR claimed_at IS NOT NULL`); err != nil {
		return fmt.Errorf("remove the finished logins: %w", err)
	}
	return nil
}

// deviceDB is the registry a pending login lives in.
//
// It is the same database the refresh tokens go into, deliberately: a device
// login ends in IssueTokens, and a row written somewhere that cannot mint the
// session it authorises is a login that can never complete.
func (s *Service) deviceDB() (client.DatabaseClient, error) {
	if s == nil || s.orm == nil {
		return nil, fmt.Errorf("this gateway has no registry, so it cannot hold a pending login")
	}
	db := s.orm.Database()
	if db == nil {
		return nil, fmt.Errorf("this gateway has no registry, so it cannot hold a pending login")
	}
	return db, nil
}

// newUserCode draws the code the human reads.
//
// crypto/rand, not math/rand: a predictable user code is one an attacker can
// have approved by asking the user to approve "their own" login.
func newUserCode() (string, error) {
	limit := big.NewInt(int64(len(deviceUserCodeAlphabet)))
	out := make([]byte, 0, deviceUserCodeLength+1)
	for i := 0; i < deviceUserCodeLength; i++ {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("generate the user code: %w", err)
		}
		if i == deviceUserCodeLength/2 {
			out = append(out, '-')
		}
		out = append(out, deviceUserCodeAlphabet[n.Int64()])
	}
	return string(out), nil
}

// NormalizeUserCode is how a typed code becomes the stored one.
//
// Case and the separator are presentation: somebody reading a code aloud will
// say it without the dash, and somebody typing it will use whichever case
// their terminal was in.
func NormalizeUserCode(raw string) (string, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - ('a' - 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, raw)
	if len(cleaned) != deviceUserCodeLength {
		return "", fmt.Errorf("a device code is %d characters (you gave %d)",
			deviceUserCodeLength, len(cleaned))
	}
	return cleaned[:deviceUserCodeLength/2] + "-" + cleaned[deviceUserCodeLength/2:], nil
}

// deviceExpired reads an expires_at column, and treats one it cannot read as
// past.
//
// The column is NOT NULL, so an unreadable value is a value somebody wrote
// wrong. A login that stays open because its deadline could not be parsed is
// the one failure mode worth ruling out by construction.
func deviceExpired(cell any) bool {
	at, present, readable := parseTimestamp(cell)
	if !present || !readable {
		return true
	}
	return time.Now().After(at)
}

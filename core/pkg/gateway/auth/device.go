package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Device attribution (bugboard feat-384).
//
// A function could previously learn only which ACCOUNT was calling: every
// device of an account produced an identical JWT subject. Retrieval endpoints
// are authenticated per account, so anyone holding the account seed could call
// them without being a current device of that account.
//
// The split of responsibility here is deliberate and is the whole design:
//
//   - The GATEWAY asserts POSSESSION — "the holder of device key X was present
//     at this login" — a cryptographic fact it can verify by itself.
//   - The NAMESPACE asserts AUTHORIZATION — "X is a current device of this
//     account" — policy only the app can know.
//
// The gateway therefore stores no roster and has no opinion about which devices
// an account may have. Teaching it otherwise would put a per-request WASM call
// into the auth path (fail-open, 2s timeout, 3 attempts — every property you do
// not want on an authorization check) and couple the platform to one tenant's
// data model.

const (
	// DeviceClaimFingerprint is the JWT claim carrying the verified device
	// fingerprint. Functions read it with the `get_caller_claim` host call.
	DeviceClaimFingerprint = "device_fp"

	// DeviceClaimSince is the JWT claim carrying the gateway's first-seen time
	// for this (account, device) pair, as Unix seconds.
	//
	// This is the claim that makes "a new device never receives the archive"
	// actually hold. An app's signed device roster cannot establish it against
	// the threat the rule exists for: if the roster is signed by a key derived
	// from the account seed, an attacker holding that seed backdates their
	// device and claims the whole history. The gateway's observation is not
	// something that attacker can move.
	DeviceClaimSince = "device_since"

	// deviceAssertionPrefix is prepended to the login challenge before the
	// device signs it, so a device signature can never be replayed as an
	// account signature (or vice versa) over the same bytes. Domain separation.
	deviceAssertionPrefix = "orama-device-assertion:v1:"

	// deviceFingerprintBytes is how much of the SHA-256 of the public key forms
	// the fingerprint. 16 bytes / 128 bits leaves no practical margin for a
	// targeted collision, which matters because the fingerprint is the identity
	// a function authorizes against.
	deviceFingerprintBytes = 16
)

// ethAddressPattern matches a 20-byte hex ETH address in any casing.
var ethAddressPattern = regexp.MustCompile(`^0[xX][0-9a-fA-F]{40}$`)

// ErrDeviceBindTransient marks a device-binding failure caused by
// infrastructure rather than by the caller's credentials.
//
// The distinction is the bugboard #125 lesson applied here: during a leader
// re-election in a rolling upgrade, an rqlite error is not "your signature is
// wrong". Collapsing it into 401 makes every device login look like rejected
// credentials, and clients respond to 401 by tearing down the session and
// re-running SIWE — a reconnect storm precisely when the cluster is already
// struggling. Callers must surface this as a retryable 503.
var ErrDeviceBindTransient = fmt.Errorf("device binding temporarily unavailable")

// DeviceBinding is a verified device identity bound to one account.
type DeviceBinding struct {
	// Fingerprint is the value stamped into the JWT and compared by functions.
	Fingerprint string
	// PublicKey is the raw ed25519 device key, base64 standard encoding.
	PublicKey string
	// FirstSeen is when THIS gateway first observed this key for this account.
	FirstSeen time.Time
}

// DeviceFingerprint derives the stable fingerprint of a device public key.
//
// Derived, never client-supplied: a client that could choose its own
// fingerprint could claim another device's identity while still proving
// possession of its own key.
func DeviceFingerprint(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:deviceFingerprintBytes])
}

// VerifyDeviceAssertion checks that the holder of publicKeyB64 signed the login
// challenge, and returns the device fingerprint.
//
// challenge is the same value the account signed. Binding both signatures to
// one challenge is what ties "this account authenticated" and "this device was
// present" into a single event — two independently-replayable signatures over
// different material would not.
func VerifyDeviceAssertion(challenge, publicKeyB64, signatureB64 string) (string, error) {
	pub, err := decodeDeviceKey(publicKeyB64)
	if err != nil {
		return "", err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signatureB64))
	if err != nil {
		return "", fmt.Errorf("device_signature is not valid base64")
	}
	if len(sig) != ed25519.SignatureSize {
		return "", fmt.Errorf("device_signature must be %d bytes, got %d", ed25519.SignatureSize, len(sig))
	}
	if strings.TrimSpace(challenge) == "" {
		return "", fmt.Errorf("cannot verify a device assertion against an empty challenge")
	}

	if !ed25519.Verify(pub, deviceAssertionMessage(challenge), sig) {
		return "", fmt.Errorf("device signature does not verify against the login challenge")
	}
	return DeviceFingerprint(pub), nil
}

// deviceAssertionMessage is the exact byte string a device signs.
func deviceAssertionMessage(challenge string) []byte {
	return []byte(deviceAssertionPrefix + challenge)
}

// DeviceAssertionMessage exposes the signing input so clients and tests build
// the identical bytes. Publishing this is what keeps the client and the server
// from drifting into two subtly different messages.
func DeviceAssertionMessage(challenge string) []byte {
	return deviceAssertionMessage(challenge)
}

// decodeDeviceKey parses and validates a base64 ed25519 public key.
func decodeDeviceKey(publicKeyB64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyB64))
	if err != nil {
		return nil, fmt.Errorf("device_public_key is not valid base64")
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("device_public_key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// BindDevice records a verified device for an account and returns the binding.
//
// The first successful assertion sets first_seen_at; every later one only moves
// last_seen_at. That asymmetry is the point — see DeviceClaimSince.
//
// A revoked binding is NOT silently resurrected: it returns an error, so a
// device the namespace removed cannot log back in and re-acquire its claim.
// Un-revoking is the namespace's decision, made through its own flow, not a
// side effect of the removed device trying again.
func (s *Service) BindDevice(ctx context.Context, namespace, subject, publicKeyB64, fingerprint string) (*DeviceBinding, error) {
	subject = normalizeDeviceSubject(subject)
	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve namespace %s: %v", ErrDeviceBindTransient, namespace, err)
	}
	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()
	now := time.Now().UTC()

	// READ FIRST, then insert only if absent.
	//
	// The obvious shape — INSERT OR IGNORE then SELECT back — is wrong on rqlite
	// and failed on devnet the first time it ran: a write goes through raft, and
	// an immediately-following read is not guaranteed to observe it. The row was
	// committed correctly and the read microseconds later returned nothing, which
	// the code then reported as "binding vanished". Unit tests could not catch it
	// because the fake database is instantly consistent.
	//
	// This ordering needs no read-after-write at all: a returning device is found
	// by the read and uses its STORED first_seen_at; a new device is inserted and
	// is, by definition, first seen now.
	existing, err := db.Query(internalCtx,
		`SELECT public_key, first_seen_at, revoked_at FROM orama_device_bindings
		  WHERE namespace_id = ? AND subject = ? AND device_fp = ?`,
		nsID, subject, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("%w: read device binding: %v", ErrDeviceBindTransient, err)
	}

	if existing != nil && existing.Count > 0 && len(existing.Rows) > 0 {
		row := existing.Rows[0]
		if len(row) < 3 {
			return nil, fmt.Errorf("device binding row has %d columns, want 3", len(row))
		}
		if row[2] != nil && fmt.Sprint(row[2]) != "" {
			return nil, fmt.Errorf("device %s is revoked for this account", fingerprint)
		}
		// Fail CLOSED on an unreadable stored timestamp: "now" is the newest
		// possible first-seen, so the device gets the least archive access
		// rather than the most.
		firstSeen, ok := parseDeviceTime(row[1])
		if !ok {
			firstSeen = now
		}

		if _, uerr := db.Query(internalCtx,
			`UPDATE orama_device_bindings SET last_seen_at = ?
			  WHERE namespace_id = ? AND subject = ? AND device_fp = ?`,
			now, nsID, subject, fingerprint); uerr != nil {
			// Non-fatal: last_seen is observability, not authorization.
			s.logger.ComponentWarn(logging.ComponentGeneral, "failed to update device last_seen_at",
				zap.String("namespace", namespace), zap.String("device_fp", fingerprint), zap.Error(uerr))
		}

		return &DeviceBinding{
			Fingerprint: fingerprint,
			PublicKey:   fmt.Sprint(row[0]),
			FirstSeen:   firstSeen,
		}, nil
	}

	// First time this account has presented this key. OR IGNORE keeps a
	// concurrent first login from erroring on the UNIQUE constraint; if we lose
	// that race the stored first_seen_at is the other request's, milliseconds
	// from ours, which is immaterial to a "did this device exist before that
	// message" comparison.
	if _, err := db.Query(internalCtx,
		`INSERT OR IGNORE INTO orama_device_bindings
		   (namespace_id, subject, device_fp, public_key, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		nsID, subject, fingerprint, publicKeyB64, now, now); err != nil {
		return nil, fmt.Errorf("%w: record device binding: %v", ErrDeviceBindTransient, err)
	}

	return &DeviceBinding{
		Fingerprint: fingerprint,
		PublicKey:   publicKeyB64,
		FirstSeen:   now,
	}, nil
}

// ErrDeviceNotFound means no binding matched and no token was revoked — the
// revocation did nothing at all.
//
// Surfaced rather than reported as success because the realistic ways to hit it
// are all silent misses an operator must see: a subject in the wrong casing or
// form, a fingerprint that never existed, or the call sent to a gateway whose
// RQLite does not hold that binding. A security control that answers 200 while
// changing nothing is worse than one that errors.
var ErrDeviceNotFound = fmt.Errorf("no device binding or bound refresh token matched")

// RevokeDevice marks a device binding revoked and revokes every refresh token
// obtained by that device.
//
// Both halves are required. Marking the binding alone stops future logins but
// leaves the existing refresh chain minting valid device-stamped tokens for its
// full 30-day life; revoking the refresh rows is what bounds a revoked device
// to one access-token TTL. Returns the number of refresh tokens revoked.
func (s *Service) RevokeDevice(ctx context.Context, namespace, subject, fingerprint string) (int64, error) {
	// s.db is wired conditionally (SetRqliteClient), and this path needs it for
	// RowsAffected. RefreshToken guards it the same way rather than panicking
	// inside an HTTP handler.
	if s.db == nil {
		return 0, fmt.Errorf("device revocation requires the rqlite client, which is not configured")
	}
	subject = normalizeDeviceSubject(subject)
	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return 0, fmt.Errorf("resolve namespace %s: %w", namespace, err)
	}
	internalCtx := client.WithInternalAuth(ctx)
	now := time.Now().UTC()

	// s.db (not s.orm) is the lower-level rqlite client: it is the one that
	// exposes RowsAffected, which this path needs to tell "revoked something"
	// from "matched nothing".
	res, err := s.db.Exec(internalCtx,
		`UPDATE orama_device_bindings SET revoked_at = ?
		  WHERE namespace_id = ? AND subject = ? AND device_fp = ? AND revoked_at IS NULL`,
		now, nsID, subject, fingerprint)
	if err != nil {
		return 0, fmt.Errorf("revoke device binding: %w", err)
	}
	bindingsRevoked, _ := res.RowsAffected()
	if bindingsRevoked == 0 {
		// Either unknown or already revoked. Still fall through to the token
		// revocation below: an earlier partial failure could have left the
		// binding revoked with live tokens, and this call must converge on
		// "this device holds nothing".
		s.logger.ComponentWarn(logging.ComponentGeneral,
			"device revoke matched no live binding; revoking any bound refresh tokens anyway",
			zap.String("namespace", namespace), zap.String("device_fp", fingerprint))
	}

	// grace_used_at is burned alongside revoked_at, exactly as RevokeToken does
	// for logout. Without it the reuse grace (bugboard #125) would still accept
	// the revoked device's token once more within the grace window, handing it a
	// fresh 30-day chain — this call must converge on "this device holds
	// nothing", not "nothing new".
	tokRes, err := s.db.Exec(internalCtx,
		`UPDATE refresh_tokens SET revoked_at = ?, grace_used_at = ?
		  WHERE namespace_id = ? AND subject = ? AND device_fp = ? AND revoked_at IS NULL`,
		now, now, nsID, subject, fingerprint)
	if err != nil {
		return 0, fmt.Errorf("revoke refresh tokens for device %s: %w", fingerprint, err)
	}
	revoked, _ := tokRes.RowsAffected()
	if bindingsRevoked == 0 && revoked == 0 {
		// Nothing matched anywhere. Do not let the caller believe a device was
		// stopped when none was found.
		return 0, ErrDeviceNotFound
	}
	return revoked, nil
}

// deviceClaims renders a binding as the gateway-owned JWT claims.
//
// Kept separate from the namespace claims provider's output on purpose: these
// are stamped by the gateway after the provider runs, and the keys are reserved
// so a provider can never inject or overwrite them.
func deviceClaims(b *DeviceBinding) map[string]string {
	if b == nil {
		return nil
	}
	return map[string]string{
		DeviceClaimFingerprint: b.Fingerprint,
		DeviceClaimSince:       fmt.Sprint(b.FirstSeen.UTC().Unix()),
	}
}

// parseDeviceTime coerces a stored timestamp into a time.Time, reporting
// whether it could be established at all.
//
// It returns ok=false rather than a fallback because the two call sites need
// OPPOSITE failure behaviour, and a shared silent default gave the refresh path
// the dangerous one: time.Time{}.Unix() is -62135596800, so an unparseable
// timestamp minted device_since = year 1 — older than every message, which
// under "a new device never receives the archive" grants the whole archive.
// That is maximally fail-open on the single rule this feature exists to
// enforce. Callers now decide explicitly.
func parseDeviceTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed, true
			}
		}
	}
	return time.Time{}, false
}

// normalizeDeviceSubject canonicalises the account identifier used for device
// bindings.
//
// ETH addresses are hex and the signature check is case-insensitive
// (verifyEthSignature compares recovered addresses without regard to case), so
// a client may present any casing and still authenticate. Without
// normalisation, a REVOKED device simply logs back in with different casing:
// the UNIQUE(namespace_id, subject, device_fp) constraint does not collide, a
// fresh un-revoked row is created, and revocation is defeated. It would also
// silently reset first_seen_at, handing a device archive access it had lost.
//
// Only ETH is folded. Solana addresses are base58 and case is significant
// there, so lowercasing them would merge genuinely distinct accounts.
func normalizeDeviceSubject(subject string) string {
	s := strings.TrimSpace(subject)
	if ethAddressPattern.MatchString(s) {
		return strings.ToLower(s)
	}
	return s
}

// withDeviceClaims returns custom with the gateway-owned device claims added.
//
// Always returns a fresh map when a device is present, so the caller's map (the
// one that gets stored on the refresh token) is never mutated to include them.
func withDeviceClaims(custom map[string]string, device *DeviceBinding) map[string]string {
	dc := deviceClaims(device)
	if len(dc) == 0 {
		return custom
	}
	out := make(map[string]string, len(custom)+len(dc))
	for k, v := range custom {
		out[k] = v
	}
	// Device claims win: their keys are gateway-reserved, so a namespace
	// provider cannot have set them, but overwriting rather than skipping means
	// a future provider bug cannot shadow a verified device either.
	for k, v := range dc {
		out[k] = v
	}
	return out
}

// stripDeviceClaims returns custom without the gateway-owned device claims.
//
// Used before persisting claims on a refresh token: those stored claims feed
// reuseLastKnownClaims, which is wallet-scoped and would otherwise replay one
// device's identity into another device's login.
func stripDeviceClaims(custom map[string]string) map[string]string {
	if len(custom) == 0 {
		return custom
	}
	out := make(map[string]string, len(custom))
	for k, v := range custom {
		if k == DeviceClaimFingerprint || k == DeviceClaimSince {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// deviceFingerprintOf renders a binding's fingerprint for the refresh_tokens
// column, mapping a nil binding to NULL.
func deviceFingerprintOf(device *DeviceBinding) any {
	if device == nil {
		return nil
	}
	return nullableFingerprint(device.Fingerprint)
}

// nullableFingerprint renders a fingerprint for the refresh_tokens column,
// mapping "" to NULL. See deviceFingerprintOf for why the distinction matters.
func nullableFingerprint(fp string) any {
	if fp == "" {
		return nil
	}
	return fp
}

// liveDeviceBinding returns the binding for a fingerprint, or nil when it is
// revoked or unknown. An error means the lookup itself failed, which callers
// must NOT read as "revoked".
//
// Takes an already-resolved nsID: the refresh path resolved it several steps
// earlier, and re-resolving would add a second rqlite round-trip to every
// device-bound refresh plus an extra transient failure mode that drops the
// claim for 15 minutes.
func (s *Service) liveDeviceBinding(ctx context.Context, nsID interface{}, subject, fingerprint string) (*DeviceBinding, error) {
	subject = normalizeDeviceSubject(subject)
	res, err := s.orm.Database().Query(client.WithInternalAuth(ctx),
		`SELECT public_key, first_seen_at FROM orama_device_bindings
		  WHERE namespace_id = ? AND subject = ? AND device_fp = ? AND revoked_at IS NULL`,
		nsID, subject, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("read device binding: %w", err)
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 {
		// Positive evidence: no live binding. The caller unbinds the chain.
		return nil, nil
	}
	if len(res.Rows[0]) < 2 {
		// A shape anomaly is NOT evidence of revocation. Returning nil here
		// would permanently unbind a chain whose device is perfectly valid, so
		// surface it as an error and let the caller drop only this token's claim.
		return nil, fmt.Errorf("device binding row has %d columns, want 2", len(res.Rows[0]))
	}
	firstSeen, ok := parseDeviceTime(res.Rows[0][1])
	if !ok {
		// Same reasoning: cannot establish first-seen, so cannot honestly stamp
		// device_since. An error drops the claim for this token; it must never
		// become year 1.
		return nil, fmt.Errorf("device binding for %s has an unreadable first_seen_at", fingerprint)
	}
	return &DeviceBinding{
		Fingerprint: fingerprint,
		PublicKey:   fmt.Sprint(res.Rows[0][0]),
		FirstSeen:   firstSeen,
	}, nil
}

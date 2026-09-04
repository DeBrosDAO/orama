package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// Device attribution (bugboard feat-384).
//
// The gateway's job here is narrow and absolute: assert that the holder of a
// specific device key was present at a specific login, in a way the caller
// cannot forge. Everything downstream — a function refusing history to a device
// that joined yesterday, an app revoking a stolen phone — rests on that one
// assertion being unfakeable. These tests exist to hold it.

func newDeviceKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate device key: %v", err)
	}
	return pub, priv, base64.StdEncoding.EncodeToString(pub)
}

func signAssertion(t *testing.T, priv ed25519.PrivateKey, challenge string) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, DeviceAssertionMessage(challenge)))
}

// The happy path: a device that signs the challenge is identified by a
// fingerprint derived from its key.
func TestVerifyDeviceAssertion_acceptsAGenuineAssertion(t *testing.T) {
	pub, priv, pubB64 := newDeviceKey(t)
	const challenge = "nonce-abc123"

	fp, err := VerifyDeviceAssertion(challenge, pubB64, signAssertion(t, priv, challenge))
	if err != nil {
		t.Fatalf("genuine assertion rejected: %v", err)
	}
	if fp != DeviceFingerprint(pub) {
		t.Errorf("fingerprint = %q, want the derivation of the presented key", fp)
	}
}

// The core forgery case: a signature from a DIFFERENT key must not authenticate
// as this device. Without this the claim is decorative.
func TestVerifyDeviceAssertion_rejectsAnotherDevicesSignature(t *testing.T) {
	_, _, victimPubB64 := newDeviceKey(t)
	_, attackerPriv, _ := newDeviceKey(t)
	const challenge = "nonce-abc123"

	if _, err := VerifyDeviceAssertion(challenge, victimPubB64, signAssertion(t, attackerPriv, challenge)); err == nil {
		t.Fatal("a signature made by another key verified as this device — the device claim would be forgeable")
	}
}

// A signature over a DIFFERENT challenge must not be replayable into this
// login. This is what binds the device assertion to one specific login event
// rather than making it a reusable bearer artifact.
func TestVerifyDeviceAssertion_rejectsAReplayedSignatureFromAnotherLogin(t *testing.T) {
	_, priv, pubB64 := newDeviceKey(t)
	stolen := signAssertion(t, priv, "nonce-from-an-earlier-login")

	if _, err := VerifyDeviceAssertion("nonce-for-this-login", pubB64, stolen); err == nil {
		t.Fatal("a device assertion from a previous login replayed into a new one")
	}
}

// The account signature and the device assertion cover different bytes, so
// neither can be presented as the other. Domain separation, verified rather
// than assumed.
func TestDeviceAssertionMessage_isDomainSeparatedFromTheRawChallenge(t *testing.T) {
	const challenge = "nonce-abc123"
	msg := string(DeviceAssertionMessage(challenge))

	if msg == challenge {
		t.Fatal("the device signs the bare challenge — an account signature could be replayed as a device assertion")
	}
	if !strings.HasSuffix(msg, challenge) {
		t.Errorf("assertion message %q does not bind the challenge", msg)
	}
}

// A raw signature over the bare challenge (what a naive client, or an attacker
// holding only an account signature, would present) must not verify.
func TestVerifyDeviceAssertion_rejectsASignatureOverTheBareChallenge(t *testing.T) {
	_, priv, pubB64 := newDeviceKey(t)
	const challenge = "nonce-abc123"
	bare := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(challenge)))

	if _, err := VerifyDeviceAssertion(challenge, pubB64, bare); err == nil {
		t.Fatal("a signature over the undomain-separated challenge verified")
	}
}

// Malformed input is rejected with a clear error rather than panicking or
// being coerced into a valid-looking result.
func TestVerifyDeviceAssertion_rejectsMalformedInput(t *testing.T) {
	_, priv, pubB64 := newDeviceKey(t)
	const challenge = "nonce-abc123"
	good := signAssertion(t, priv, challenge)

	for name, tc := range map[string]struct{ pub, sig, challenge string }{
		"public key not base64": {"not!base64!", good, challenge},
		"public key wrong size": {base64.StdEncoding.EncodeToString([]byte("short")), good, challenge},
		"signature not base64":  {pubB64, "not!base64!", challenge},
		"signature wrong size":  {pubB64, base64.StdEncoding.EncodeToString([]byte("short")), challenge},
		"empty challenge":       {pubB64, good, ""},
		"empty public key":      {"", good, challenge},
		"empty signature":       {pubB64, "", challenge},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyDeviceAssertion(tc.challenge, tc.pub, tc.sig); err == nil {
				t.Error("malformed device assertion accepted")
			}
		})
	}
}

// Distinct keys must produce distinct fingerprints, and the same key the same
// one — the fingerprint IS the identity a function authorizes against.
func TestDeviceFingerprint_isStableAndDistinct(t *testing.T) {
	pubA, _, _ := newDeviceKey(t)
	pubB, _, _ := newDeviceKey(t)

	if DeviceFingerprint(pubA) != DeviceFingerprint(pubA) {
		t.Error("fingerprint is not stable for one key")
	}
	if DeviceFingerprint(pubA) == DeviceFingerprint(pubB) {
		t.Error("two different device keys share a fingerprint")
	}
	if got := len(DeviceFingerprint(pubA)); got != deviceFingerprintBytes*2 {
		t.Errorf("fingerprint is %d hex chars, want %d", got, deviceFingerprintBytes*2)
	}
}

// --- claim plumbing -------------------------------------------------------

// The device claims must never be persisted onto the refresh token.
//
// This is the cross-device forgery guard. reuseLastKnownClaims replays the most
// recent stored claims for a WALLET, with no device dimension — so a stored
// device claim would be replayed into a different device's login during a
// claims-provider hiccup, producing a correctly-signed token attributing device
// B's request to device A.
func TestStripDeviceClaims_removesOnlyTheDeviceClaims(t *testing.T) {
	in := map[string]string{
		"account_id":           "acct-1",
		"tier":                 "pro",
		DeviceClaimFingerprint: "fp-1",
		DeviceClaimSince:       "1700000000",
	}

	out := stripDeviceClaims(in)

	if _, ok := out[DeviceClaimFingerprint]; ok {
		t.Error("device fingerprint would be stored on the refresh token and replayed to another device")
	}
	if _, ok := out[DeviceClaimSince]; ok {
		t.Error("device_since would be stored on the refresh token")
	}
	if out["account_id"] != "acct-1" || out["tier"] != "pro" {
		t.Errorf("stripping device claims disturbed the namespace's own claims: %v", out)
	}
	if _, ok := in[DeviceClaimFingerprint]; !ok {
		t.Error("stripDeviceClaims mutated its input")
	}
}

// A claim set that was only device claims becomes nil, not an empty map that
// would marshal to a misleading "{}" on the refresh row.
func TestStripDeviceClaims_deviceOnlyBecomesNil(t *testing.T) {
	if out := stripDeviceClaims(map[string]string{DeviceClaimFingerprint: "fp-1"}); out != nil {
		t.Errorf("got %v, want nil", out)
	}
}

func TestWithDeviceClaims_stampsFingerprintAndFirstSeen(t *testing.T) {
	first := time.Unix(1700000000, 0).UTC()
	out := withDeviceClaims(map[string]string{"account_id": "acct-1"},
		&DeviceBinding{Fingerprint: "fp-1", FirstSeen: first})

	if out[DeviceClaimFingerprint] != "fp-1" {
		t.Errorf("device_fp = %q", out[DeviceClaimFingerprint])
	}
	if out[DeviceClaimSince] != "1700000000" {
		t.Errorf("device_since = %q, want the gateway's first-seen unix time", out[DeviceClaimSince])
	}
	if out["account_id"] != "acct-1" {
		t.Error("namespace claims were lost")
	}
}

// An account-only login (no assertion) must mint no device claim at all —
// never an empty one, which a function might read as "device present".
func TestWithDeviceClaims_nilDeviceAddsNothing(t *testing.T) {
	in := map[string]string{"account_id": "acct-1"}
	out := withDeviceClaims(in, nil)

	if _, ok := out[DeviceClaimFingerprint]; ok {
		t.Error("an account-only login carries a device claim")
	}
}

// withDeviceClaims must not mutate the caller's map — that map is what gets
// persisted to the refresh token, and mutating it would smuggle the device
// claims into storage despite stripDeviceClaims.
func TestWithDeviceClaims_doesNotMutateItsInput(t *testing.T) {
	in := map[string]string{"account_id": "acct-1"}
	_ = withDeviceClaims(in, &DeviceBinding{Fingerprint: "fp-1", FirstSeen: time.Now()})

	if _, ok := in[DeviceClaimFingerprint]; ok {
		t.Error("the map destined for the refresh token was mutated to include device claims")
	}
}

// "" must become SQL NULL, not an empty string: the revocation UPDATE matches
// on device_fp, and an empty string would collide with account-only rows.
func TestNullableFingerprint_emptyIsNull(t *testing.T) {
	if got := nullableFingerprint(""); got != nil {
		t.Errorf("got %v, want nil so the column is NULL", got)
	}
	if got := nullableFingerprint("fp-1"); got != "fp-1" {
		t.Errorf("got %v, want the fingerprint", got)
	}
}

func TestDeviceFingerprintOf_nilBindingIsNull(t *testing.T) {
	if got := deviceFingerprintOf(nil); got != nil {
		t.Errorf("got %v, want nil for an account-only login", got)
	}
}

// --- subject normalisation ------------------------------------------------

// The revocation bypass this closes: ETH signature verification is
// case-insensitive, so a revoked device can present a different casing of the
// same address. Without normalisation the UNIQUE(namespace_id, subject,
// device_fp) constraint does not collide, a fresh UN-REVOKED row is created,
// and the device is fully back — with a reset first_seen_at that also hands it
// archive access it had lost.
func TestNormalizeDeviceSubject_foldsEthAddressCasing(t *testing.T) {
	const checksummed = "0xAbCdEf0123456789AbCdEf0123456789AbCdEf01"
	lower := strings.ToLower(checksummed)

	if normalizeDeviceSubject(checksummed) != normalizeDeviceSubject(lower) {
		t.Error("two casings of one ETH address normalise differently — a revoked device could re-bind by changing case")
	}
	if got := normalizeDeviceSubject(checksummed); got != lower {
		t.Errorf("got %q, want the lowercase form %q", got, lower)
	}
}

// Solana addresses are base58 and case is significant: folding them would merge
// genuinely distinct accounts into one device namespace.
func TestNormalizeDeviceSubject_leavesBase58Untouched(t *testing.T) {
	const sol = "7EqQdEULxWcraVx3mXKFjc84LhCkMGZCkRuDpvcMwJeK"

	if got := normalizeDeviceSubject(sol); got != sol {
		t.Errorf("a Solana address was case-folded: %q → %q; distinct accounts would collide", sol, got)
	}
}

func TestNormalizeDeviceSubject_trimsWhitespace(t *testing.T) {
	if got := normalizeDeviceSubject("  0xABCDEF0123456789abcdef0123456789ABCDEF01  "); got != "0xabcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("got %q", got)
	}
}

// --- stored timestamp parsing ---------------------------------------------

// parseDeviceTime must REPORT failure rather than return a zero time.
//
// A zero time.Time has Unix() == -62135596800, so a silent fallback minted
// device_since = year 1 — older than every message, which under "a new device
// never receives the archive" grants the entire archive. That is the exact
// inverse of the rule, reached by a driver returning an unexpected layout.
func TestParseDeviceTime_reportsFailureInsteadOfYearOne(t *testing.T) {
	for name, in := range map[string]any{
		"unparseable string": "not-a-timestamp",
		"nil":                nil,
		"integer":            12345,
		"empty string":       "",
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := parseDeviceTime(in)
			if ok {
				t.Fatalf("claimed success for %v", in)
			}
			if got.Unix() > 0 {
				t.Errorf("returned a usable-looking time %v for unparseable input", got)
			}
		})
	}
}

func TestParseDeviceTime_acceptsTheDriverFormats(t *testing.T) {
	want := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	for name, in := range map[string]any{
		"time.Time": want,
		"RFC3339":   want.Format(time.RFC3339),
		"sqlite":    want.Format("2006-01-02 15:04:05"),
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := parseDeviceTime(in)
			if !ok {
				t.Fatalf("failed to parse %v", in)
			}
			if !got.Equal(want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

// --- read-after-write ------------------------------------------------------

// bindDeviceDB models rqlite's actual consistency: a write goes through raft and
// is NOT guaranteed visible to an immediately-following read.
//
// The first implementation did INSERT OR IGNORE then SELECT back, and failed on
// devnet the first time it ran — the row committed correctly, the read
// microseconds later returned nothing, and the code reported "binding vanished"
// as a 401. Unit tests missed it because the fake was instantly consistent, so
// this fake deliberately is not.
type bindDeviceDB struct {
	client.DatabaseClient
	rowVisible bool // whether the SELECT finds an existing binding
	selects    int
	inserts    int
}

// bindDeviceOrm wraps the fake so it satisfies the service's client seam.
type bindDeviceOrm struct {
	client.NetworkClient
	db *bindDeviceDB
}

func (o *bindDeviceOrm) Database() client.DatabaseClient { return o.db }

// newDeviceTestService builds a Service backed by the supplied fake database.
func newDeviceTestService(t *testing.T, db *bindDeviceDB) *Service {
	t.Helper()
	s := createDualKeyService(t)
	s.orm = &bindDeviceOrm{db: db}
	return s
}

func (d *bindDeviceDB) result(cols []string, rows [][]interface{}) *client.QueryResult {
	return &client.QueryResult{Count: int64(len(rows)), Rows: rows, Columns: cols}
}

func (d *bindDeviceDB) Query(_ context.Context, sql string, _ ...interface{}) (*client.QueryResult, error) {
	switch {
	case strings.Contains(sql, "INSERT OR IGNORE INTO namespaces"):
		return &client.QueryResult{Count: 0}, nil
	case strings.Contains(sql, "FROM namespaces"):
		return d.result([]string{"id"}, [][]interface{}{{int64(1)}}), nil
	case strings.Contains(sql, "INSERT OR IGNORE INTO orama_device_bindings"):
		d.inserts++
		// The write "succeeds" but stays invisible to reads, as raft replication
		// makes possible.
		return &client.QueryResult{Count: 0}, nil
	case strings.Contains(sql, "FROM orama_device_bindings"):
		d.selects++
		if !d.rowVisible {
			return &client.QueryResult{Count: 0}, nil
		}
		return d.result(
			[]string{"public_key", "first_seen_at", "revoked_at"},
			[][]interface{}{{"stored-pubkey", time.Unix(1700000000, 0).UTC(), nil}},
		), nil
	}
	return &client.QueryResult{Count: 0}, nil
}

// A first-time binding must succeed without ever reading back what it just
// wrote. This is the devnet failure, reproduced.
func TestBindDevice_firstBindDoesNotDependOnReadAfterWrite(t *testing.T) {
	db := &bindDeviceDB{rowVisible: false}
	s := newDeviceTestService(t, db)

	binding, err := s.BindDevice(context.Background(), "anchat-v2", "0xWALLET", "pubkey-b64", "fp-1")
	if err != nil {
		t.Fatalf("first bind failed on a database that does not serve reads-after-writes: %v", err)
	}
	if binding.Fingerprint != "fp-1" {
		t.Errorf("fingerprint = %q", binding.Fingerprint)
	}
	if binding.FirstSeen.IsZero() {
		t.Error("first_seen not set on a newly-created binding")
	}
	if db.inserts != 1 {
		t.Errorf("inserts = %d, want 1", db.inserts)
	}
}

// A returning device takes first_seen from STORAGE, never from the clock —
// otherwise device_since would advance on every login and a device would keep
// re-earning "joined just now", which is the whole property the archive rule
// depends on.
func TestBindDevice_returningDeviceKeepsStoredFirstSeen(t *testing.T) {
	db := &bindDeviceDB{rowVisible: true}
	s := newDeviceTestService(t, db)

	binding, err := s.BindDevice(context.Background(), "anchat-v2", "0xWALLET", "pubkey-b64", "fp-1")
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	if got := binding.FirstSeen.Unix(); got != 1700000000 {
		t.Errorf("first_seen = %d, want the stored 1700000000 — device_since must not advance on re-login", got)
	}
	if db.inserts != 0 {
		t.Errorf("a returning device re-inserted (%d); first_seen_at must be set once", db.inserts)
	}
}

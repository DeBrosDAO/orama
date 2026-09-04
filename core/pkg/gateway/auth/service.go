package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"go.uber.org/zap"
)

// Service handles authentication business logic
type Service struct {
	logger           *logging.ColoredLogger
	orm              client.NetworkClient
	db               rqlite.Client // lower-level client; used where rows-affected is needed (e.g. refresh-token CAS rotation, feature #68)
	signingKey       *rsa.PrivateKey
	keyID            string
	edSigningKey     ed25519.PrivateKey
	edKeyID          string
	preferEdDSA      bool
	defaultNS        string
	apiKeyHMACSecret string         // HMAC secret for hashing API keys before storage
	claimsResolver   ClaimsResolver // namespace claims-provider hook (bugboard #548); nil = none
	// apiKeyORM is the core/index RQLite client used for api_keys writes
	// on a namespace gateway (bugboard #162). Nil means use s.orm (the
	// main/index gateway, where orm already is the registry).
	apiKeyORM client.NetworkClient

	// revocations is the list of tokens this gateway refuses. Built in
	// NewService whenever there is a database to read it from.
	revocations *RevocationList

	// audit records the auth events worth keeping. Built alongside the
	// revocations, for the same reason: a Service with a database has one.
	audit *AuditLog
}

// minRSAKeyBits is the smallest RSA signing key this gateway will use. 2048 is
// the floor everyone agrees on; below it the signature on an access token is
// not worth checking.
const minRSAKeyBits = 2048

// ErrTokenRevoked is returned for a token whose signature verifies but which
// has been revoked — a key that was revoked, or a session that was ended.
var ErrTokenRevoked = errors.New("token revoked")

func NewService(logger *logging.ColoredLogger, orm client.NetworkClient, signingKeyPEM string, defaultNS string) (*Service, error) {
	s := &Service{
		logger:    logger,
		orm:       orm,
		defaultNS: defaultNS,
	}

	// Every Service with a database consults the revocations. There is no way
	// to build one that verifies tokens against a database and does not.
	if orm != nil {
		s.revocations = NewRevocationList(orm, logger)
		s.audit = NewAuditLog(orm, logger)
	}

	if signingKeyPEM != "" {
		block, _ := pem.Decode([]byte(signingKeyPEM))
		if block == nil {
			return nil, fmt.Errorf("failed to parse signing key PEM")
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse RSA private key: %w", err)
		}
		// A short RSA key signs tokens anybody can forge. The size was never
		// checked, so whatever PEM the operator supplied was used.
		if bits := key.N.BitLen(); bits < minRSAKeyBits {
			return nil, fmt.Errorf("the configured RSA signing key is %d bits; %d is the minimum, "+
				"below which the signature on an access token is not worth checking", bits, minRSAKeyBits)
		}
		s.signingKey = key

		// Generate a simple KID from the public key hash
		pubBytes := x509.MarshalPKCS1PublicKey(&key.PublicKey)
		sum := sha256.Sum256(pubBytes)
		s.keyID = hex.EncodeToString(sum[:8])
	}

	return s, nil
}

// SetAPIKeyHMACSecret configures the HMAC secret used to hash API keys before storage.
// When set, API keys are stored as HMAC-SHA256(key, secret) in the database.
func (s *Service) SetAPIKeyHMACSecret(secret string) {
	s.apiKeyHMACSecret = secret
}

// SetAPIKeyRegistry points API-key reads/writes at the core/index RQLite
// client. Required on namespace gateways after rqlite_dsn wins for
// deps.Client (bugboard #162): keys must not be stored in the tenant DB.
func (s *Service) SetAPIKeyRegistry(orm client.NetworkClient) {
	s.apiKeyORM = orm
}

func (s *Service) keyORM() client.NetworkClient {
	if s.apiKeyORM != nil {
		return s.apiKeyORM
	}
	return s.orm
}

// SetRqliteClient injects the lower-level rqlite client. Required for code
// paths that need rows-affected feedback for compare-and-swap operations
// (e.g. atomic refresh-token rotation, feature #68). The higher-level
// `client.NetworkClient` interface in `s.orm` does not expose RowsAffected
// on writes.
//
// Safe to call zero or one times; idempotent. Without it, methods that
// depend on CAS semantics fall back to the previous less-atomic behaviour
// (currently: RefreshToken returns ErrRotationNotConfigured).
func (s *Service) SetRqliteClient(db rqlite.Client) {
	s.db = db
}

// ClaimsResolver resolves additive, namespace-defined JWT custom claims for an
// authenticated wallet at token-mint time (bugboard #548/#920). The concrete
// implementation invokes the namespace's reserved `auth-claims-provider`
// serverless function; it MUST be fail-open (return nil, never error) so a
// missing/slow/broken provider never breaks authentication. Injected via
// SetClaimsResolver; nil = no custom claims (every namespace's default).
type ClaimsResolver interface {
	ResolveClaims(ctx context.Context, wallet, namespace string) map[string]string
}

// SetClaimsResolver wires the namespace claims-provider hook used at mint time.
func (s *Service) SetClaimsResolver(r ClaimsResolver) { s.claimsResolver = r }

// resolveCustomClaims returns the namespace's additive claims for this wallet,
// or nil. Fail-open by contract — the resolver never errors.
func (s *Service) resolveCustomClaims(ctx context.Context, wallet, namespace string) map[string]string {
	if s.claimsResolver == nil {
		return nil
	}
	return s.claimsResolver.ResolveClaims(ctx, wallet, namespace)
}

// lastKnownCustomClaims returns the most recently stored non-empty custom_claims
// for (nsID, subject) from refresh_tokens, or nil. It is the durable anti-
// fragmentation fallback (bugboard #143): once a wallet has EVER resolved
// account_id, a later TRANSIENT provider failure at login must not silently drop
// it — a device that registers under the wallet Sub instead of the stable
// account_id never receives fan-out push. Fail-soft: any error/empty → nil, so a
// missing history simply mints without custom claims (the pre-existing behaviour).
func (s *Service) lastKnownCustomClaims(ctx context.Context, nsID interface{}, subject string) map[string]string {
	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()
	// bugboard #154: exclude revoked rows. Without this a logged-out / rotated
	// row (or a stale row for a deleted-then-recreated identity) whose
	// expires_at is still in the future could resurrect stale claims (e.g. a
	// prior account_id). Only a live, non-revoked row is a trustworthy
	// last-known-good source.
	res, err := db.Query(internalCtx,
		`SELECT custom_claims FROM refresh_tokens
		 WHERE namespace_id = ? AND subject = ?
		   AND revoked_at IS NULL
		   AND custom_claims IS NOT NULL AND custom_claims != ''
		 ORDER BY expires_at DESC LIMIT 1`,
		nsID, subject)
	if err != nil || res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return nil
	}
	cc, ok := res.Rows[0][0].(string)
	if !ok {
		return nil
	}
	return unmarshalClaims(cc)
}

// reuseLastKnownClaims implements the durable anti-fragmentation fallback
// (bugboard #143). When this namespace HAS a claims provider (claimsResolver !=
// nil) but it just failed open (transient cold-WASM stall) yielding no
// account_id, the wallet's login-time devices would register under the wallet
// Sub instead of the stable account_id and never receive fan-out push. In that
// case reuse the last account_id this wallet ever resolved. Only runs when the
// resolver is configured AND returned empty — single-credential apps (no
// provider) are unaffected, and the refresh path is untouched (it already
// replays stored claims forward). Returns the claims to mint with.
func (s *Service) reuseLastKnownClaims(ctx context.Context, nsID interface{}, wallet, namespace string, custom map[string]string) map[string]string {
	if s.claimsResolver == nil || len(custom) > 0 {
		return custom
	}
	lkg := s.lastKnownCustomClaims(ctx, nsID, wallet)
	if len(lkg) == 0 {
		return custom
	}
	s.logger.ComponentInfo(logging.ComponentGeneral,
		"claims provider returned empty at login; reusing last-known-good custom claims (anti-fragmentation)",
		zap.String("namespace", namespace), zap.String("subject", wallet))
	return lkg
}

// ErrRotationNotConfigured is returned by RefreshToken when the service
// wasn't given an rqlite client — refusing to rotate without atomicity
// guarantees is safer than rotating non-atomically.
var ErrRotationNotConfigured = fmt.Errorf("auth service not configured for atomic refresh-token rotation (missing rqlite client)")

// NormalizeWallet canonicalises a wallet address for storage and lookup.
//
// The same wallet reaches the gateway in different cases: EIP-55 checksummed
// from one client, lowercase from another. Every row keyed by a wallet is
// matched by exact string equality, so unless both the write and the read
// normalise the same way one login records ownership that a later login cannot
// find, and the wallet ends up owning the namespace twice under two spellings.
func NormalizeWallet(wallet string) string {
	return strings.ToLower(strings.TrimSpace(wallet))
}

// HashAPIKey returns the HMAC-SHA256 hash of an API key if the HMAC secret is set,
// or returns the raw key for backward compatibility during rolling upgrade.
func (s *Service) HashAPIKey(key string) string {
	if s.apiKeyHMACSecret == "" {
		return key
	}
	return HmacSHA256Hex(key, s.apiKeyHMACSecret)
}

// SetEdDSAKey configures an Ed25519 signing key for EdDSA JWT support.
// When set, new tokens are signed with EdDSA; RS256 is still accepted for verification.
func (s *Service) SetEdDSAKey(privKey ed25519.PrivateKey) {
	s.edSigningKey = privKey
	pubBytes := []byte(privKey.Public().(ed25519.PublicKey))
	sum := sha256.Sum256(pubBytes)
	s.edKeyID = "ed_" + hex.EncodeToString(sum[:8])
	s.preferEdDSA = true
}

// CreateNonce generates a new nonce and stores it in the database
func (s *Service) CreateNonce(ctx context.Context, wallet, purpose, namespace string) (string, error) {
	// Generate a URL-safe random nonce (32 bytes)
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	nonce := base64.RawURLEncoding.EncodeToString(buf)

	// Use internal context to bypass authentication for system operations
	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()

	// Shared with ConsumeNonce so a nonce is always claimed from the namespace
	// it was filed under.
	namespace = s.nonceNamespace(namespace)

	// The namespace must already exist. This used to be an INSERT OR IGNORE,
	// so an unauthenticated POST to /v1/auth/challenge created a namespace for
	// any name at all — name squatting was free, and signing in to a name
	// nobody had taken silently created it. Creating one is its own
	// authenticated call now.
	nsID, err := s.lookupNamespaceID(ctx, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}
	if nsID == nil {
		return "", &ErrNamespaceUnknown{Namespace: namespace}
	}

	// A challenge writes a Raft-replicated row, and nothing proves the caller
	// owns the wallet it names. Without a ceiling on how many can be
	// outstanding at once, a grind fills the table for that wallet.
	if err := s.checkOutstandingNonces(internalCtx, db, nsID, normalizeNonceWallet(wallet), namespace); err != nil {
		return "", err
	}

	// Store nonce with 5 minute expiry. ConsumeNonce matches this row by exact
	// string equality, so both sides normalise the wallet the same way.
	walletLower := normalizeNonceWallet(wallet)
	if _, err := db.Query(internalCtx,
		"INSERT INTO nonces(namespace_id, wallet, nonce, purpose, expires_at) VALUES (?, ?, ?, ?, datetime('now', '+5 minutes'))",
		nsID, walletLower, nonce, purpose,
	); err != nil {
		return "", fmt.Errorf("failed to store nonce: %w", err)
	}

	return nonce, nil
}

// VerifySignature verifies a wallet signature for a given nonce
func (s *Service) VerifySignature(ctx context.Context, wallet, nonce, signature, chainType string) (bool, error) {
	chainType = strings.ToUpper(strings.TrimSpace(chainType))
	if chainType == "" {
		chainType = "ETH"
	}

	switch chainType {
	case "ETH":
		return s.verifyEthSignature(wallet, nonce, signature)
	case "SOL":
		return s.verifySolSignature(wallet, nonce, signature)
	default:
		return false, fmt.Errorf("unsupported chain type: %s", chainType)
	}
}

func (s *Service) verifyEthSignature(wallet, nonce, signature string) (bool, error) {
	msg := []byte(nonce)
	prefix := []byte("\x19Ethereum Signed Message:\n" + strconv.Itoa(len(msg)))
	hash := ethcrypto.Keccak256(prefix, msg)

	sigHex := strings.TrimSpace(signature)
	if strings.HasPrefix(sigHex, "0x") || strings.HasPrefix(sigHex, "0X") {
		sigHex = sigHex[2:]
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != 65 {
		return false, fmt.Errorf("invalid signature format")
	}

	if sig[64] >= 27 {
		sig[64] -= 27
	}

	pub, err := ethcrypto.SigToPub(hash, sig)
	if err != nil {
		return false, fmt.Errorf("signature recovery failed: %w", err)
	}

	addr := ethcrypto.PubkeyToAddress(*pub).Hex()
	want := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(wallet, "0x"), "0X"))
	got := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(addr, "0x"), "0X"))

	return got == want, nil
}

func (s *Service) verifySolSignature(wallet, nonce, signature string) (bool, error) {
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false, fmt.Errorf("invalid base64 signature: %w", err)
	}
	if len(sig) != 64 {
		return false, fmt.Errorf("invalid signature length: expected 64 bytes, got %d", len(sig))
	}

	pubKeyBytes, err := s.Base58Decode(wallet)
	if err != nil {
		return false, fmt.Errorf("invalid wallet address: %w", err)
	}
	if len(pubKeyBytes) != 32 {
		return false, fmt.Errorf("invalid public key length: expected 32 bytes, got %d", len(pubKeyBytes))
	}

	message := []byte(nonce)
	return ed25519.Verify(ed25519.PublicKey(pubKeyBytes), message, sig), nil
}

// IssueTokens generates access and refresh tokens for a verified wallet
func (s *Service) IssueTokens(ctx context.Context, wallet, namespace string) (string, string, int64, error) {
	if s.signingKey == nil {
		return "", "", 0, fmt.Errorf("signing key unavailable")
	}

	// Resolve namespace-defined additive claims (bugboard #548) ONCE at mint
	// time. Stored with the refresh token below and replayed across rotations
	// so the 15-min refresh path never re-invokes the provider.
	custom := s.resolveCustomClaims(ctx, wallet, namespace)

	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to resolve namespace ID: %w", err)
	}

	custom = s.reuseLastKnownClaims(ctx, nsID, wallet, namespace, custom)

	// Issue access token (15m)
	token, expUnix, err := s.GenerateJWT(namespace, wallet, 15*time.Minute, custom)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to generate JWT: %w", err)
	}

	// Create refresh token (30d)
	rbuf := make([]byte, 32)
	if _, err := rand.Read(rbuf); err != nil {
		return "", "", 0, fmt.Errorf("failed to generate refresh token: %w", err)
	}
	refresh := base64.RawURLEncoding.EncodeToString(rbuf)

	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()
	hashedRefresh := sha256Hex(refresh)
	if _, err := db.Query(internalCtx,
		"INSERT INTO refresh_tokens(namespace_id, subject, token, audience, expires_at, custom_claims) VALUES (?, ?, ?, ?, datetime('now', '+30 days'), ?)",
		nsID, wallet, hashedRefresh, "gateway", marshalClaims(custom),
	); err != nil {
		return "", "", 0, fmt.Errorf("failed to store refresh token: %w", err)
	}

	return token, refresh, expUnix, nil
}

// ErrRefreshTokenReplay is returned when a refresh token's CAS lock is lost —
// the row was already revoked between our read and our write, meaning either
// another concurrent request rotated it OR an attacker is replaying a stolen
// token after the legitimate client refreshed. Callers should treat this as
// a potential security event and surface 401 to the client; the service
// itself emits a WARN log so operators can audit.
//
// This is the tripwire promised by RFC 9700 §4.12 (refresh-token rotation).
var ErrRefreshTokenReplay = fmt.Errorf("refresh token already rotated or invalid")

// ErrRefreshTransient is returned when refresh-token rotation fails for a
// RETRYABLE reason — an rqlite-layer error rather than a genuine bad/expired
// token. Bugboard #125: during a rolling gateway restart the rqlite leader is
// briefly unavailable (re-election window), so the lookup/rotation errors;
// collapsing that into "invalid token" forces a 401 → full SIWE re-auth, which
// is impossible on a locked device answering a VoIP-woken call. Callers MUST
// surface this as a retryable 503, NOT a 401, so the client retries within the
// ring window instead of tearing down the session.
var ErrRefreshTransient = fmt.Errorf("refresh token rotation temporarily unavailable")

const (
	// refreshSelectRetries bounds how many times the refresh lookup is retried
	// when the rqlite read errors (transient leader unavailability). The read
	// is idempotent and happens BEFORE any write, so retrying is safe.
	refreshSelectRetries = 3
	// refreshSelectRetryDelay is the backoff between lookup retries. Three
	// tries × 250ms rides out a brief leader re-election without adding
	// meaningful latency to the common (healthy-leader) path.
	refreshSelectRetryDelay = 250 * time.Millisecond
	// refreshReuseGrace is how long after a refresh token is rotated (revoked)
	// the gateway will still accept it ONE more time, to recover a client whose
	// rotation response was lost in transit — otherwise the retry dead-ends in a
	// 401 → SIWE, impossible on a VoIP-woken locked screen (bugboard #125, RFC
	// 9700 §4.13.2). Kept short, and single-use via grace_used_at, so a stolen
	// token cannot be replayed at leisure.
	refreshReuseGrace = 60 * time.Second
)

// RefreshToken validates the supplied refresh token, atomically rotates it
// (revokes the old, mints a new), and returns a fresh access token alongside
// the rotated refresh token.
//
// Rotation is the RFC 9700 BCP §4.12 / feature #68 behaviour:
//
//  1. SELECT the subject for the supplied token (must be unrevoked + unexpired)
//  2. UPDATE revoked_at = now() WHERE token = ? AND revoked_at IS NULL
//     -- this is the atomic CAS. If RowsAffected == 0, the race was lost
//     -- (concurrent rotation or token-replay attack); we fail closed and
//     -- emit a security log line so operators can investigate.
//  3. Generate a fresh refresh-token + fresh access JWT
//  4. INSERT the new refresh-token row
//  5. Return both
//
// Failure modes:
//   - Token invalid/expired at step 1 → standard "invalid or expired" error,
//     no security event.
//   - CAS lost at step 2 → ErrRefreshTokenReplay, WARN logged with subject +
//     namespace. The client sees 401.
//   - Crash between step 2 and step 4 → user is left with revoked old + no
//     new, forcing re-login. Acceptable: degrades to re-auth, never enables
//     double-use of a single refresh token.
//
// Returns:
//
//	accessToken     — newly minted short-lived JWT (15 min)
//	newRefreshToken — newly minted long-lived refresh token (30 days)
//	subject         — wallet/subject claim of the refreshed session
//	expUnix         — access token expiry (unix seconds)
//	err             — non-nil on any failure; ErrRefreshTokenReplay for CAS loss
func (s *Service) RefreshToken(ctx context.Context, refreshToken, namespace string) (accessToken, newRefreshToken, subject string, expUnix int64, err error) {
	// Atomic rotation requires the lower-level rqlite client (RowsAffected
	// feedback isn't exposed by the higher-level client.NetworkClient).
	// Refuse to rotate non-atomically — see ErrRotationNotConfigured.
	if s.db == nil {
		return "", "", "", 0, ErrRotationNotConfigured
	}

	internalCtx := client.WithInternalAuth(ctx)
	ormDB := s.orm.Database()

	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		// Bugboard #125: namespace resolution runs an rqlite query BEFORE the
		// token lookup, so a leader re-election during a rolling restart fails
		// here too. Treat it as retryable (→ 503), not a bad token (→ 401) —
		// the refresh-path namespace comes from an already-authenticated
		// session, so a resolution failure is a transient DB error, never the
		// client's fault.
		s.logger.ComponentWarn(logging.ComponentGeneral,
			"refresh namespace resolution failed (transient, surfacing retryable)",
			zap.String("namespace", namespace),
			zap.Error(err))
		return "", "", "", 0, ErrRefreshTransient
	}

	hashedRefresh := sha256Hex(refreshToken)

	// Step 1: read the subject. Tells us who the token belongs to AND
	// validates that it's currently usable (not revoked, not expired).
	//
	// Bugboard #125: distinguish a TRANSIENT rqlite error (leader briefly
	// unavailable during a rolling restart) from a GENUINE token miss. The
	// read is idempotent and pre-write, so we retry it a few times; only after
	// exhausting retries do we surface ErrRefreshTransient (→ 503, client
	// retries). An actual empty result (Count == 0) is a real bad/expired
	// token → "invalid or expired" (→ 401). Collapsing the two used to 401 a
	// valid session during every restart, defeating the VoIP-wake refresh.
	selectQ := `SELECT subject, custom_claims FROM refresh_tokens
	            WHERE namespace_id = ? AND token = ?
	              AND revoked_at IS NULL
	              AND (expires_at IS NULL OR expires_at > datetime('now'))
	            LIMIT 1`
	var res *client.QueryResult
	var selErr error
	for attempt := 0; attempt < refreshSelectRetries; attempt++ {
		res, selErr = ormDB.Query(internalCtx, selectQ, nsID, hashedRefresh)
		if selErr == nil && res != nil {
			break
		}
		if attempt < refreshSelectRetries-1 {
			time.Sleep(refreshSelectRetryDelay)
		}
	}
	if selErr != nil || res == nil {
		// rqlite error persisted across retries — leader likely mid-election.
		// Retryable, NOT an invalid token.
		s.logger.ComponentWarn(logging.ComponentGeneral,
			"refresh token lookup failed (transient rqlite error, surfacing retryable)",
			zap.String("namespace", namespace),
			zap.Error(selErr))
		return "", "", "", 0, ErrRefreshTransient
	}
	// graceRecovery is set when the presented token was NOT in the active set
	// but qualifies for the bugboard #125 single-use reuse grace (a just-
	// rotated token whose rotation response was lost). In that case the old row
	// is already revoked, so we SKIP the revoke CAS (step 2) — the grace CAS
	// inside tryRefreshReuseGrace is our single-use lock — and go straight to
	// minting a fresh session.
	graceRecovery := false
	var custom map[string]string
	if res.Count == 0 {
		gSubject, gCustom, gOK, gErr := s.tryRefreshReuseGrace(internalCtx, ormDB, nsID, hashedRefresh)
		if gErr != nil {
			// Transient rqlite error during the grace lookup/claim — retryable,
			// not a verdict on the token (bugboard #125).
			s.logger.ComponentWarn(logging.ComponentGeneral,
				"refresh reuse-grace lookup failed (transient rqlite error, surfacing retryable)",
				zap.String("namespace", namespace), zap.Error(gErr))
			return "", "", "", 0, ErrRefreshTransient
		}
		if !gOK {
			// Genuinely not found / revoked outside grace / grace already
			// consumed / expired — a real bad token.
			return "", "", "", 0, fmt.Errorf("invalid or expired refresh token")
		}
		subject = gSubject
		custom = gCustom
		graceRecovery = true
		s.logger.ComponentInfo(logging.ComponentGeneral,
			"refresh token reuse-grace recovery (lost-response retry, single-use)",
			zap.String("namespace", namespace), zap.String("subject", subject))
	} else {
		var customClaimsJSON string
		if len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
			if val, ok := res.Rows[0][0].(string); ok {
				subject = val
			} else {
				b, _ := json.Marshal(res.Rows[0][0])
				_ = json.Unmarshal(b, &subject)
			}
			// custom_claims (bugboard #548) — resolved once at login, replayed on
			// every rotation so the refresh path never re-invokes the provider.
			if len(res.Rows[0]) > 1 {
				if cc, ok := res.Rows[0][1].(string); ok {
					customClaimsJSON = cc
				}
			}
		}
		custom = unmarshalClaims(customClaimsJSON)
	}

	// bugboard #154: an empty-claims session must be able to self-heal on
	// refresh. A JWT minted BEFORE the app's account row exists (e.g. AnChat
	// signup: token minted at SIWE-verify, account_id resolves 5-25s later)
	// stored empty custom_claims; the pre-existing behaviour replayed that
	// emptiness for the whole ~30-day refresh chain, so account_id-gated
	// features (push routing) stayed broken until a manual re-login. When the
	// stored claims are EMPTY and this namespace has a provider, RE-RESOLVE
	// here so the session converges within one refresh cycle (~15 min).
	// Scoped to empty-claims sessions only: a healthy (non-empty) session never
	// touches the provider, so the hot path pays nothing. resolveCustomClaims
	// is fail-open (never errors) and we only overwrite on a non-empty result,
	// so a still-unavailable provider leaves custom empty and refresh proceeds
	// unchanged. Self-terminating: once healed, subsequent refreshes see
	// non-empty claims and skip this entirely. subject is the wallet (stored as
	// the refresh-row subject at mint), exactly what the resolver expects.
	if len(custom) == 0 && s.claimsResolver != nil {
		if reResolved := s.resolveCustomClaims(internalCtx, subject, namespace); len(reResolved) > 0 {
			custom = reResolved
		}
	}

	// Step 2: atomic CAS — revoke the old row. RowsAffected is the lock.
	// Two concurrent calls with the same refresh token: exactly one wins
	// the UPDATE (RowsAffected == 1); the other sees RowsAffected == 0
	// and bails with the replay tripwire.
	//
	// Skipped on a grace recovery (bugboard #125): the token is ALREADY
	// revoked, so this CAS would always see RowsAffected == 0 and mis-fire the
	// replay tripwire. The single-use grace CAS (grace_used_at) inside
	// tryRefreshReuseGrace already served as the lock for this path.
	if !graceRecovery {
		updRes, err := s.db.Exec(internalCtx,
			`UPDATE refresh_tokens SET revoked_at = datetime('now')
			 WHERE namespace_id = ? AND token = ? AND revoked_at IS NULL`,
			nsID, hashedRefresh)
		if err != nil {
			// rqlite write error (leader unavailable) — retryable, not a bad
			// token. No row was revoked, so a client retry is safe (bugboard #125).
			s.logger.ComponentWarn(logging.ComponentGeneral,
				"refresh token revoke failed (transient rqlite error, surfacing retryable)",
				zap.String("namespace", namespace),
				zap.Error(err))
			return "", "", "", 0, ErrRefreshTransient
		}
		affected, _ := updRes.RowsAffected()
		if affected == 0 {
			// Race lost OR replay attempt: token was unrevoked at step 1 but
			// already revoked by step 2, meaning a concurrent call rotated it
			// in between. Could be benign (same client retrying due to a
			// transient network error) or malicious (stolen token + race).
			// Either way: fail closed, log it, let the operator investigate.
			s.logger.ComponentWarn(logging.ComponentGeneral,
				"refresh token rotation: concurrent use detected (possible replay)",
				zap.String("namespace", namespace),
				zap.String("subject", subject))
			return "", "", "", 0, ErrRefreshTokenReplay
		}
	}

	// Step 3: mint the new access JWT, carrying forward the stored custom
	// claims so a rotated token keeps the same account_id etc. (bugboard #548).
	accessToken, expUnix, err = s.GenerateJWT(namespace, subject, 15*time.Minute, custom)
	if err != nil {
		return "", "", "", 0, fmt.Errorf("generate access token: %w", err)
	}

	// Step 4: mint and persist a new refresh token (32-byte random,
	// base64-url-encoded; stored hashed). 30-day TTL. Note: if this
	// INSERT fails after the UPDATE succeeded (step 2), the user is left
	// with revoked old + no new and must re-authenticate. Acceptable —
	// degrades to re-auth, never to double-use of a single refresh token.
	rbuf := make([]byte, 32)
	if _, err := rand.Read(rbuf); err != nil {
		return "", "", "", 0, fmt.Errorf("generate refresh token: %w", err)
	}
	newRefreshToken = base64.RawURLEncoding.EncodeToString(rbuf)
	hashedNew := sha256Hex(newRefreshToken)
	// Re-marshal from the parsed map (not the raw stored string) so the new
	// row and the freshly-minted access token are provably consistent and
	// self-healing — a malformed stored blob converges to "" on both sides
	// rather than being propagated forward verbatim. custom_claims is written
	// ONLY here and in IssueTokens, both from a sanitized map (bugboard #548).
	if _, err := ormDB.Query(internalCtx,
		"INSERT INTO refresh_tokens(namespace_id, subject, token, audience, expires_at, custom_claims) VALUES (?, ?, ?, ?, datetime('now', '+30 days'), ?)",
		nsID, subject, hashedNew, "gateway", marshalClaims(custom)); err != nil {
		// The old token is already revoked (step 2). A retryable error here
		// leaves the client to re-attempt — which will re-auth since the old
		// token is gone — but that's strictly better than masking a transient
		// failure as a permanent 401 (bugboard #125). Surface retryable.
		s.logger.ComponentWarn(logging.ComponentGeneral,
			"refresh token store failed after revoke (transient rqlite error)",
			zap.String("namespace", namespace),
			zap.Error(err))
		return "", "", "", 0, ErrRefreshTransient
	}

	return accessToken, newRefreshToken, subject, expUnix, nil
}

// tryRefreshReuseGrace implements the bounded, single-use reuse grace for a
// rotated refresh token (bugboard #125, RFC 9700 §4.13.2). A token revoked
// within refreshReuseGrace whose grace_used_at is still NULL is accepted ONCE
// more — recovering a client that lost its rotation response in transit (a
// reconnect storm during a gateway roll) before it dead-ends in a 401 → SIWE.
//
// Returns (subject, custom, true, nil) on a successful single-use grace claim;
// (—, —, false, nil) when there is no eligible row, the token was revoked
// outside the grace window, it has expired, or the grace was already consumed
// (caller → 401). A non-nil error is a transient rqlite failure (caller → 503).
//
// Security: the grace is both short-windowed AND single-use (a CAS on
// grace_used_at), so a stolen token cannot be replayed repeatedly; and it never
// touches the concurrent-rotation replay tripwire, which fires on the active
// path only.
func (s *Service) tryRefreshReuseGrace(ctx context.Context, ormDB client.DatabaseClient, nsID interface{}, hashedRefresh string) (subject string, custom map[string]string, ok bool, err error) {
	graceArg := fmt.Sprintf("-%d seconds", int(refreshReuseGrace.Seconds()))
	sel := `SELECT subject, custom_claims FROM refresh_tokens
	        WHERE namespace_id = ? AND token = ?
	          AND revoked_at IS NOT NULL
	          AND revoked_at > datetime('now', ?)
	          AND grace_used_at IS NULL
	          AND (expires_at IS NULL OR expires_at > datetime('now'))
	        LIMIT 1`
	res, qerr := ormDB.Query(ctx, sel, nsID, hashedRefresh, graceArg)
	if qerr != nil {
		return "", nil, false, qerr // transient rqlite error → caller 503
	}
	if res == nil || res.Count == 0 {
		return "", nil, false, nil // no eligible grace row → caller 401
	}

	var customClaimsJSON string
	if len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
		if v, vok := res.Rows[0][0].(string); vok {
			subject = v
		} else {
			b, _ := json.Marshal(res.Rows[0][0])
			_ = json.Unmarshal(b, &subject)
		}
		if len(res.Rows[0]) > 1 {
			if cc, cok := res.Rows[0][1].(string); cok {
				customClaimsJSON = cc
			}
		}
	}
	if subject == "" {
		return "", nil, false, nil // defensive: never grace-mint an anonymous session
	}

	// Single-use CAS: claim the grace. Exactly one caller wins; a concurrent
	// replay of the same just-revoked token sees RowsAffected == 0 → no grace.
	// The same time-window predicate is repeated so the claim can't succeed on a
	// row that aged out of the window between the SELECT and here.
	updRes, uerr := s.db.Exec(ctx,
		`UPDATE refresh_tokens SET grace_used_at = datetime('now')
		 WHERE namespace_id = ? AND token = ? AND grace_used_at IS NULL
		   AND revoked_at IS NOT NULL AND revoked_at > datetime('now', ?)`,
		nsID, hashedRefresh, graceArg)
	if uerr != nil {
		return "", nil, false, uerr // transient
	}
	if affected, _ := updRes.RowsAffected(); affected == 0 {
		return "", nil, false, nil // grace already consumed (concurrent) → caller 401
	}
	return subject, unmarshalClaims(customClaimsJSON), true, nil
}

// RevokeToken revokes a specific refresh token or all tokens for a subject
func (s *Service) RevokeToken(ctx context.Context, namespace, token string, all bool, subject string) error {
	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()

	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return err
	}

	// Explicit revocation (logout / revoke-all) ALSO burns the reuse-grace slot
	// (grace_used_at) so a deliberately-revoked token can NEVER be recovered by
	// the bugboard #125 reuse grace. Rotation does not go through RevokeToken,
	// so the legitimate lost-response grace path is unaffected; this only closes
	// the logout-bypass where a just-logged-out token would otherwise be
	// grace-eligible for the 60s window.
	if token != "" {
		hashedToken := sha256Hex(token)
		_, err := db.Query(internalCtx, "UPDATE refresh_tokens SET revoked_at = datetime('now'), grace_used_at = datetime('now') WHERE namespace_id = ? AND token = ? AND revoked_at IS NULL", nsID, hashedToken)
		return err
	}

	if all && subject != "" {
		_, err := db.Query(internalCtx, "UPDATE refresh_tokens SET revoked_at = datetime('now'), grace_used_at = datetime('now') WHERE namespace_id = ? AND subject = ? AND revoked_at IS NULL", nsID, subject)
		return err
	}

	return fmt.Errorf("nothing to revoke")
}

// GetOrCreateAPIKey returns an existing API key or creates a new one for a wallet in a namespace.
//
// It refuses when the namespace belongs to another wallet. This used to be the
// takeover: the ownership row it wrote unconditionally made any wallet that
// named an existing namespace an admin co-owner of it, and the key it returned
// carried no scopes, which the read path treated as admin. Both halves are
// closed here — ownership is claimed before anything is minted, and the key is
// minted with the grant written down.
func (s *Service) GetOrCreateAPIKey(ctx context.Context, wallet, namespace string) (string, error) {
	internalCtx := client.WithInternalAuth(ctx)
	db := s.keyORM().Database()

	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return "", err
	}

	if err := s.ClaimNamespaceOwnership(ctx, db, nsID, namespace, wallet); err != nil {
		return "", err
	}

	// Try existing linkage
	var apiKey string
	r1, err := db.Query(internalCtx,
		"SELECT api_keys.key FROM wallet_api_keys JOIN api_keys ON wallet_api_keys.api_key_id = api_keys.id WHERE wallet_api_keys.namespace_id = ? AND LOWER(wallet_api_keys.wallet) = LOWER(?) LIMIT 1",
		nsID, wallet,
	)
	if err == nil && r1 != nil && r1.Count > 0 && len(r1.Rows) > 0 && len(r1.Rows[0]) > 0 {
		if val, ok := r1.Rows[0][0].(string); ok {
			apiKey = val
		}
	}

	if apiKey != "" {
		return apiKey, nil
	}

	// Create new API key
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate api key: %w", err)
	}
	apiKey = "ak_" + base64.RawURLEncoding.EncodeToString(buf) + ":" + namespace

	// Store the HMAC hash of the key (not the raw key) if HMAC secret is configured.
	//
	// The scope set is written, never left NULL. This is the owner's own key, so
	// the grant is admin — the same access an empty column used to be read as,
	// with the difference that it is now a decision on the row rather than an
	// inference in the reader, and a key minted with no scopes denies.
	hashedKey := s.HashAPIKey(apiKey)
	ownerScopes := ScopeSet{ScopeAdmin: {}}.Canonical()
	if _, err := db.Query(internalCtx,
		"INSERT INTO api_keys(key, name, namespace_id, scopes) VALUES (?, ?, ?, ?)",
		hashedKey, "", nsID, ownerScopes,
	); err != nil {
		return "", fmt.Errorf("failed to store api key: %w", err)
	}

	// Link wallet -> api_key. A key the caller cannot find again on their next
	// login is a new key every time, so this is not best-effort.
	rid, err := db.Query(internalCtx, "SELECT id FROM api_keys WHERE key = ? LIMIT 1", hashedKey)
	if err != nil || rid == nil || rid.Count == 0 || len(rid.Rows) == 0 || len(rid.Rows[0]) == 0 {
		return "", fmt.Errorf("api key stored for namespace %q but its id could not be read back: %w", namespace, err)
	}
	if _, err := db.Query(internalCtx,
		"INSERT OR IGNORE INTO wallet_api_keys(namespace_id, wallet, api_key_id) VALUES (?, ?, ?)",
		nsID, NormalizeWallet(wallet), rid.Rows[0][0],
	); err != nil {
		return "", fmt.Errorf("failed to link the api key to wallet in namespace %q: %w", namespace, err)
	}

	// The key owns the namespace as well: that row is how the namespace gate
	// recognizes a request authenticated by the key rather than by the wallet.
	// Without it the caller holds a key that is refused everywhere.
	if _, err := db.Query(internalCtx,
		"INSERT OR IGNORE INTO namespace_ownership(namespace_id, owner_type, owner_id) VALUES (?, 'api_key', ?)",
		nsID, hashedKey,
	); err != nil {
		return "", fmt.Errorf("failed to record key ownership of namespace %q: %w", namespace, err)
	}

	return apiKey, nil
}

// ResolveNamespaceID ensures the given namespace exists and returns its primary key ID.
func (s *Service) ResolveNamespaceID(ctx context.Context, ns string) (interface{}, error) {
	return s.resolveNamespaceIDOn(ctx, s.orm, ns)
}

func (s *Service) resolveKeyNamespaceID(ctx context.Context, ns string) (interface{}, error) {
	return s.resolveNamespaceIDOn(ctx, s.keyORM(), ns)
}

func (s *Service) resolveNamespaceIDOn(ctx context.Context, orm client.NetworkClient, ns string) (interface{}, error) {
	if orm == nil {
		return nil, fmt.Errorf("client not initialized")
	}
	ns = strings.TrimSpace(ns)
	if ns == "" {
		ns = "default"
	}

	internalCtx := client.WithInternalAuth(ctx)
	db := orm.Database()

	if _, err := db.Query(internalCtx, "INSERT OR IGNORE INTO namespaces(name) VALUES (?)", ns); err != nil {
		return nil, err
	}
	res, err := db.Query(internalCtx, "SELECT id FROM namespaces WHERE name = ? LIMIT 1", ns)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return nil, fmt.Errorf("failed to resolve namespace")
	}
	return res.Rows[0][0], nil
}

// Base58Decode decodes a base58-encoded string
func (s *Service) Base58Decode(input string) ([]byte, error) {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	answer := big.NewInt(0)
	j := big.NewInt(1)
	for i := len(input) - 1; i >= 0; i-- {
		tmp := strings.IndexByte(alphabet, input[i])
		if tmp == -1 {
			return nil, fmt.Errorf("invalid base58 character")
		}
		idx := big.NewInt(int64(tmp))
		tmp1 := new(big.Int)
		tmp1.Mul(idx, j)
		answer.Add(answer, tmp1)
		j.Mul(j, big.NewInt(58))
	}
	// Handle leading zeros
	res := answer.Bytes()
	for i := 0; i < len(input) && input[i] == alphabet[0]; i++ {
		res = append([]byte{0}, res...)
	}
	return res, nil
}

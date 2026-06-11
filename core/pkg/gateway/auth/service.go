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
	apiKeyHMACSecret string // HMAC secret for hashing API keys before storage
}

func NewService(logger *logging.ColoredLogger, orm client.NetworkClient, signingKeyPEM string, defaultNS string) (*Service, error) {
	s := &Service{
		logger:    logger,
		orm:       orm,
		defaultNS: defaultNS,
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

// ErrRotationNotConfigured is returned by RefreshToken when the service
// wasn't given an rqlite client — refusing to rotate without atomicity
// guarantees is safer than rotating non-atomically.
var ErrRotationNotConfigured = fmt.Errorf("auth service not configured for atomic refresh-token rotation (missing rqlite client)")

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

	if namespace == "" {
		namespace = s.defaultNS
		if namespace == "" {
			namespace = "default"
		}
	}

	// Ensure namespace exists
	if _, err := db.Query(internalCtx, "INSERT OR IGNORE INTO namespaces(name) VALUES (?)", namespace); err != nil {
		return "", fmt.Errorf("failed to ensure namespace: %w", err)
	}

	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve namespace ID: %w", err)
	}

	// Store nonce with 5 minute expiry
	walletLower := strings.ToLower(strings.TrimSpace(wallet))
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

	// Issue access token (15m)
	token, expUnix, err := s.GenerateJWT(namespace, wallet, 15*time.Minute)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to generate JWT: %w", err)
	}

	// Create refresh token (30d)
	rbuf := make([]byte, 32)
	if _, err := rand.Read(rbuf); err != nil {
		return "", "", 0, fmt.Errorf("failed to generate refresh token: %w", err)
	}
	refresh := base64.RawURLEncoding.EncodeToString(rbuf)

	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to resolve namespace ID: %w", err)
	}

	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()
	hashedRefresh := sha256Hex(refresh)
	if _, err := db.Query(internalCtx,
		"INSERT INTO refresh_tokens(namespace_id, subject, token, audience, expires_at) VALUES (?, ?, ?, ?, datetime('now', '+30 days'))",
		nsID, wallet, hashedRefresh, "gateway",
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
		return "", "", "", 0, err
	}

	hashedRefresh := sha256Hex(refreshToken)

	// Step 1: read the subject. Tells us who the token belongs to AND
	// validates that it's currently usable (not revoked, not expired).
	selectQ := `SELECT subject FROM refresh_tokens
	            WHERE namespace_id = ? AND token = ?
	              AND revoked_at IS NULL
	              AND (expires_at IS NULL OR expires_at > datetime('now'))
	            LIMIT 1`
	res, err := ormDB.Query(internalCtx, selectQ, nsID, hashedRefresh)
	if err != nil || res == nil || res.Count == 0 {
		return "", "", "", 0, fmt.Errorf("invalid or expired refresh token")
	}
	if len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
		if val, ok := res.Rows[0][0].(string); ok {
			subject = val
		} else {
			b, _ := json.Marshal(res.Rows[0][0])
			_ = json.Unmarshal(b, &subject)
		}
	}

	// Step 2: atomic CAS — revoke the old row. RowsAffected is the lock.
	// Two concurrent calls with the same refresh token: exactly one wins
	// the UPDATE (RowsAffected == 1); the other sees RowsAffected == 0
	// and bails with the replay tripwire.
	updRes, err := s.db.Exec(internalCtx,
		`UPDATE refresh_tokens SET revoked_at = datetime('now')
		 WHERE namespace_id = ? AND token = ? AND revoked_at IS NULL`,
		nsID, hashedRefresh)
	if err != nil {
		return "", "", "", 0, fmt.Errorf("revoke old refresh token: %w", err)
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

	// Step 3: mint the new access JWT.
	accessToken, expUnix, err = s.GenerateJWT(namespace, subject, 15*time.Minute)
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
	if _, err := ormDB.Query(internalCtx,
		"INSERT INTO refresh_tokens(namespace_id, subject, token, audience, expires_at) VALUES (?, ?, ?, ?, datetime('now', '+30 days'))",
		nsID, subject, hashedNew, "gateway"); err != nil {
		return "", "", "", 0, fmt.Errorf("store rotated refresh token: %w", err)
	}

	return accessToken, newRefreshToken, subject, expUnix, nil
}

// RevokeToken revokes a specific refresh token or all tokens for a subject
func (s *Service) RevokeToken(ctx context.Context, namespace, token string, all bool, subject string) error {
	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()

	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return err
	}

	if token != "" {
		hashedToken := sha256Hex(token)
		_, err := db.Query(internalCtx, "UPDATE refresh_tokens SET revoked_at = datetime('now') WHERE namespace_id = ? AND token = ? AND revoked_at IS NULL", nsID, hashedToken)
		return err
	}

	if all && subject != "" {
		_, err := db.Query(internalCtx, "UPDATE refresh_tokens SET revoked_at = datetime('now') WHERE namespace_id = ? AND subject = ? AND revoked_at IS NULL", nsID, subject)
		return err
	}

	return fmt.Errorf("nothing to revoke")
}

// RegisterApp registers a new client application
func (s *Service) RegisterApp(ctx context.Context, wallet, namespace, name, publicKey string) (string, error) {
	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()

	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return "", err
	}

	// Generate client app_id
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate app id: %w", err)
	}
	appID := "app_" + base64.RawURLEncoding.EncodeToString(buf)

	// Persist app
	if _, err := db.Query(internalCtx, "INSERT INTO apps(namespace_id, app_id, name, public_key) VALUES (?, ?, ?, ?)", nsID, appID, name, publicKey); err != nil {
		return "", err
	}

	// Record ownership
	_, _ = db.Query(internalCtx, "INSERT OR IGNORE INTO namespace_ownership(namespace_id, owner_type, owner_id) VALUES (?, ?, ?)", nsID, "wallet", wallet)

	return appID, nil
}

// GetOrCreateAPIKey returns an existing API key or creates a new one for a wallet in a namespace
func (s *Service) GetOrCreateAPIKey(ctx context.Context, wallet, namespace string) (string, error) {
	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()

	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
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

	// Store the HMAC hash of the key (not the raw key) if HMAC secret is configured
	hashedKey := s.HashAPIKey(apiKey)
	if _, err := db.Query(internalCtx, "INSERT INTO api_keys(key, name, namespace_id) VALUES (?, ?, ?)", hashedKey, "", nsID); err != nil {
		return "", fmt.Errorf("failed to store api key: %w", err)
	}

	// Link wallet -> api_key
	rid, err := db.Query(internalCtx, "SELECT id FROM api_keys WHERE key = ? LIMIT 1", hashedKey)
	if err == nil && rid != nil && rid.Count > 0 && len(rid.Rows) > 0 && len(rid.Rows[0]) > 0 {
		apiKeyID := rid.Rows[0][0]
		_, _ = db.Query(internalCtx, "INSERT OR IGNORE INTO wallet_api_keys(namespace_id, wallet, api_key_id) VALUES (?, ?, ?)", nsID, strings.ToLower(wallet), apiKeyID)
	}

	// Record ownerships — store the hash in ownership too
	_, _ = db.Query(internalCtx, "INSERT OR IGNORE INTO namespace_ownership(namespace_id, owner_type, owner_id) VALUES (?, 'api_key', ?)", nsID, hashedKey)
	_, _ = db.Query(internalCtx, "INSERT OR IGNORE INTO namespace_ownership(namespace_id, owner_type, owner_id) VALUES (?, 'wallet', ?)", nsID, wallet)

	return apiKey, nil
}

// ResolveNamespaceID ensures the given namespace exists and returns its primary key ID.
func (s *Service) ResolveNamespaceID(ctx context.Context, ns string) (interface{}, error) {
	if s.orm == nil {
		return nil, fmt.Errorf("client not initialized")
	}
	ns = strings.TrimSpace(ns)
	if ns == "" {
		ns = "default"
	}

	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()

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

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
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// Service handles authentication business logic
type Service struct {
	logger      *logging.ColoredLogger
	orm         client.NetworkClient
	signingKey  *rsa.PrivateKey
	keyID       string
	defaultNS   string
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
	if _, err := db.Query(internalCtx,
		"INSERT INTO refresh_tokens(namespace_id, subject, token, audience, expires_at) VALUES (?, ?, ?, ?, datetime('now', '+30 days'))",
		nsID, wallet, refresh, "gateway",
	); err != nil {
		return "", "", 0, fmt.Errorf("failed to store refresh token: %w", err)
	}

	return token, refresh, expUnix, nil
}

// RefreshToken validates a refresh token and issues a new access token
func (s *Service) RefreshToken(ctx context.Context, refreshToken, namespace string) (string, string, int64, error) {
	internalCtx := client.WithInternalAuth(ctx)
	db := s.orm.Database()

	nsID, err := s.ResolveNamespaceID(ctx, namespace)
	if err != nil {
		return "", "", 0, err
	}

	q := "SELECT subject FROM refresh_tokens WHERE namespace_id = ? AND token = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > datetime('now')) LIMIT 1"
	res, err := db.Query(internalCtx, q, nsID, refreshToken)
	if err != nil || res == nil || res.Count == 0 {
		return "", "", 0, fmt.Errorf("invalid or expired refresh token")
	}

	subject := ""
	if len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
		if val, ok := res.Rows[0][0].(string); ok {
			subject = val
		} else {
			b, _ := json.Marshal(res.Rows[0][0])
			_ = json.Unmarshal(b, &subject)
		}
	}

	token, expUnix, err := s.GenerateJWT(namespace, subject, 15*time.Minute)
	if err != nil {
		return "", "", 0, err
	}

	return token, subject, expUnix, nil
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
		_, err := db.Query(internalCtx, "UPDATE refresh_tokens SET revoked_at = datetime('now') WHERE namespace_id = ? AND token = ? AND revoked_at IS NULL", nsID, token)
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

	if _, err := db.Query(internalCtx, "INSERT INTO api_keys(key, name, namespace_id) VALUES (?, ?, ?)", apiKey, "", nsID); err != nil {
		return "", fmt.Errorf("failed to store api key: %w", err)
	}

	// Link wallet -> api_key
	rid, err := db.Query(internalCtx, "SELECT id FROM api_keys WHERE key = ? LIMIT 1", apiKey)
	if err == nil && rid != nil && rid.Count > 0 && len(rid.Rows) > 0 && len(rid.Rows[0]) > 0 {
		apiKeyID := rid.Rows[0][0]
		_, _ = db.Query(internalCtx, "INSERT OR IGNORE INTO wallet_api_keys(namespace_id, wallet, api_key_id) VALUES (?, ?, ?)", nsID, strings.ToLower(wallet), apiKeyID)
	}

	// Record ownerships
	_, _ = db.Query(internalCtx, "INSERT OR IGNORE INTO namespace_ownership(namespace_id, owner_type, owner_id) VALUES (?, 'api_key', ?)", nsID, apiKey)
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

package contracts

import (
	"context"
	"time"
)

// AuthService handles wallet-based authentication and authorization.
// Provides nonce generation, signature verification, JWT lifecycle management,
// and application registration for the gateway.
type AuthService interface {
	// CreateNonce generates a cryptographic nonce for wallet authentication.
	// The nonce is valid for a limited time and used to prevent replay attacks.
	// wallet is the wallet address, purpose describes the nonce usage,
	// and namespace isolates nonces across different contexts.
	CreateNonce(ctx context.Context, wallet, purpose, namespace string) (string, error)

	// VerifySignature validates a cryptographic signature from a wallet.
	// Supports multiple blockchain types (ETH, SOL) for signature verification.
	// Returns true if the signature is valid for the given nonce.
	VerifySignature(ctx context.Context, wallet, nonce, signature, chainType string) (bool, error)

	// IssueTokens generates a new access token and refresh token pair.
	// Access tokens are short-lived (typically 15 minutes).
	// Refresh tokens are long-lived (typically 30 days).
	// Returns: accessToken, refreshToken, expirationUnix, error.
	IssueTokens(ctx context.Context, wallet, namespace string) (string, string, int64, error)

	// RefreshToken validates a refresh token and issues a new access token.
	// Returns: newAccessToken, subject (wallet), expirationUnix, error.
	RefreshToken(ctx context.Context, refreshToken, namespace string) (string, string, int64, error)

	// RevokeToken invalidates a refresh token or all tokens for a subject.
	// If token is provided, revokes that specific token.
	// If all is true and subject is provided, revokes all tokens for that subject.
	RevokeToken(ctx context.Context, namespace, token string, all bool, subject string) error

	// ParseAndVerifyJWT validates a JWT access token and returns its claims.
	// Verifies signature, expiration, and issuer.
	ParseAndVerifyJWT(token string) (*JWTClaims, error)

	// GenerateJWT creates a new signed JWT with the specified claims and TTL.
	// Returns: token, expirationUnix, error.
	GenerateJWT(namespace, subject string, ttl time.Duration) (string, int64, error)

	// RegisterApp registers a new client application with the gateway.
	// Returns an application ID that can be used for OAuth flows.
	RegisterApp(ctx context.Context, wallet, namespace, name, publicKey string) (string, error)

	// GetOrCreateAPIKey retrieves an existing API key or creates a new one.
	// API keys provide programmatic access without interactive authentication.
	GetOrCreateAPIKey(ctx context.Context, wallet, namespace string) (string, error)

	// ResolveNamespaceID ensures a namespace exists and returns its internal ID.
	// Creates the namespace if it doesn't exist.
	ResolveNamespaceID(ctx context.Context, namespace string) (interface{}, error)
}

// JWTClaims represents the claims contained in a JWT access token.
type JWTClaims struct {
	Iss       string `json:"iss"`       // Issuer
	Sub       string `json:"sub"`       // Subject (wallet address)
	Aud       string `json:"aud"`       // Audience
	Iat       int64  `json:"iat"`       // Issued At
	Nbf       int64  `json:"nbf"`       // Not Before
	Exp       int64  `json:"exp"`       // Expiration
	Namespace string `json:"namespace"` // Namespace isolation
}

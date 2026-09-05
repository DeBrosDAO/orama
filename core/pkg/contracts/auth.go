package contracts

import (
	"context"
	"time"
)

// AuthService handles the token and key half of authentication: JWT lifecycle,
// refresh-token rotation, API keys and namespace resolution. Wallet sign-in
// itself is not declared here — see the note inside.
type AuthService interface {
	// The wallet-login half of the service is not declared here.
	//
	// It used to be CreateNonce and VerifySignature, both of which took and
	// returned nothing but strings. What a wallet signs is a Sign-In-With
	// message now — EIP-4361 for EVM, its Solana counterpart for SOL — and the
	// pair is CreateChallenge / VerifySignedMessage in pkg/gateway/auth, which
	// exchange a rendered message and a parsed one. Declaring them here would
	// mean importing those types into a package whose whole rule is that no
	// concrete type appears in a signature, or restating them and having two
	// definitions of one message format.
	//
	// See pkg/gateway/auth/challenge.go and pkg/gateway/auth/siw.

	// IssueTokens generates a new access token and refresh token pair.
	// Access tokens are short-lived (typically 15 minutes).
	// Refresh tokens are long-lived (typically 30 days).
	// Returns: accessToken, refreshToken, expirationUnix, error.
	IssueTokens(ctx context.Context, wallet, namespace string) (string, string, int64, error)

	// RefreshToken atomically rotates a refresh token: validates the supplied
	// token, revokes it, mints a fresh refresh token alongside a new access
	// token, and returns both. RFC 9700 §4.12 / feature #68.
	// Returns: newAccessToken, newRefreshToken, subject (wallet), expirationUnix, error.
	// The error sentinel ErrRefreshTokenReplay indicates the CAS lock was lost
	// (concurrent use or replay attempt).
	RefreshToken(ctx context.Context, refreshToken, namespace string) (string, string, string, int64, error)

	// RevokeToken invalidates a refresh token or all tokens for a subject.
	// If token is provided, revokes that specific token.
	// If all is true and subject is provided, revokes all tokens for that subject.
	RevokeToken(ctx context.Context, namespace, token string, all bool, subject string) error

	// ParseAndVerifyJWT validates a JWT access token and returns its claims.
	// Verifies signature, expiration, and issuer.
	ParseAndVerifyJWT(token string) (*JWTClaims, error)

	// GenerateJWT creates a new signed JWT with the specified subject, TTL, and
	// optional additive custom claims (nil = none; bugboard #548).
	// Returns: token, expirationUnix, error.
	GenerateJWT(namespace, subject string, ttl time.Duration, custom map[string]string) (string, int64, error)

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

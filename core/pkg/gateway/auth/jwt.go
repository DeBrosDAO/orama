package auth

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Service) JWKSHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	keys := make([]any, 0, 2)

	// RSA key (RS256)
	if s.signingKey != nil {
		pub := s.signingKey.Public().(*rsa.PublicKey)
		n := pub.N.Bytes()
		eVal := pub.E
		eb := make([]byte, 0)
		for eVal > 0 {
			eb = append([]byte{byte(eVal & 0xff)}, eb...)
			eVal >>= 8
		}
		if len(eb) == 0 {
			eb = []byte{0}
		}
		keys = append(keys, map[string]string{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": s.keyID,
			"n":   base64.RawURLEncoding.EncodeToString(n),
			"e":   base64.RawURLEncoding.EncodeToString(eb),
		})
	}

	// Every Ed25519 key this cluster will accept a token from, not only this
	// gateway's own. A client that verifies a token locally needs the key that
	// signed it, and after key separation that is a different key per
	// namespace — a JWKS carrying one of them would make every other token
	// unverifiable.
	//
	// The namespace a key is bound to is published with it. It is not part of
	// the JWK standard, so it goes in as an additional member: a client that
	// does not know about it ignores it, and one that does can refuse a token
	// whose claim disagrees without asking the gateway.
	for _, key := range s.signingKeys.All() {
		keys = append(keys, map[string]string{
			"kty":       "OKP",
			"use":       "sig",
			"alg":       "EdDSA",
			"kid":       key.KID,
			"crv":       "Ed25519",
			"x":         base64.RawURLEncoding.EncodeToString(key.Public),
			"namespace": key.Namespace,
		})
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
}

// Internal types for JWT handling
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

type JWTClaims struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
	Aud string `json:"aud"`
	Iat int64  `json:"iat"`
	Nbf int64  `json:"nbf"`
	Exp int64  `json:"exp"`
	// Jti names this token. Without one a token could not be revoked
	// individually: logging out dropped the refresh token and left the access
	// token valid until it expired. Tokens minted before this have no jti and
	// are covered only by a revocation of their subject.
	Jti       string `json:"jti,omitempty"`
	Namespace string `json:"namespace"`
	// Custom holds app-defined claims (e.g. tier, subscription state).
	// Read by serverless functions via the get_caller_claim host call.
	// May be nil if the token has no custom claims.
	Custom map[string]string `json:"custom,omitempty"`
}

// revocationSubjectKeys returns the names a token's subject may have been
// revoked under.
//
// A wallet subject is revoked under itself. A token exchanged from an API key
// carries the raw key as its subject, while RevokeKey only ever has the hash —
// so the hash is the other name to look under.
func (s *Service) revocationSubjectKeys(subject string) []string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil
	}
	keys := []string{subject}
	if hashed := s.HashAPIKey(subject); hashed != "" && hashed != subject {
		keys = append(keys, hashed)
	}
	return keys
}

// newTokenID mints the name a token is revoked by.
//
// 128 bits from crypto/rand: it only has to be unique, and a guessable one
// would let somebody revoke a session they do not hold.
func newTokenID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("could not generate a token id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// ParseAndVerifyJWT verifies a JWT created by this gateway using kid-based key
// selection. It accepts both RS256 (legacy) and EdDSA (new) tokens.
//
// Security (C3 fix): The key is selected by kid, then cross-checked against alg
// to prevent algorithm confusion attacks. Only RS256 and EdDSA are accepted.
func (s *Service) ParseAndVerifyJWT(token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("invalid header encoding")
	}
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid payload encoding")
	}
	sb, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("invalid signature encoding")
	}

	var header jwtHeader
	if err := json.Unmarshal(hb, &header); err != nil {
		return nil, errors.New("invalid header json")
	}

	// Explicit algorithm allowlist — reject everything else before verification
	if header.Alg != "RS256" && header.Alg != "EdDSA" {
		return nil, errors.New("unsupported algorithm")
	}

	signingInput := parts[0] + "." + parts[1]

	// Key selection by kid (not alg) — prevents algorithm confusion (C3 fix)
	//
	// bound is the namespace the selected key may sign for. It is checked
	// against the claim after the claims are parsed: a namespace gateway's key
	// signs only for its own tenant, which is what stops one compromised
	// namespace gateway minting a token for another.
	var bound SigningKey
	edKey, edFound := s.signingKeys.Lookup(header.Kid)

	switch {
	case header.Kid != "" && edFound:
		// EdDSA key matched by kid — cross-check alg
		if header.Alg != "EdDSA" {
			return nil, errors.New("algorithm mismatch for key")
		}
		if !ed25519.Verify(edKey.Public, []byte(signingInput), sb) {
			return nil, errors.New("invalid signature")
		}
		bound = edKey

	case header.Kid != "" && header.Kid == s.keyID && s.signingKey != nil:
		// RSA key matched by kid — cross-check alg
		if header.Alg != "RS256" {
			return nil, errors.New("algorithm mismatch for key")
		}
		sum := sha256.Sum256([]byte(signingInput))
		pub := s.signingKey.Public().(*rsa.PublicKey)
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sb); err != nil {
			return nil, errors.New("invalid signature")
		}

	default:
		// A token with no kid, or one naming a key this gateway does not have.
		// There used to be a branch here that accepted a token with no kid at
		// all, verifying it against the RSA key — so a token that named no key
		// selected one by omission, which is the shape of an algorithm
		// confusion attack. This gateway has always put a kid in what it mints.
		return nil, errors.New("unknown key ID")
	}

	// Parse claims
	var claims JWTClaims
	if err := json.Unmarshal(pb, &claims); err != nil {
		return nil, errors.New("invalid claims json")
	}
	// Validate issuer
	if claims.Iss != "orama-gateway" {
		return nil, errors.New("invalid issuer")
	}
	// A key bound to a namespace signs only for that namespace. Without this
	// the binding is a label: the signature is what a verifier trusts, and a
	// namespace gateway that could sign any claim would be back where it
	// started.
	if !bound.Binds(claims.Namespace) {
		return nil, fmt.Errorf("the key %s signs for %q, and this token claims %q",
			bound.KID, bound.Namespace, claims.Namespace)
	}
	// A signature that verifies says the gateway minted this token, not that
	// the token is still good. Revoking a key used to stop the key and leave
	// its tokens working for the rest of their lifetime.
	if s.revocations.Denies(&claims, s.revocationSubjectKeys(claims.Sub)) {
		return nil, ErrTokenRevoked
	}
	// Validate registered claims
	now := time.Now().Unix()
	const skew = int64(60) // allow small clock skew ±60s
	if claims.Nbf != 0 && now+skew < claims.Nbf {
		return nil, errors.New("token not yet valid")
	}
	if claims.Exp != 0 && now-skew > claims.Exp {
		return nil, errors.New("token expired")
	}
	if claims.Iat != 0 && claims.Iat-skew > now {
		return nil, errors.New("invalid iat")
	}
	if claims.Aud != "gateway" {
		return nil, errors.New("invalid audience")
	}
	return &claims, nil
}

// AccessTokenLifetime is how long a minted access token is good for. It is the
// window a rotation has to leave the outgoing key verifiable for, and the
// window the previous cluster-derived key is accepted across an upgrade.
const AccessTokenLifetime = 15 * time.Minute

// GenerateJWT mints a signed access token. `custom` carries additive
// app-defined claims (e.g. the namespace's account_id from the claims-provider
// hook, bugboard #548) under the top-level "custom" object — read back via
// JWTClaims.Custom / oh.GetCallerClaim. Pass nil for none. Reserved claims
// (sub/iss/aud/iat/nbf/exp/namespace) are always gateway-controlled and cannot
// be overridden by `custom` (the caller is responsible for not putting
// reserved keys here; the claims-provider path sanitizes them out upstream).
func (s *Service) GenerateJWT(ns, subject string, ttl time.Duration, custom map[string]string) (string, int64, error) {
	// Prefer EdDSA when available
	if s.preferEdDSA && s.edSigningKey != nil {
		return s.generateEdDSAJWT(ns, subject, ttl, custom)
	}
	return s.generateRSAJWT(ns, subject, ttl, custom)
}

func (s *Service) generateEdDSAJWT(ns, subject string, ttl time.Duration, custom map[string]string) (string, int64, error) {
	if s.edSigningKey == nil {
		return "", 0, errors.New("EdDSA signing key unavailable")
	}
	header := map[string]string{
		"alg": "EdDSA",
		"typ": "JWT",
		"kid": s.edKeyID,
	}
	hb, _ := json.Marshal(header)
	now := time.Now().UTC()
	exp := now.Add(ttl)
	jti, err := newTokenID()
	if err != nil {
		return "", 0, err
	}
	payload := map[string]any{
		"iss":       "orama-gateway",
		"sub":       subject,
		"aud":       "gateway",
		"iat":       now.Unix(),
		"nbf":       now.Unix(),
		"exp":       exp.Unix(),
		"jti":       jti,
		"namespace": ns,
	}
	if len(custom) > 0 {
		payload["custom"] = custom
	}
	pb, _ := json.Marshal(payload)
	hb64 := base64.RawURLEncoding.EncodeToString(hb)
	pb64 := base64.RawURLEncoding.EncodeToString(pb)
	signingInput := hb64 + "." + pb64
	sig := ed25519.Sign(s.edSigningKey, []byte(signingInput))
	sb64 := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + sb64, exp.Unix(), nil
}

func (s *Service) generateRSAJWT(ns, subject string, ttl time.Duration, custom map[string]string) (string, int64, error) {
	if s.signingKey == nil {
		return "", 0, errors.New("signing key unavailable")
	}
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
		"kid": s.keyID,
	}
	hb, _ := json.Marshal(header)
	now := time.Now().UTC()
	exp := now.Add(ttl)
	jti, err := newTokenID()
	if err != nil {
		return "", 0, err
	}
	payload := map[string]any{
		"iss":       "orama-gateway",
		"sub":       subject,
		"aud":       "gateway",
		"iat":       now.Unix(),
		"nbf":       now.Unix(),
		"exp":       exp.Unix(),
		"jti":       jti,
		"namespace": ns,
	}
	if len(custom) > 0 {
		payload["custom"] = custom
	}
	pb, _ := json.Marshal(payload)
	hb64 := base64.RawURLEncoding.EncodeToString(hb)
	pb64 := base64.RawURLEncoding.EncodeToString(pb)
	signingInput := hb64 + "." + pb64
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.signingKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", 0, err
	}
	sb64 := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + sb64, exp.Unix(), nil
}

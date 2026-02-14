package auth

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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

	// Ed25519 key (EdDSA)
	if s.edSigningKey != nil {
		pubKey := s.edSigningKey.Public().(ed25519.PublicKey)
		keys = append(keys, map[string]string{
			"kty": "OKP",
			"use": "sig",
			"alg": "EdDSA",
			"kid": s.edKeyID,
			"crv": "Ed25519",
			"x":   base64.RawURLEncoding.EncodeToString(pubKey),
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
	Iss       string `json:"iss"`
	Sub       string `json:"sub"`
	Aud       string `json:"aud"`
	Iat       int64  `json:"iat"`
	Nbf       int64  `json:"nbf"`
	Exp       int64  `json:"exp"`
	Namespace string `json:"namespace"`
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
	switch {
	case header.Kid != "" && header.Kid == s.edKeyID && s.edSigningKey != nil:
		// EdDSA key matched by kid — cross-check alg
		if header.Alg != "EdDSA" {
			return nil, errors.New("algorithm mismatch for key")
		}
		pubKey := s.edSigningKey.Public().(ed25519.PublicKey)
		if !ed25519.Verify(pubKey, []byte(signingInput), sb) {
			return nil, errors.New("invalid signature")
		}

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

	case header.Kid == "":
		// Legacy token without kid — RS256 only (backward compat)
		if header.Alg != "RS256" {
			return nil, errors.New("legacy token must be RS256")
		}
		if s.signingKey == nil {
			return nil, errors.New("signing key unavailable")
		}
		sum := sha256.Sum256([]byte(signingInput))
		pub := s.signingKey.Public().(*rsa.PublicKey)
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sb); err != nil {
			return nil, errors.New("invalid signature")
		}

	default:
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

func (s *Service) GenerateJWT(ns, subject string, ttl time.Duration) (string, int64, error) {
	// Prefer EdDSA when available
	if s.preferEdDSA && s.edSigningKey != nil {
		return s.generateEdDSAJWT(ns, subject, ttl)
	}
	return s.generateRSAJWT(ns, subject, ttl)
}

func (s *Service) generateEdDSAJWT(ns, subject string, ttl time.Duration) (string, int64, error) {
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
	payload := map[string]any{
		"iss":       "orama-gateway",
		"sub":       subject,
		"aud":       "gateway",
		"iat":       now.Unix(),
		"nbf":       now.Unix(),
		"exp":       exp.Unix(),
		"namespace": ns,
	}
	pb, _ := json.Marshal(payload)
	hb64 := base64.RawURLEncoding.EncodeToString(hb)
	pb64 := base64.RawURLEncoding.EncodeToString(pb)
	signingInput := hb64 + "." + pb64
	sig := ed25519.Sign(s.edSigningKey, []byte(signingInput))
	sb64 := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + sb64, exp.Unix(), nil
}

func (s *Service) generateRSAJWT(ns, subject string, ttl time.Duration) (string, int64, error) {
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
	payload := map[string]any{
		"iss":       "orama-gateway",
		"sub":       subject,
		"aud":       "gateway",
		"iat":       now.Unix(),
		"nbf":       now.Unix(),
		"exp":       exp.Unix(),
		"namespace": ns,
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

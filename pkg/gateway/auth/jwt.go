package auth

import (
	"crypto"
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
	if s.signingKey == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
		return
	}
	pub := s.signingKey.Public().(*rsa.PublicKey)
	n := pub.N.Bytes()
	// Encode exponent as big-endian bytes
	eVal := pub.E
	eb := make([]byte, 0)
	for eVal > 0 {
		eb = append([]byte{byte(eVal & 0xff)}, eb...)
		eVal >>= 8
	}
	if len(eb) == 0 {
		eb = []byte{0}
	}
	jwk := map[string]string{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": s.keyID,
		"n":   base64.RawURLEncoding.EncodeToString(n),
		"e":   base64.RawURLEncoding.EncodeToString(eb),
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwk}})
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

// ParseAndVerifyJWT verifies an RS256 JWT created by this gateway and returns claims
func (s *Service) ParseAndVerifyJWT(token string) (*JWTClaims, error) {
	if s.signingKey == nil {
		return nil, errors.New("signing key unavailable")
	}
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
	if header.Alg != "RS256" {
		return nil, errors.New("unsupported alg")
	}
	// Verify signature
	signingInput := parts[0] + "." + parts[1]
	sum := sha256.Sum256([]byte(signingInput))
	pub := s.signingKey.Public().(*rsa.PublicKey)
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sb); err != nil {
		return nil, errors.New("invalid signature")
	}
	// Parse claims
	var claims JWTClaims
	if err := json.Unmarshal(pb, &claims); err != nil {
		return nil, errors.New("invalid claims json")
	}
	// Validate issuer
	if claims.Iss != "debros-gateway" {
		return nil, errors.New("invalid issuer")
	}
	// Validate registered claims
	now := time.Now().Unix()
	// allow small clock skew ±60s
	const skew = int64(60)
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
		"iss":       "debros-gateway",
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

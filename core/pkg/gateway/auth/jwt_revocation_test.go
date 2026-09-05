package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"
)

func base64RawURL(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func serviceWithRevocations(t *testing.T) (*Service, *revocationDB) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}
	db := &revocationDB{}
	s := &Service{apiKeyHMACSecret: "test-hmac-secret"}
	s.SetEdDSAKey(priv, "")
	s.revocations = NewRevocationList(registryOf(&revocationNet{db: db}), nil)
	return s, db
}

// The whole point: a token that verifies is not the same as a token that is
// still good.
func TestParseAndVerifyJWT_refusesARevokedToken(t *testing.T) {
	s, _ := serviceWithRevocations(t)

	token, _, err := s.GenerateJWT("ns", "0xwallet", time.Hour, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	claims, err := s.ParseAndVerifyJWT(token)
	if err != nil {
		t.Fatalf("a fresh token was refused: %v", err)
	}
	if claims.Jti == "" {
		t.Fatal("the token has no id, so it could never be revoked on its own")
	}

	if err := s.RevokeSession(context.Background(), claims); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	_, err = s.ParseAndVerifyJWT(token)
	if !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("a revoked token verified: %v", err)
	}
}

// Revoking an API key must reach the tokens already exchanged from it. The
// token carries the raw key as its subject; the revocation is recorded under
// the hash, which is all RevokeKey has.
func TestParseAndVerifyJWT_aKeysRevocationReachesItsTokens(t *testing.T) {
	s, _ := serviceWithRevocations(t)
	rawKey := "ak_runtime:acme"

	token, _, err := s.GenerateJWT("acme", rawKey, 15*time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	if _, err := s.ParseAndVerifyJWT(token); err != nil {
		t.Fatalf("a fresh token was refused: %v", err)
	}

	// What RevokeKey does: it has the hash, never the raw key.
	if err := s.revocations.RevokeSubject(context.Background(), s.HashAPIKey(rawKey), "key revoked", time.Hour); err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}

	if _, err := s.ParseAndVerifyJWT(token); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("a token exchanged from a revoked key still verifies: %v", err)
	}
}

func TestRevokeAllSessions_endsEveryTokenForASubject(t *testing.T) {
	s, _ := serviceWithRevocations(t)

	first, _, err := s.GenerateJWT("ns", "0xwallet", time.Hour, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	second, _, err := s.GenerateJWT("ns", "0xwallet", time.Hour, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	if err := s.RevokeAllSessions(context.Background(), "0xwallet"); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	for name, token := range map[string]string{"first": first, "second": second} {
		if _, err := s.ParseAndVerifyJWT(token); !errors.Is(err, ErrTokenRevoked) {
			t.Errorf("the %s session survived logging out everywhere: %v", name, err)
		}
	}
}

func TestRevokeSession_saysSoForATokenItCannotName(t *testing.T) {
	s, _ := serviceWithRevocations(t)
	err := s.RevokeSession(context.Background(), &JWTClaims{Sub: "0xwallet"}) // no jti
	if err == nil {
		t.Fatal("a token with no id was reported as revoked")
	}
	if !strings.Contains(err.Error(), "every session") {
		t.Errorf("the error does not say what to do instead: %v", err)
	}
}

// Two tokens must not share an id, or revoking one would revoke the other.
func TestGenerateJWT_mintsADistinctIDEachTime(t *testing.T) {
	s, _ := serviceWithRevocations(t)

	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		token, _, err := s.GenerateJWT("ns", "0xwallet", time.Hour, nil)
		if err != nil {
			t.Fatalf("GenerateJWT: %v", err)
		}
		claims, err := s.ParseAndVerifyJWT(token)
		if err != nil {
			t.Fatalf("ParseAndVerifyJWT: %v", err)
		}
		if seen[claims.Jti] {
			t.Fatalf("two tokens share the id %q; revoking one would revoke the other", claims.Jti)
		}
		seen[claims.Jti] = true
	}
}

// A token that names no key used to select one by omission, verified against
// the RSA key — the shape of an algorithm confusion attack. This gateway has
// always put a kid in what it mints.
func TestParseAndVerifyJWT_refusesATokenWithNoKeyID(t *testing.T) {
	s, _ := serviceWithRevocations(t)

	token, _, err := s.GenerateJWT("ns", "0xwallet", time.Hour, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	parts := strings.Split(token, ".")
	// Re-encode the header without its kid, keeping the signature.
	stripped := strings.Join([]string{
		base64RawURL(`{"alg":"EdDSA","typ":"JWT"}`), parts[1], parts[2],
	}, ".")

	if _, err := s.ParseAndVerifyJWT(stripped); err == nil {
		t.Fatal("a token naming no key was accepted")
	}
}

// A short RSA key signs tokens anybody can forge, and the size was never
// checked.
func TestNewService_refusesAnRSAKeyTooSmallToTrust(t *testing.T) {
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(small),
	})

	if _, err := NewService(nil, nil, string(pemBytes), "default"); err == nil {
		t.Fatal("a 1024-bit signing key was accepted")
	} else if !strings.Contains(err.Error(), "2048") {
		t.Errorf("the refusal does not say what the minimum is: %v", err)
	}
}

func TestNewService_acceptsAnRSAKeyAtTheFloor(t *testing.T) {
	if testing.Short() {
		t.Skip("generating a 2048-bit key is slow")
	}
	key, err := rsa.GenerateKey(rand.Reader, minRSAKeyBits)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if _, err := NewService(nil, nil, string(pemBytes), "default"); err != nil {
		t.Fatalf("a %d-bit key was refused: %v", minRSAKeyBits, err)
	}
}

// A Service built with a database always consults the revocations. A Service
// that verified tokens against a database and did not would be the fifteen
// minute window again.
func TestNewService_wiresTheRevocationsWheneverThereIsADatabase(t *testing.T) {
	s, err := NewService(nil, &revocationNet{db: &revocationDB{}}, "", "default")
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if s.Revocations() == nil {
		t.Fatal("a Service with a database has no revocation list, so nothing it verifies can be revoked")
	}
}

package gateway

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

func TestJWTGenerateAndParse(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	svc, err := auth.NewService(nil, nil, string(keyPEM), "default")
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	tok, exp, err := svc.GenerateJWT("ns1", "subj", time.Minute)
	if err != nil || exp <= 0 {
		t.Fatalf("gen err=%v exp=%d", err, exp)
	}

	claims, err := svc.ParseAndVerifyJWT(tok)
	if err != nil {
		t.Fatalf("verify err: %v", err)
	}
	if claims.Namespace != "ns1" || claims.Sub != "subj" || claims.Aud != "gateway" || claims.Iss != "debros-gateway" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestJWTExpired(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	svc, err := auth.NewService(nil, nil, string(keyPEM), "default")
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	// Use sufficiently negative TTL to bypass allowed clock skew
	tok, _, err := svc.GenerateJWT("ns1", "subj", -2*time.Minute)
	if err != nil {
		t.Fatalf("gen err=%v", err)
	}
	if _, err := svc.ParseAndVerifyJWT(tok); err == nil {
		t.Fatalf("expected expired error")
	}
}

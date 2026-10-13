package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// These are also in orama/caddy/vectors_test.go; see caddy_vectors_test.go.

func TestACMEPayloadNonced_matchesTheCaddyDNSProvider(t *testing.T) {
	mac := hmac.New(sha256.New, []byte("provider-test-key"))
	mac.Write([]byte(acmePayloadNonced("POST", "/v1/internal/acme/present", "",
		[]byte(`{"fqdn":"_acme-challenge.example.com.","value":"x"}`), "00112233445566778899aabbccddeeff", 1790000000)))
	const want = "979cf48ef2b884a0b5bad5129fd9a4c471bc3e9b031587e7d82f1b5675b82af8"
	if got := hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Fatalf("nonced ACME MAC = %s, want %s", got, want)
	}
}

func TestCoordinationPayloadV3_matchesTheCaddyStorageModule(t *testing.T) {
	key, _ := hex.DecodeString("619c0812d37db0ca397535bf2e097d1554f6f72e784d327cdd463281b33e5700")
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(coordinationPayloadV3("POST", "caddy-tls-store", "6001", "/v1/internal/tls-store", "",
		[]byte(`{"op":"load","key":"a"}`), "00112233445566778899aabbccddeeff", 1790000000)))
	const want = "e9c2a85ba619b989508ab660c2f98e006aa81cb37113f768a3a3ec1860795ff6"
	if got := hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Fatalf("coordination v3 MAC = %s, want %s", got, want)
	}
}

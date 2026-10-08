package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// Caddy's modules (orama/caddy) sign their calls to the gateway and cannot
// import this package, so the constructions are written twice. These vectors
// are also in orama/caddy/vectors_test.go: a drift on either side fails a test
// instead of a renewal on a node.

func TestACMEPayload_matchesTheCaddyDNSProvider(t *testing.T) {
	mac := hmac.New(sha256.New, []byte("provider-test-key"))
	mac.Write([]byte(acmePayload("POST", "/v1/internal/acme/present", "",
		[]byte(`{"fqdn":"_acme-challenge.example.com.","value":"x"}`), 1790000000)))
	const want = "932194e4680fbf6651b9cf0642913158f59c7a25f4092808e906c12af317c419"
	if got := hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Fatalf("ACME MAC = %s, want %s", got, want)
	}
}

func TestCoordinationPayloadV2_matchesTheCaddyStorageModule(t *testing.T) {
	key, _ := hex.DecodeString("619c0812d37db0ca397535bf2e097d1554f6f72e784d327cdd463281b33e5700")
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(coordinationPayloadV2("POST", "caddy-tls-store", "/v1/internal/tls-store", "",
		[]byte(`{"op":"load","key":"a"}`), "00112233445566778899aabbccddeeff", 1790000000)))
	const want = "37ec7972806cd402f94ead221547054c3238dee9d8bfe8dbcd61d6ecc100863a"
	if got := hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Fatalf("coordination v2 MAC = %s, want %s", got, want)
	}
}

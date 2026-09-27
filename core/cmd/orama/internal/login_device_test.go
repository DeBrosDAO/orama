package cli

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLoginDevice_keepsThePrivateHalfLocal(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "device.jwk")
	raw, _ := json.Marshal(map[string]string{
		"kty": "OKP", "crv": "Ed25519",
		"x": base64.RawURLEncoding.EncodeToString(pub),
		"d": base64.RawURLEncoding.EncodeToString(priv),
	})
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	device, err := loadLoginDevice(file)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]string
	if err := json.Unmarshal(device.PublicJWK, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["d"] != "" || sent["x"] == "" {
		t.Fatalf("public JWK = %s", device.PublicJWK)
	}
	sig, err := device.Sign("the sign-in message")
	if err != nil {
		t.Fatal(err)
	}
	rawSig, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !ed25519.Verify(pub, []byte("the sign-in message"), rawSig) {
		t.Fatalf("device signature does not verify")
	}
}

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
	device, err := loadLoginDevice(writeJWK(t, pub, priv.Seed()))
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

func writeJWK(t *testing.T, x ed25519.PublicKey, d []byte) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "device.jwk")
	raw, err := json.Marshal(map[string]string{
		"kty": "OKP", "crv": "Ed25519",
		"x": base64.RawURLEncoding.EncodeToString(x),
		"d": base64.RawURLEncoding.EncodeToString(d),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestLoadLoginDevice_refusesAKeyThatIsNotAnRFC8037Seed(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]string{
		"the 64-byte Go form":     writeJWK(t, pub, priv),
		"an empty seed":           writeJWK(t, pub, nil),
		"a seed of another key":   writeJWK(t, other, priv.Seed()),
		"a short seed":            writeJWK(t, pub, priv.Seed()[:16]),
		"a missing key file path": filepath.Join(t.TempDir(), "absent.jwk"),
	} {
		if _, err := loadLoginDevice(file); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

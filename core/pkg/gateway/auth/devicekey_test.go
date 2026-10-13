package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
	"testing"
)

// testDevice is a device holding a key, as a platform would.
type testDevice struct {
	jwk  string
	sign func(message []byte) string
	id   string
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// p256Device signs the way WebCrypto and CryptoKit do: 64 bytes, r||s.
func p256Device(t *testing.T) testDevice {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	pad := func(n *big.Int) []byte { return n.FillBytes(make([]byte, 32)) }
	jwk := `{"kty":"EC","crv":"P-256","x":"` + b64(pad(priv.X)) + `","y":"` + b64(pad(priv.Y)) + `"}`
	key, err := ParseDeviceKey([]byte(jwk))
	if err != nil {
		t.Fatalf("parse the device's own key: %v", err)
	}
	return testDevice{jwk: jwk, id: key.ID(), sign: func(message []byte) string {
		digest := sha256.Sum256(message)
		r, s, err := ecdsa.Sign(rand.Reader, priv, digest[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return b64(append(pad(r), pad(s)...))
	}}
}

func ed25519Device(t *testing.T) testDevice {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	jwk := `{"kty":"OKP","crv":"Ed25519","x":"` + b64(pub) + `"}`
	key, err := ParseDeviceKey([]byte(jwk))
	if err != nil {
		t.Fatalf("parse the device's own key: %v", err)
	}
	return testDevice{jwk: jwk, id: key.ID(), sign: func(message []byte) string {
		return b64(ed25519.Sign(priv, message))
	}}
}

// RFC 8037 appendix A.3: the thumbprint of its example Ed25519 key.
func TestDeviceKeyID_isTheRFC7638Thumbprint(t *testing.T) {
	key, err := ParseDeviceKey([]byte(`{"kty":"OKP","crv":"Ed25519","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k"; key.ID() != want {
		t.Errorf("thumbprint = %s, want %s", key.ID(), want)
	}
	if !ValidDeviceID(key.ID()) {
		t.Error("a real thumbprint is not a valid device id")
	}
}

// The id does not depend on how the client laid its JSON out or what else it
// put in it: members outside the thumbprint are ignored.
func TestDeviceKeyID_ignoresMemberOrderAndExtras(t *testing.T) {
	a, _ := ParseDeviceKey([]byte(`{"kty":"OKP","crv":"Ed25519","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}`))
	b, err := ParseDeviceKey([]byte(`{"x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo","use":"sig","crv":"Ed25519","kty":"OKP","kid":"mine"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if a.ID() != b.ID() || a.JWK() != b.JWK() {
		t.Errorf("one key, two ids: %s and %s", a.ID(), b.ID())
	}
}

func TestDeviceKeyVerify_acceptsEachPlatformsSignature(t *testing.T) {
	msg := []byte("orama-device-proof-v1\nrefresh\nanchat\ntoken\n1800000000\nabcdefghijklmnop")
	for name, device := range map[string]testDevice{"P-256": p256Device(t), "Ed25519": ed25519Device(t)} {
		key, err := ParseDeviceKey([]byte(device.jwk))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if err := key.Verify(msg, device.sign(msg)); err != nil {
			t.Errorf("%s: a genuine signature was refused: %v", name, err)
		}
		if err := key.Verify(append(msg, '!'), device.sign(msg)); !errors.Is(err, ErrDeviceSignatureInvalid) {
			t.Errorf("%s: a signature over other bytes was accepted: %v", name, err)
		}
	}
}

// Android Keystore and SecKey hand back ASN.1 DER.
func TestDeviceKeyVerify_acceptsADERSignature(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pad := func(n *big.Int) []byte { return n.FillBytes(make([]byte, 32)) }
	key, err := ParseDeviceKey([]byte(`{"kty":"EC","crv":"P-256","x":"` + b64(pad(priv.X)) + `","y":"` + b64(pad(priv.Y)) + `"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	msg := []byte("sign me")
	digest := sha256.Sum256(msg)
	der, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := key.Verify(msg, b64(der)); err != nil {
		t.Errorf("a DER signature was refused: %v", err)
	}
}

func TestParseDeviceKey_refusals(t *testing.T) {
	good := p256Device(t).jwk
	for name, raw := range map[string]string{
		"empty":                 ``,
		"not json":              `nope`,
		"a private key":         strings.Replace(good, `"kty"`, `"d":"c2VjcmV0","kty"`, 1),
		"RSA":                   `{"kty":"RSA","n":"AQAB","e":"AQAB"}`,
		"P-384":                 `{"kty":"EC","crv":"P-384","x":"AA","y":"AA"}`,
		"a point off the curve": `{"kty":"EC","crv":"P-256","x":"` + b64(make([]byte, 32)) + `","y":"` + b64(append(make([]byte, 31), 1)) + `"}`,
		"a short Ed25519 key":   `{"kty":"OKP","crv":"Ed25519","x":"` + b64(make([]byte, 31)) + `"}`,
		"padded base64":         `{"kty":"OKP","crv":"Ed25519","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo="}`,
		"standard alphabet":     `{"kty":"OKP","crv":"Ed25519","x":"11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo"}`,
		"oversized":             `{"kty":"OKP","crv":"Ed25519","x":"` + strings.Repeat("A", maxDeviceKeyBytes) + `"}`,
	} {
		if _, err := ParseDeviceKey([]byte(raw)); !errors.Is(err, ErrDeviceKeyInvalid) {
			t.Errorf("%s: accepted, or refused for the wrong reason: %v", name, err)
		}
	}
}

func TestValidDeviceID_acceptsOnlyAThumbprintsShape(t *testing.T) {
	for id, want := range map[string]bool{
		"kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k": true,
		"":      false,
		"short": false,
		"kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k=": false,
		"kPrK/qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k":  false,
	} {
		if got := ValidDeviceID(id); got != want {
			t.Errorf("ValidDeviceID(%q) = %v, want %v", id, got, want)
		}
	}
}

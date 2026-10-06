package cli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/auth"
	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// loadLoginDevice reads an Ed25519 private JWK and keeps the private half
// here. The value handed to the gateway is the public JWK.
func loadLoginDevice(path string) (*auth.LoginDevice, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, clierr.Failure("read device key: %w", err)
	}
	var stored struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		X   string `json:"x"`
		D   string `json:"d"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, clierr.Usage("device key must be a JSON JWK: %v", err)
	}
	if stored.Kty != "OKP" || stored.Crv != "Ed25519" || stored.X == "" || stored.D == "" {
		return nil, clierr.Usage("device key must be an Ed25519 private JWK (kty OKP, crv Ed25519, x and d)")
	}
	// RFC 8037 makes d the 32-byte seed, not Go's 64-byte seed-and-public form.
	seed, err := base64.RawURLEncoding.DecodeString(stored.D)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, clierr.Usage("device key d is not an Ed25519 private key (RFC 8037: the %d-byte seed, base64url)", ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	if x, err := base64.RawURLEncoding.DecodeString(stored.X); err != nil || !bytes.Equal(x, priv.Public().(ed25519.PublicKey)) {
		return nil, clierr.Usage("device key x is not the public key of d")
	}
	public, err := json.Marshal(struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		X   string `json:"x"`
	}{stored.Kty, stored.Crv, stored.X})
	if err != nil {
		return nil, clierr.Failure("encode the public device key: %w", err)
	}
	key, err := authsvc.ParseDeviceKey(public)
	if err != nil {
		return nil, clierr.Usage("device key: %v", err)
	}
	return &auth.LoginDevice{
		ID:        key.ID(),
		PublicJWK: public,
		Sign: func(message string) (string, error) {
			return base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(message))), nil
		},
	}, nil
}

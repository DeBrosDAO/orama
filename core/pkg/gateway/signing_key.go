package gateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
	"golang.org/x/crypto/hkdf"
)

const jwtKeyFileName = "jwt-signing-key.pem"

// eddsaKeyFileName is where this gateway's own signing key lives. The auth
// package names the same file, because a rotation overwrites exactly what the
// next boot reads; a test holds the two together.
const eddsaKeyFileName = auth.EdDSAKeyFileName

// loadOrCreateSigningKey loads the JWT signing key from disk, or generates a new one
// if none exists. This ensures JWTs survive gateway restarts.
func loadOrCreateSigningKey(dataDir string, logger *logging.ColoredLogger) ([]byte, error) {
	keyPath := filepath.Join(dataDir, "secrets", jwtKeyFileName)

	// Try to load existing key
	if keyPEM, err := os.ReadFile(keyPath); err == nil && len(keyPEM) > 0 {
		// Verify the key is valid
		block, _ := pem.Decode(keyPEM)
		if block != nil {
			if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
				logger.ComponentInfo(logging.ComponentGeneral, "Loaded existing JWT signing key",
					zap.String("path", keyPath))
				return keyPEM, nil
			}
		}
		logger.ComponentWarn(logging.ComponentGeneral, "Existing JWT signing key is invalid, generating new one",
			zap.String("path", keyPath))
	}

	// Generate new key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate RSA key: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	// Ensure secrets directory exists
	secretsDir := filepath.Dir(keyPath)
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		return nil, fmt.Errorf("create secrets directory: %w", err)
	}

	// Write key with restrictive permissions
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("write signing key: %w", err)
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Generated and saved new JWT signing key",
		zap.String("path", keyPath))
	return keyPEM, nil
}

// jwtEdDSADerivePurpose is the HKDF label the Ed25519 signing seed used to be
// derived from the cluster secret with.
//
// Nothing signs with it any more. Every node holds the cluster secret, so a key
// derived from it is a key every node can mint any namespace's tokens with —
// which is the hole this replaces. It is kept only so that tokens minted before
// the upgrade keep verifying for the length of one access token; see
// LegacyClusterSigningKey.
const jwtEdDSADerivePurpose = "orama-jwt-eddsa-v1"

// loadOrCreateEdSigningKey returns the Ed25519 key this gateway signs with,
// generating and persisting one the first time.
//
// It used to derive the key from the cluster secret so that every gateway in
// the cluster held the same one. That is exactly what made a compromised
// namespace gateway able to mint a token for any tenant. Each gateway has its
// own key now, and the public halves are published so the others can verify.
func loadOrCreateEdSigningKey(dataDir string, logger *logging.ColoredLogger) (ed25519.PrivateKey, error) {
	keyPath := filepath.Join(dataDir, "secrets", eddsaKeyFileName)

	if keyPEM, err := os.ReadFile(keyPath); err == nil && len(keyPEM) > 0 {
		block, _ := pem.Decode(keyPEM)
		if block != nil {
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err == nil {
				if edKey, ok := parsed.(ed25519.PrivateKey); ok {
					logger.ComponentInfo(logging.ComponentGeneral, "Loaded existing EdDSA signing key",
						zap.String("path", keyPath))
					return edKey, nil
				}
			}
		}
		// Refusing is the only safe answer. Generating a replacement would
		// silently invalidate every token this gateway has issued, and
		// overwrite the only copy of a key that might be recoverable.
		return nil, fmt.Errorf("the EdDSA signing key at %s cannot be read; move it aside to have a new one generated, "+
			"which invalidates every token this gateway has issued", keyPath)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate Ed25519 key: %w", err)
	}

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal Ed25519 key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes})

	secretsDir := filepath.Dir(keyPath)
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		return nil, fmt.Errorf("create secrets directory: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("write EdDSA signing key: %w", err)
	}
	if err := os.Chmod(keyPath, 0600); err != nil {
		return nil, fmt.Errorf("restrict EdDSA signing key: %w", err)
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Generated an EdDSA signing key for this gateway",
		zap.String("path", keyPath))
	return priv, nil
}

// LegacyClusterSigningKey returns the key every gateway derived from the
// cluster secret before each got its own.
//
// It is added as verify-only, and only for one access-token lifetime after
// boot. Tokens minted before the upgrade have to keep working across it; after
// that window a key every node can derive must not verify anything, or the
// separation this change exists for would hold for the new keys and not at all
// for the old one.
func LegacyClusterSigningKey(clusterSecret string) (ed25519.PublicKey, error) {
	seed, err := deriveEd25519Seed(clusterSecret)
	if err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey), nil
}

// deriveEd25519Seed derives a deterministic 32-byte seed for Ed25519 from the
// cluster secret using HKDF-SHA256 with a stable purpose label. Same secret +
// same label = same seed = same keypair on every gateway in the cluster.
func deriveEd25519Seed(clusterSecret string) ([]byte, error) {
	if clusterSecret == "" {
		return nil, fmt.Errorf("cluster secret is empty")
	}
	reader := hkdf.New(sha256.New, []byte(clusterSecret), nil, []byte(jwtEdDSADerivePurpose))
	seed := make([]byte, ed25519.SeedSize)
	if _, err := io.ReadFull(reader, seed); err != nil {
		return nil, fmt.Errorf("HKDF read failed: %w", err)
	}
	return seed, nil
}

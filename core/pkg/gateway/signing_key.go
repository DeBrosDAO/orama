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

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
	"golang.org/x/crypto/hkdf"
)

// jwtKeyFileName is this gateway's RSA signing key, inside its state directory.
const jwtKeyFileName = constants.GatewayRSAKeyFileName

// eddsaKeyFileName is where this gateway's own signing key lives. The auth
// package names the same file, because a rotation overwrites exactly what the
// next boot reads; a test holds the two together.
const eddsaKeyFileName = auth.EdDSAKeyFileName

// loadOrCreateSigningKey loads the JWT signing key from this gateway's state
// directory, or generates a new one if none exists. This ensures JWTs survive
// gateway restarts.
func loadOrCreateSigningKey(stateDir string, logger *logging.ColoredLogger) ([]byte, error) {
	keyPath := filepath.Join(stateDir, jwtKeyFileName)

	if keyPEM, err := os.ReadFile(keyPath); err == nil && len(keyPEM) > 0 {
		block, _ := pem.Decode(keyPEM)
		if block != nil {
			if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
				logger.ComponentInfo(logging.ComponentGeneral, "Loaded existing JWT signing key",
					zap.String("path", keyPath))
				return keyPEM, nil
			}
		}
		// Refusing, as for the EdDSA key: a replacement would silently
		// invalidate every token this gateway issued and overwrite the only
		// copy of a key that might be recoverable.
		return nil, fmt.Errorf("the JWT signing key at %s cannot be read; move it aside to have a new one generated, "+
			"which invalidates every token this gateway has issued", keyPath)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read the JWT signing key %s: %w", keyPath, err)
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

	// Write key with restrictive permissions. The state directory already
	// exists: ensureStateDir created it, 0700, before anything reads it.
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("write the JWT signing key %s: %w", keyPath, err)
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
// generating and persisting one in its state directory the first time.
//
// It used to derive the key from the cluster secret so that every gateway in
// the cluster held the same one. That is exactly what made a compromised
// namespace gateway able to mint a token for any tenant. Each gateway has its
// own key now, and the public halves are published so the others can verify.
//
// A key on disk that IS the cluster-derived one — what every 0.122.x node
// wrote, and what the upgrade carries into the index gateway's state
// directory — is replaced rather than loaded: every node can compute it, so
// signing with it would reopen the hole. Nothing it signed is lost; the
// legacy key stays verify-only for one token lifetime (LegacyClusterSigningKey).
func loadOrCreateEdSigningKey(stateDir, clusterSecret string, logger *logging.ColoredLogger) (ed25519.PrivateKey, bool, error) {
	keyPath := auth.SigningKeyPath(stateDir)

	// migrated is true only when the key on disk was the cluster-derived one
	// and this call replaced it. Callers arm that key for one token lifetime
	// in that case and in no other.
	migrated := false
	keyPEM, err := os.ReadFile(keyPath)
	switch {
	case err == nil && len(keyPEM) > 0:
		edKey, perr := parseEdSigningKey(keyPEM)
		if perr != nil {
			// Refusing is the only safe answer. Generating a replacement would
			// silently invalidate every token this gateway has issued, and
			// overwrite the only copy of a key that might be recoverable.
			return nil, false, fmt.Errorf("the EdDSA signing key at %s cannot be read (%v); move it aside to have a new one generated, "+
				"which invalidates every token this gateway has issued", keyPath, perr)
		}
		shared, derr := isClusterDerivedKey(edKey, clusterSecret)
		if derr != nil {
			return nil, false, derr
		}
		if !shared {
			logger.ComponentInfo(logging.ComponentGeneral, "Loaded existing EdDSA signing key",
				zap.String("path", keyPath))
			return edKey, false, nil
		}
		migrated = true
		logger.ComponentWarn(logging.ComponentGeneral,
			"The EdDSA signing key on disk is the cluster-derived key every node can compute; replacing it with this gateway's own",
			zap.String("path", keyPath))
	case err != nil && !os.IsNotExist(err):
		return nil, false, fmt.Errorf("read the EdDSA signing key %s: %w", keyPath, err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, fmt.Errorf("generate Ed25519 key: %w", err)
	}
	if err := auth.PersistSigningKey(stateDir, priv); err != nil {
		return nil, false, err
	}

	logger.ComponentInfo(logging.ComponentGeneral, "Generated an EdDSA signing key for this gateway",
		zap.String("path", keyPath))
	return priv, migrated, nil
}

// parseEdSigningKey reads a PKCS#8 Ed25519 private key.
func parseEdSigningKey(keyPEM []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("not PKCS#8: %w", err)
	}
	edKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("a %T, not an Ed25519 key", parsed)
	}
	return edKey, nil
}

// isClusterDerivedKey reports whether key is the one every node derives from
// the cluster secret. With no cluster secret there is nothing to derive, so no
// key can be it.
func isClusterDerivedKey(key ed25519.PrivateKey, clusterSecret string) (bool, error) {
	if clusterSecret == "" {
		return false, nil
	}
	legacy, err := LegacyClusterSigningKey(clusterSecret)
	if err != nil {
		return false, fmt.Errorf("derive the legacy cluster signing key to compare against: %w", err)
	}
	return legacy.Equal(key.Public()), nil
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

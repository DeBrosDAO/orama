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

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
	"golang.org/x/crypto/hkdf"
)

const jwtKeyFileName = "jwt-signing-key.pem"
const eddsaKeyFileName = "jwt-eddsa-key.pem"

// jwtEdDSADerivePurpose is the HKDF info string used to derive the cluster-wide
// Ed25519 JWT signing seed from the cluster secret. Bumping the version label
// (e.g. "...-v2") on disk = full re-derive + invalidates old tokens.
const jwtEdDSADerivePurpose = "orama-jwt-eddsa-v1"

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

// loadOrCreateEdSigningKey loads or generates an Ed25519 private key for EdDSA JWT signing.
//
// Bug #215 fix: when a non-empty clusterSecret is provided, the key is derived
// DETERMINISTICALLY from the cluster secret using HKDF-SHA256. This means every
// gateway in the cluster ends up with the same Ed25519 keypair, so a JWT signed
// by one gateway can be verified by any other gateway. Without this, each
// gateway minted its own random key on first boot, JWTs were unverifiable
// cross-gateway, and host functions saw empty `caller_jwt_subject` whenever
// invoke landed on a different node than `/v1/auth/login`.
//
// When clusterSecret is empty (single-node test rigs, no shared secret),
// falls back to the legacy random-per-node behaviour.
//
// On-disk PEM is the source of truth at runtime; if it doesn't match the
// derivation (e.g. left over from before this fix, or cluster secret rotated
// the label), the file is rewritten with the canonical key. Old tokens become
// unverifiable but JWT lifetime is short (15 min) so the disruption is bounded.
func loadOrCreateEdSigningKey(dataDir, clusterSecret string, logger *logging.ColoredLogger) (ed25519.PrivateKey, error) {
	keyPath := filepath.Join(dataDir, "secrets", eddsaKeyFileName)

	// Compute the canonical key for this cluster (if a secret is available).
	// Empty clusterSecret = legacy mode, no canonical key, just use whatever
	// is on disk or freshly generated.
	var canonical ed25519.PrivateKey
	if clusterSecret != "" {
		seed, err := deriveEd25519Seed(clusterSecret)
		if err != nil {
			return nil, fmt.Errorf("derive Ed25519 seed from cluster secret: %w", err)
		}
		canonical = ed25519.NewKeyFromSeed(seed)
	}

	// Try to load existing key
	if keyPEM, err := os.ReadFile(keyPath); err == nil && len(keyPEM) > 0 {
		block, _ := pem.Decode(keyPEM)
		if block != nil {
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err == nil {
				if edKey, ok := parsed.(ed25519.PrivateKey); ok {
					// If we have a canonical cluster-derived key, the on-disk
					// key MUST match it. Otherwise we'd silently keep using a
					// per-node random key and JWTs wouldn't verify cross-node.
					if canonical != nil && !ed25519.PrivateKey(edKey).Equal(canonical) {
						logger.ComponentWarn(logging.ComponentGeneral,
							"On-disk EdDSA key does not match cluster-derived key; rewriting from cluster secret",
							zap.String("path", keyPath))
						// Fall through to write canonical below.
					} else {
						logger.ComponentInfo(logging.ComponentGeneral, "Loaded existing EdDSA signing key",
							zap.String("path", keyPath))
						return edKey, nil
					}
				}
			}
		}
		if canonical == nil {
			logger.ComponentWarn(logging.ComponentGeneral, "Existing EdDSA signing key is invalid, generating new one",
				zap.String("path", keyPath))
		}
	}

	// Either nothing on disk, key was unparseable, or it didn't match the
	// canonical cluster-derived key. Use canonical when available; otherwise
	// generate a random one (legacy single-node fallback).
	priv := canonical
	if priv == nil {
		_, generated, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate Ed25519 key: %w", err)
		}
		priv = generated
	}

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal Ed25519 key: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	})

	// Ensure secrets directory exists
	secretsDir := filepath.Dir(keyPath)
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		return nil, fmt.Errorf("create secrets directory: %w", err)
	}

	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("write EdDSA signing key: %w", err)
	}

	source := "random per-node"
	if canonical != nil {
		source = "cluster-derived (HKDF)"
	}
	logger.ComponentInfo(logging.ComponentGeneral, "Saved EdDSA signing key",
		zap.String("path", keyPath),
		zap.String("source", source))
	return priv, nil
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

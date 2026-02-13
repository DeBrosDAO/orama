package gateway

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

const jwtKeyFileName = "jwt-signing-key.pem"

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

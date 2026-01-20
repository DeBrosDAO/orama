package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/certutil"
)

// ensureCertificatesForDomain generates self-signed certificates for the domain
func ensureCertificatesForDomain(domain string) error {
	// Get home directory
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create cert directory
	certDir := filepath.Join(home, ".orama", "certs")
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return fmt.Errorf("failed to create cert directory: %w", err)
	}

	// Create certificate manager
	cm := certutil.NewCertificateManager(certDir)

	// Ensure CA certificate exists
	caCertPEM, caKeyPEM, err := cm.EnsureCACertificate()
	if err != nil {
		return fmt.Errorf("failed to ensure CA certificate: %w", err)
	}

	// Ensure node certificate exists for the domain
	_, _, err = cm.EnsureNodeCertificate(domain, caCertPEM, caKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to ensure node certificate: %w", err)
	}

	// Also create wildcard certificate if domain is not already wildcard
	if !strings.HasPrefix(domain, "*.") {
		wildcardDomain := "*." + domain
		_, _, err = cm.EnsureNodeCertificate(wildcardDomain, caCertPEM, caKeyPEM)
		if err != nil {
			return fmt.Errorf("failed to ensure wildcard certificate: %w", err)
		}
	}

	return nil
}

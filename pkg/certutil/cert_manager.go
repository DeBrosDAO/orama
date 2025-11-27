// Package certutil provides utilities for managing self-signed certificates
package certutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// CertificateManager manages self-signed certificates for the network
type CertificateManager struct {
	baseDir string
}

// NewCertificateManager creates a new certificate manager
func NewCertificateManager(baseDir string) *CertificateManager {
	return &CertificateManager{
		baseDir: baseDir,
	}
}

// EnsureCACertificate creates or loads the CA certificate
func (cm *CertificateManager) EnsureCACertificate() ([]byte, []byte, error) {
	caCertPath := filepath.Join(cm.baseDir, "ca.crt")
	caKeyPath := filepath.Join(cm.baseDir, "ca.key")

	// Check if CA already exists
	if _, err := os.Stat(caCertPath); err == nil {
		certPEM, err := os.ReadFile(caCertPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		keyPEM, err := os.ReadFile(caKeyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read CA key: %w", err)
		}
		return certPEM, keyPEM, nil
	}

	// Create new CA certificate
	certPEM, keyPEM, err := cm.generateCACertificate()
	if err != nil {
		return nil, nil, err
	}

	// Ensure directory exists
	if err := os.MkdirAll(cm.baseDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("failed to create cert directory: %w", err)
	}

	// Write to files
	if err := os.WriteFile(caCertPath, certPEM, 0644); err != nil {
		return nil, nil, fmt.Errorf("failed to write CA certificate: %w", err)
	}
	if err := os.WriteFile(caKeyPath, keyPEM, 0600); err != nil {
		return nil, nil, fmt.Errorf("failed to write CA key: %w", err)
	}

	return certPEM, keyPEM, nil
}

// EnsureNodeCertificate creates or loads a node certificate signed by the CA
func (cm *CertificateManager) EnsureNodeCertificate(hostname string, caCertPEM, caKeyPEM []byte) ([]byte, []byte, error) {
	certPath := filepath.Join(cm.baseDir, fmt.Sprintf("%s.crt", hostname))
	keyPath := filepath.Join(cm.baseDir, fmt.Sprintf("%s.key", hostname))

	// Check if certificate already exists
	if _, err := os.Stat(certPath); err == nil {
		certData, err := os.ReadFile(certPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read certificate: %w", err)
		}
		keyData, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read key: %w", err)
		}
		return certData, keyData, nil
	}

	// Create new certificate
	certPEM, keyPEM, err := cm.generateNodeCertificate(hostname, caCertPEM, caKeyPEM)
	if err != nil {
		return nil, nil, err
	}

	// Write to files
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return nil, nil, fmt.Errorf("failed to write certificate: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, nil, fmt.Errorf("failed to write key: %w", err)
	}

	return certPEM, keyPEM, nil
}

// generateCACertificate generates a self-signed CA certificate
func (cm *CertificateManager) generateCACertificate() ([]byte, []byte, error) {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "DeBros Network Root CA",
			Organization: []string{"DeBros"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0), // 10 year validity
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	// Self-sign the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key to PEM
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyDER,
	})

	return certPEM, keyPEM, nil
}

// generateNodeCertificate generates a certificate signed by the CA
func (cm *CertificateManager) generateNodeCertificate(hostname string, caCertPEM, caKeyPEM []byte) ([]byte, []byte, error) {
	// Parse CA certificate and key
	caCert, caKey, err := cm.parseCACertificate(caCertPEM, caKeyPEM)
	if err != nil {
		return nil, nil, err
	}

	// Generate node private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName: hostname,
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().AddDate(5, 0, 0), // 5 year validity
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{hostname},
	}

	// Add wildcard support if hostname contains *.debros.network
	if hostname == "*.debros.network" {
		template.DNSNames = []string{"*.debros.network", "debros.network"}
	} else if hostname == "debros.network" {
		template.DNSNames = []string{"*.debros.network", "debros.network"}
	}

	// Try to parse as IP address for IP-based certificates
	if ip := net.ParseIP(hostname); ip != nil {
		template.IPAddresses = []net.IP{ip}
		template.DNSNames = nil
	}

	// Sign certificate with CA
	certDER, err := x509.CreateCertificate(rand.Reader, &template, caCert, &privateKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key to PEM
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyDER,
	})

	return certPEM, keyPEM, nil
}

// parseCACertificate parses CA certificate and key from PEM
func (cm *CertificateManager) parseCACertificate(caCertPEM, caKeyPEM []byte) (*x509.Certificate, *rsa.PrivateKey, error) {
	// Parse CA certificate
	certBlock, _ := pem.Decode(caCertPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("failed to parse CA certificate PEM")
	}

	caCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// Parse CA private key
	keyBlock, _ := pem.Decode(caKeyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("failed to parse CA key PEM")
	}

	caKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA key: %w", err)
	}

	rsaKey, ok := caKey.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("CA key is not RSA")
	}

	return caCert, rsaKey, nil
}

// LoadTLSCertificate loads a TLS certificate from PEM files
func LoadTLSCertificate(certPEM, keyPEM []byte) (tls.Certificate, error) {
	return tls.X509KeyPair(certPEM, keyPEM)
}


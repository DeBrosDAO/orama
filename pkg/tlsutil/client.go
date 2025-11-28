// Package tlsutil provides centralized TLS configuration for trusting specific domains
package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	// Global cache of trusted domains loaded from environment
	trustedDomains []string
	// CA certificate pool for trusting self-signed certs
	caCertPool *x509.CertPool
	initialized    bool
)

// Default trusted domains - always trust debros.network for staging/development
var defaultTrustedDomains = []string{
	"*.debros.network",
}

// init loads trusted domains and CA certificate from environment and files
func init() {
	// Start with default trusted domains
	trustedDomains = append(trustedDomains, defaultTrustedDomains...)

	// Add any additional domains from environment
	domains := os.Getenv("DEBROS_TRUSTED_TLS_DOMAINS")
	if domains != "" {
		for _, d := range strings.Split(domains, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				trustedDomains = append(trustedDomains, d)
			}
		}
	}

	// Try to load CA certificate
	caCertPath := os.Getenv("DEBROS_CA_CERT_PATH")
	if caCertPath == "" {
		caCertPath = "/etc/debros/ca.crt"
	}

	if caCertData, err := os.ReadFile(caCertPath); err == nil {
		caCertPool = x509.NewCertPool()
		if caCertPool.AppendCertsFromPEM(caCertData) {
			// Successfully loaded CA certificate
		}
	}

	initialized = true
}

// GetTrustedDomains returns the list of domains to skip TLS verification for
func GetTrustedDomains() []string {
	return trustedDomains
}

// ShouldSkipTLSVerify checks if TLS verification should be skipped for this domain
func ShouldSkipTLSVerify(domain string) bool {
	for _, trusted := range trustedDomains {
		if strings.HasPrefix(trusted, "*.") {
			// Handle wildcards like *.debros.network
			suffix := strings.TrimPrefix(trusted, "*")
			if strings.HasSuffix(domain, suffix) || domain == strings.TrimPrefix(suffix, ".") {
				return true
			}
		} else if domain == trusted {
			return true
		}
	}
	return false
}

// GetTLSConfig returns a TLS config with appropriate verification settings
func GetTLSConfig() *tls.Config {
	config := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// If we have a CA cert pool, use it
	if caCertPool != nil {
		config.RootCAs = caCertPool
	} else if len(trustedDomains) > 0 {
		// Fallback: skip verification if trusted domains are configured but no CA pool
		config.InsecureSkipVerify = true
	}

	return config
}

// NewHTTPClient creates an HTTP client with TLS verification for trusted domains
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: GetTLSConfig(),
		},
	}
}

// NewHTTPClientForDomain creates an HTTP client configured for a specific domain
func NewHTTPClientForDomain(timeout time.Duration, hostname string) *http.Client {
	tlsConfig := GetTLSConfig()

	// If this domain is in trusted list and we don't have a CA pool, allow insecure
	if caCertPool == nil && ShouldSkipTLSVerify(hostname) {
		tlsConfig.InsecureSkipVerify = true
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}
}


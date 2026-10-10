// Package tlsutil provides centralized TLS configuration for trusting specific domains
package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	// Global cache of trusted domains loaded from environment
	trustedDomains []string
	// CA certificate pool for trusting self-signed certs
	caCertPool *x509.CertPool
)

// Default trusted domains - always trust orama.network for staging/development
var defaultTrustedDomains = []string{
	"*.orama.network",
}

// init loads trusted domains and CA certificate from environment and files
func init() {
	// Start with default trusted domains
	trustedDomains = append(trustedDomains, defaultTrustedDomains...)

	// Add any additional domains from environment
	domains := os.Getenv("ORAMA_TRUSTED_TLS_DOMAINS")
	if domains != "" {
		for _, d := range strings.Split(domains, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				trustedDomains = append(trustedDomains, d)
			}
		}
	}

	// Try to load CA certificate
	caCertPath := os.Getenv("ORAMA_CA_CERT_PATH")
	if caCertPath == "" {
		caCertPath = "/etc/orama/ca.crt"
	}

	if caCertData, err := os.ReadFile(caCertPath); err == nil {
		caCertPool = x509.NewCertPool()
		if caCertPool.AppendCertsFromPEM(caCertData) {
			// Successfully loaded CA certificate
		}
	}
}

// GetTrustedDomains returns the list of domains to skip TLS verification for
func GetTrustedDomains() []string {
	return trustedDomains
}

// ShouldSkipTLSVerify checks if TLS verification should be skipped for this domain
func ShouldSkipTLSVerify(domain string) bool {
	for _, trusted := range trustedDomains {
		if strings.HasPrefix(trusted, "*.") {
			// Handle wildcards like *.orama.network
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

	// If we have a CA cert pool, use it for verifying self-signed certs
	if caCertPool != nil {
		config.RootCAs = caCertPool
	}

	return withScopedRoots(config)
}

// idleConnTimeout closes a pooled keep-alive connection nobody has reused for
// this long, the same bound http.DefaultTransport applies. The shared pool
// holds at most MaxIdleConnsPerHost idle connections per host; this also lets
// the ones to hosts that are never contacted again go.
const idleConnTimeout = 90 * time.Second

// The one connection pool behind every client built here.
//
// NewHTTPClient used to build a new http.Transport on each call, and most
// callers call it per request (the rqlite admin client, readiness probes,
// health checks). A Transport is a connection pool: every request left its
// keep-alive connection parked in a pool nothing would ever use again, with no
// idle timeout to close it. On a node that leaked one connection to the local
// rqlited per health check — about ten a minute, each a goroutine on both ends —
// until the process was restarted (bugboard 2729).
var (
	transportMu  sync.Mutex
	transport    *http.Transport
	transportGen uint64 // scopedGeneration the transport's TLS config was built at
)

// sharedTransport returns the process-wide transport, rebuilt only when the
// scoped roots change, since GetTLSConfig bakes them into the TLS config.
func sharedTransport() *http.Transport {
	transportMu.Lock()
	defer transportMu.Unlock()

	gen := scopedGeneration()
	if transport != nil && transportGen == gen {
		return transport
	}
	if transport != nil {
		// Clients already holding the old transport keep working; only its
		// parked connections are released.
		transport.CloseIdleConnections()
	}
	transport = &http.Transport{
		TLSClientConfig: GetTLSConfig(),
		IdleConnTimeout: idleConnTimeout,
	}
	transportGen = gen
	return transport
}

// NewHTTPClient returns a client with TLS verification for trusted domains.
//
// Clients share one connection pool, so building one per request is cheap and
// reuses keep-alive connections. Callers must not modify the Transport.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: sharedTransport(),
	}
}

// NewHTTPClientForDomain creates an HTTP client configured for a specific domain.
// Only skips TLS verification for explicitly trusted domains when no CA cert is available.
// It shares NewHTTPClient's connection pool.
func NewHTTPClientForDomain(timeout time.Duration, hostname string) *http.Client {
	_ = hostname // domain is for callers; verification uses system/Caddy CAs (bugboard #112)
	return NewHTTPClient(timeout)
}

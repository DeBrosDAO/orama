package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

// Scoped roots let a client trust a private CA for one cluster's domain
// without trusting it for anything else. A test cluster on Let's Encrypt's
// staging CA, or a private cluster with its own CA, is otherwise unreachable:
// the only lever was ORAMA_CA_CERT_PATH, which replaces the system roots for
// every host the process talks to.
var (
	scopedMu    sync.RWMutex
	scopedRoots = map[string]*x509.CertPool{} // domain → its extra CAs
)

// TrustCAForDomain trusts the PEM certificates in caFile as roots for domain
// and every name under it, in addition to the system roots.
func TrustCAForDomain(domain, caFile string) error {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain == "" {
		return errors.New("a CA must be scoped to a domain")
	}
	pemData, err := os.ReadFile(caFile)
	if err != nil {
		return fmt.Errorf("read CA file %s for %s: %w", caFile, domain, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return fmt.Errorf("CA file %s for %s holds no PEM certificate", caFile, domain)
	}
	scopedMu.Lock()
	scopedRoots[domain] = pool
	scopedMu.Unlock()
	return nil
}

// hasScopedRoots reports whether any domain has a CA of its own.
func hasScopedRoots() bool {
	scopedMu.RLock()
	defer scopedMu.RUnlock()
	return len(scopedRoots) > 0
}

// scopedPoolFor is the domain's own CA pool for serverName, or nil.
func scopedPoolFor(serverName string) *x509.CertPool {
	name := strings.ToLower(strings.TrimSuffix(serverName, "."))
	scopedMu.RLock()
	defer scopedMu.RUnlock()
	for domain, pool := range scopedRoots {
		if name == domain || strings.HasSuffix(name, "."+domain) {
			return pool
		}
	}
	return nil
}

// withScopedRoots returns cfg verifying each server against the roots its name
// is scoped to. crypto/tls takes one root pool per config, so the chain check
// moves into VerifyConnection, which runs the same x509 verification with the
// pool chosen by server name; InsecureSkipVerify only turns off the built-in
// check that this replaces. With no scoped roots cfg is returned unchanged.
func withScopedRoots(cfg *tls.Config) *tls.Config {
	if !hasScopedRoots() {
		return cfg
	}
	out := cfg.Clone()
	base := cfg.RootCAs // nil means the system roots
	out.InsecureSkipVerify = true
	out.VerifyConnection = func(cs tls.ConnectionState) error {
		return verifyScoped(cs, base)
	}
	return out
}

func verifyScoped(cs tls.ConnectionState, base *x509.CertPool) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("server presented no certificate")
	}
	if cs.ServerName == "" {
		return errors.New("no server name to verify the certificate against")
	}
	intermediates := x509.NewCertPool()
	for _, c := range cs.PeerCertificates[1:] {
		intermediates.AddCert(c)
	}
	opts := x509.VerifyOptions{DNSName: cs.ServerName, Roots: base, Intermediates: intermediates}
	_, err := cs.PeerCertificates[0].Verify(opts)
	if err == nil {
		return nil
	}
	if pool := scopedPoolFor(cs.ServerName); pool != nil {
		opts.Roots = pool
		if _, scopedErr := cs.PeerCertificates[0].Verify(opts); scopedErr == nil {
			return nil
		}
	}
	return fmt.Errorf("verify certificate for %s: %w", cs.ServerName, err)
}

// InstallScopedRoots applies the scoped roots to http.DefaultTransport, which
// every client built without its own transport uses. Call it once at startup,
// after TrustCAForDomain; with no scoped roots it changes nothing.
func InstallScopedRoots() error {
	if !hasScopedRoots() {
		return nil
	}
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return fmt.Errorf("http.DefaultTransport is a %T, not an *http.Transport", http.DefaultTransport)
	}
	base := t.TLSClientConfig
	if base == nil {
		base = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	t.TLSClientConfig = withScopedRoots(base)
	return nil
}

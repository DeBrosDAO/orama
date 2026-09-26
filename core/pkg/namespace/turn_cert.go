package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// caddyCertificatesDir holds one directory per ACME issuer, named after
	// the issuer's directory URL (Let's Encrypt production is
	// acme-v02.api.letsencrypt.org-directory, staging
	// acme-staging-v02.api.letsencrypt.org-directory).
	caddyCertificatesDir = "certificates"
	// caddyDefaultIssuerDir is the directory of Caddy's default issuer, used
	// to name the expected path of a certificate that does not exist yet.
	caddyDefaultIssuerDir = "acme-v02.api.letsencrypt.org-directory"

	// caddyServiceStorageDir is where the Caddy service (User=orama,
	// HOME=/var/lib/caddy) persists its ACME certificates on a node. TURNS
	// reuses the wildcard certificate from here; nothing in orama-node writes
	// Caddy's configuration (it runs ProtectSystem=strict and could not).
	caddyServiceStorageDir = "/var/lib/caddy/caddy"
)

// caddyWildcardCertPaths returns the cert/key file paths for the
// `*.<baseDomain>` wildcard certificate in the Caddy service's storage. Caddy
// names the wildcard directory `wildcard_.<baseDomain>`. The gateway already
// provisions this wildcard for HTTPS, so a single-label subdomain of the base
// domain (e.g. the stealth TURNS host `cdn-<hash>.<baseDomain>`) is covered by
// it without any per-domain provisioning.
func caddyWildcardCertPaths(baseDomain string) (certPath, keyPath string) {
	return locateCaddyCert(caddyServiceStorageDir, "wildcard_."+baseDomain)
}

// locateCaddyCert finds the cert and key Caddy stored for name under
// storageDir, whichever ACME issuer produced them: the issuer is configurable
// (orama node install --acme-ca), and the directory is named after it. When
// several issuers hold one — the CA was switched — the most recently written
// wins, since Caddy renews only from the configured issuer. When none does,
// it returns where the default issuer would put it, so a caller's Stat reports
// "not provisioned yet".
func locateCaddyCert(storageDir, name string) (certPath, keyPath string) {
	matches, _ := filepath.Glob(filepath.Join(storageDir, caddyCertificatesDir, "*", name, name+".crt"))
	var newest time.Time
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil {
			continue
		}
		if certPath == "" || st.ModTime().After(newest) {
			certPath, newest = m, st.ModTime()
		}
	}
	if certPath == "" {
		certPath = filepath.Join(storageDir, caddyCertificatesDir, caddyDefaultIssuerDir, name, name+".crt")
	}
	return certPath, strings.TrimSuffix(certPath, ".crt") + ".key"
}

package tlsstore

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// certificatesPrefix is where CertMagic keeps certificates: one directory per
// ACME issuer, then one per name, holding <name>.crt, <name>.key and
// <name>.json. A wildcard's directory is "wildcard_." followed by its base.
const certificatesPrefix = "certificates/"

// exportProbeLabel names a host the wildcard must cover for an export to be
// taken: any single label under the base domain does.
const exportProbeLabel = "turn-probe"

// Exporter writes the cluster's `*.<base>` certificate and key to files, for
// the services that terminate TLS themselves (the shared TURN server's TURNS
// listener and its stealth hosts). Caddy keeps its certificates in the store
// rather than on disk, so this is the only copy a process outside Caddy reads.
type Exporter struct {
	db         *sql.DB
	sealKey    []byte
	baseDomain string
	certPath   string
	keyPath    string
	now        func() time.Time
}

// NewExporter returns an exporter that reads the wildcard for baseDomain from
// the store in db (the cluster registry) and writes it to certPath and keyPath.
func NewExporter(db *sql.DB, sealKey []byte, baseDomain, certPath, keyPath string) *Exporter {
	return &Exporter{
		db: db, sealKey: sealKey, baseDomain: strings.ToLower(strings.Trim(baseDomain, ".")),
		certPath: certPath, keyPath: keyPath, now: time.Now,
	}
}

// Export writes the newest stored wildcard certificate when it differs from
// the files. It reports whether it wrote. A store holding no wildcard yet is
// ErrNotExist: the first node to obtain it has not finished.
//
// The key is written before the certificate. The TURN server reloads the pair
// when the certificate file changes, so it never reads a new certificate with
// the old key.
func (e *Exporter) Export(ctx context.Context) (bool, error) {
	if e.baseDomain == "" {
		return false, fmt.Errorf("no base domain, so there is no *.<base> certificate to export")
	}
	certKey, err := e.newestWildcard(ctx)
	if err != nil {
		return false, err
	}
	keyKey := strings.TrimSuffix(certKey, ".crt") + ".key"
	certPEM, err := e.open(ctx, certKey)
	if err != nil {
		return false, err
	}
	keyPEM, err := e.open(ctx, keyKey)
	if err != nil {
		return false, err
	}
	if err := e.check(certPEM, keyPEM); err != nil {
		return false, fmt.Errorf("stored certificate %s: %w", certKey, err)
	}
	if sameFile(e.certPath, certPEM) && sameFile(e.keyPath, keyPEM) {
		return false, nil
	}
	if err := writeAtomic(e.keyPath, keyPEM); err != nil {
		return false, err
	}
	if err := writeAtomic(e.certPath, certPEM); err != nil {
		return false, err
	}
	return true, nil
}

// newestWildcard is the key of the most recently stored `*.<base>`
// certificate, whichever issuer it came from. Caddy obtains and renews only
// from the configured CA, so the newest is the one it is renewing.
func (e *Exporter) newestWildcard(ctx context.Context) (string, error) {
	name := "wildcard_." + e.baseDomain
	rows, err := rqlite.SafeQueryContext(e.db, ctx,
		`SELECT key FROM tls_store WHERE substr(key, 1, ?) = ? ORDER BY modified_unix_ms DESC`,
		under(strings.TrimSuffix(certificatesPrefix, "/"))...)
	if err != nil {
		return "", fmt.Errorf("find the *.%s certificate in the TLS store: %w", e.baseDomain, err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return "", fmt.Errorf("find the *.%s certificate in the TLS store: %w", e.baseDomain, err)
		}
		// certificates/<issuer>/<name>/<name>.crt: exactly four components.
		if strings.Count(key, "/") == 3 && strings.HasSuffix(key, "/"+name+"/"+name+".crt") {
			return key, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("find the *.%s certificate in the TLS store: %w", e.baseDomain, err)
	}
	return "", fmt.Errorf("*.%s: %w (no node has obtained it yet)", e.baseDomain, ErrNotExist)
}

func (e *Exporter) open(ctx context.Context, key string) ([]byte, error) {
	sealed, err := NewStore(e.db).Load(ctx, key)
	if err != nil {
		return nil, err
	}
	return Open(e.sealKey, key, sealed)
}

// check refuses a pair TURN could not serve: a key that is not the
// certificate's, a certificate that has expired, or one that does not cover a
// single label under the base domain.
func (e *Exporter) check(certPEM, keyPEM []byte) error {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("certificate and key do not form a pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse the certificate: %w", err)
	}
	if !e.now().Before(leaf.NotAfter) {
		return fmt.Errorf("expired at %s", leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	if err := leaf.VerifyHostname(exportProbeLabel + "." + e.baseDomain); err != nil {
		return fmt.Errorf("does not cover *.%s: %w", e.baseDomain, err)
	}
	return nil
}

func sameFile(path string, want []byte) bool {
	have, err := os.ReadFile(path)
	return err == nil && bytes.Equal(have, want)
}

// writeAtomic replaces path with data, owner-only, through a rename so a
// reader sees the old file or the new one and never a partial one.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// IsNotExist reports whether err means the store does not hold what was asked.
func IsNotExist(err error) bool { return errors.Is(err, ErrNotExist) }

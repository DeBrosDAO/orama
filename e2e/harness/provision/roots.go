package provision

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// stagingRootsSource is the Let's Encrypt staging roots bundle `orama
// sandbox` ships (core/cmd/orama/internal/sandbox/stagingroots.go); the run
// trusts the same file, checked against the same pinned fingerprints.
const stagingRootsSource = "core/cmd/orama/internal/sandbox/letsencrypt-staging-roots.pem"

// stagingRootFingerprints are the SHA-256 fingerprints of "(STAGING) Pretend
// Pear X1" and "(STAGING) Bogus Broccoli X2", the bundle's only certificates.
var stagingRootFingerprints = []string{
	"9b2a339fe6a3e85585c4cd75536cb8c1cf7cd603b9a64bec2521858ae48da85d",
	"e70570a989f8565aabdf7cae27abd1621872d6a3f811e3fef27e3dba02912198",
}

// tlsDialTimeout bounds one certificate probe.
const tlsDialTimeout = 10 * time.Second

// checkStagingRoots refuses a bundle that is not exactly the pinned roots.
func checkStagingRoots(bundle []byte) error {
	var got []string
	for rest := bundle; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("failed to parse a staging root: %w", err)
		}
		if !cert.IsCA {
			return fmt.Errorf("staging root %q is not a CA", cert.Subject.CommonName)
		}
		sum := sha256.Sum256(cert.Raw)
		got = append(got, hex.EncodeToString(sum[:]))
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(stagingRootFingerprints, ",") {
		return fmt.Errorf("the staging roots are %v, not the pinned %v", got, stagingRootFingerprints)
	}
	return nil
}

// writeStagingRoots copies the checked roots into the run as its CA file.
func writeStagingRoots(repoRoot, dest string) error {
	src := filepath.Join(repoRoot, stagingRootsSource)
	bundle, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read the Let's Encrypt staging roots %s: %w", src, err)
	}
	if err := checkStagingRoots(bundle); err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	if err := os.WriteFile(dest, bundle, secretMode); err != nil {
		return fmt.Errorf("failed to write %s: %w", dest, err)
	}
	return nil
}

// waitCertificate polls addr until it serves, for domain, a certificate that
// verifies against the roots in caFile (and only those), or ctx ends.
func waitCertificate(ctx context.Context, addr, domain, caFile string, interval time.Duration) error {
	bundle, err := os.ReadFile(caFile)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(bundle) {
		return fmt.Errorf("%s holds no certificate", caFile)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		err := probeCertificate(ctx, addr, domain, pool)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s served no valid certificate for %s before the deadline (last: %v): %w", addr, domain, err, ctx.Err())
		case <-ticker.C:
		}
	}
}

func probeCertificate(ctx context.Context, addr, domain string, pool *x509.CertPool) error {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: tlsDialTimeout},
		Config:    &tls.Config{ServerName: domain, RootCAs: pool, MinVersion: tls.VersionTLS12},
	}
	dctx, cancel := context.WithTimeout(ctx, tlsDialTimeout)
	defer cancel()
	conn, err := dialer.DialContext(dctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}
